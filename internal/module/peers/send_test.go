package peers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
)

// ---------------------------------------------------------------------------
// postDeliver: the outbound client against httptest.
// ---------------------------------------------------------------------------

func sampleDeliverRequest() ipeers.DeliverRequest {
	return ipeers.DeliverRequest{
		MsgID: "0f7a3d2e-5b1c-4a8e-9d3f-2c6b7e8a9f01",
		From: ipeers.WireFrom{
			HostID: localHostID, AgentSessionID: targetSessionID, PID: targetPID, ProcStart: targetProcStart,
			PeerName: targetPeerName, SessionName: "cc:" + targetPeerName, DeclaredMode: ipeers.ModePrompting,
		},
		To:   ipeers.WireTo{AgentSessionID: remoteSessionID, PID: remotePID, ProcStart: remoteProcStart},
		Text: "hello from mlab",
	}
}

func TestPostDeliver_OK(t *testing.T) {
	want := sampleDeliverRequest()
	var gotReq ipeers.DeliverRequest
	var gotAuth, gotPath, gotMethod, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth, gotCT = r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Errorf("server decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ipeers.DeliverResponse{MsgID: want.MsgID, Result: ipeers.ResultDelivered, EffectiveMode: ipeers.ModeBypass, OneWay: true})
	}))
	defer srv.Close()

	resp, remote, err := postDeliver(context.Background(), newDeliverClient(), srv.URL, "outbound-tok", want)
	if err != nil || remote != nil {
		t.Fatalf("postDeliver = remote %+v err %v, want nil/nil", remote, err)
	}
	if resp.MsgID != want.MsgID || resp.Result != ipeers.ResultDelivered || resp.EffectiveMode != ipeers.ModeBypass || !resp.OneWay {
		t.Errorf("resp = %+v", resp)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/peers/deliver" {
		t.Errorf("request = %s %s, want POST /api/peers/deliver", gotMethod, gotPath)
	}
	if gotAuth != "Bearer outbound-tok" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer outbound-tok")
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	if gotReq != want {
		t.Errorf("server saw %+v, want %+v", gotReq, want)
	}
}

func TestPostDeliver_APIErrorBecomesRemoteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(ipeers.APIError{Error: ipeers.ErrTargetGone, Detail: "pid does not match the live session"})
	}))
	defer srv.Close()

	resp, remote, err := postDeliver(context.Background(), newDeliverClient(), srv.URL, "tok", sampleDeliverRequest())
	if err != nil {
		t.Fatalf("err = %v, want nil (a remote refusal is not a transport error)", err)
	}
	if remote == nil || remote.Status != http.StatusConflict || remote.Error != ipeers.ErrTargetGone || remote.Detail != "pid does not match the live session" {
		t.Errorf("remote = %+v, want {409 target_gone pid…}", remote)
	}
	if resp != (ipeers.DeliverResponse{}) {
		t.Errorf("resp = %+v, want zero", resp)
	}
}

func TestPostDeliver_NonJSONStatusBecomesHTTPCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "<html><body>boom</body></html>")
	}))
	defer srv.Close()

	_, remote, err := postDeliver(context.Background(), newDeliverClient(), srv.URL, "tok", sampleDeliverRequest())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if remote == nil || remote.Status != 500 || remote.Error != "http_500" || remote.Detail != "" {
		t.Errorf("remote = %+v, want {500 http_500}", remote)
	}
}

func TestPostDeliver_RedirectNotFollowed(t *testing.T) {
	var leaked atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
	}))
	defer sink.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL+"/api/peers/deliver", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	_, remote, err := postDeliver(context.Background(), newDeliverClient(), srv.URL, "tok", sampleDeliverRequest())
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if remote == nil || remote.Status != http.StatusTemporaryRedirect || remote.Error != "http_307" {
		t.Errorf("remote = %+v, want {307 http_307}", remote)
	}
	if leaked.Load() != 0 {
		t.Errorf("redirect target was hit %d times; the bearer must never follow a redirect", leaked.Load())
	}
}

func TestPostDeliver_OversizedBodyIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"msg_id":"` + strings.Repeat("x", 65*1024) + `"}`))
	}))
	defer srv.Close()

	_, remote, err := postDeliver(context.Background(), newDeliverClient(), srv.URL, "tok", sampleDeliverRequest())
	if err == nil || remote != nil {
		t.Fatalf("postDeliver = remote %+v err %v, want a transport-class error for a 65 KiB body", remote, err)
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want it to name the cap", err)
	}
}

func TestPostDeliver_Timeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release) // before srv.Close (LIFO): the parked handler must be let go first

	client := newDeliverClient()
	client.Timeout = 50 * time.Millisecond
	start := time.Now()
	_, remote, err := postDeliver(context.Background(), client, srv.URL, "tok", sampleDeliverRequest())
	if err == nil || remote != nil {
		t.Fatalf("postDeliver = remote %+v err %v, want a timeout error", remote, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v, want the 50 ms client timeout to bound the call", time.Since(start))
	}
}

// TestPostDeliver_UnexpectedOKBodyIsError pins that a 200 whose body is
// not a DeliverResponse this daemon would accept — a result outside
// {delivered, delivery_uncertain}, or an effective_mode that is empty or
// not a mode — is a transport-class error (bounded text), never a
// response: the remote's fields must not reach an audit row or a caller.
func TestPostDeliver_UnexpectedOKBodyIsError(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"result x", `{"msg_id":"m","result":"x","effective_mode":"prompting"}`},
		{"result huge", `{"msg_id":"m","result":"` + strings.Repeat("r", 4096) + `","effective_mode":"prompting"}`},
		{"effective_mode root", `{"msg_id":"m","result":"delivered","effective_mode":"root"}`},
		{"effective_mode empty", `{"msg_id":"m","result":"delivered"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()

			resp, remote, err := postDeliver(context.Background(), newDeliverClient(), srv.URL, "tok", sampleDeliverRequest())
			if err == nil || remote != nil {
				t.Fatalf("postDeliver = resp %+v remote %+v err %v, want a transport-class error", resp, remote, err)
			}
			if resp != (ipeers.DeliverResponse{}) {
				t.Errorf("resp = %+v, want zero", resp)
			}
			if !strings.HasPrefix(err.Error(), "decode response: ") {
				t.Errorf("err = %v, want a decode error", err)
			}
			if len(err.Error()) > 2*maxRemoteTextBytes {
				t.Errorf("err is %d bytes, want bounded", len(err.Error()))
			}
		})
	}
}

// TestSend_UnexpectedDeliverBodyNotEchoed drives the real postDeliver
// through /send against a peer answering 200 with an unbounded, unknown
// result: the caller gets remote_error (status 0), no SendResponse; the
// audit row records no result/mode and a bounded error.
func TestSend_UnexpectedDeliverBodyNotEchoed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"msg_id":"m","result":"` + strings.Repeat("r", 4096) + `","effective_mode":"root"}`))
	}))
	defer srv.Close()
	host := airHost(remoteToken, false)
	host.URL = srv.URL
	s := newSendEnv(t, envOpts{hosts: []config.PeerHost{host}})
	s.m.post = postDeliver

	ae := assertRefused(t, s.send(adminCtx(), s.sendReq()), http.StatusBadGateway, ipeers.ErrRemoteError)
	if ae.Remote == nil || ae.Remote.Status != 0 || !strings.HasPrefix(ae.Remote.Error, "decode response: ") {
		t.Errorf("remote = %+v, want status 0 with the decode error", ae.Remote)
	}
	if ae.Remote != nil && len(ae.Remote.Error) > maxRemoteTextBytes+len("…") {
		t.Errorf("remote error is %d bytes, want bounded", len(ae.Remote.Error))
	}
	row := s.onlyRow()
	if row.Result != "" || row.EffectiveMode != "" {
		t.Errorf("row result/mode = %q/%q, want both empty (the remote's fields must not be echoed)", row.Result, row.EffectiveMode)
	}
	if !strings.HasPrefix(row.Error, "decode response: ") || len(row.Error) > maxRemoteTextBytes+len("…") {
		t.Errorf("row error = %q (%d bytes), want the bounded decode error", row.Error, len(row.Error))
	}
}

func TestNewDeliverClient(t *testing.T) {
	c := newDeliverClient()
	if c.Timeout != ipeers.InterDaemonTimeout {
		t.Errorf("Timeout = %v, want %v", c.Timeout, ipeers.InterDaemonTimeout)
	}
	if c.CheckRedirect == nil || !errors.Is(c.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Errorf("CheckRedirect must refuse every redirect with ErrUseLastResponse")
	}
}

// ---------------------------------------------------------------------------
// Scaffolding: a sending daemon. The receiving-daemon env (deliver_test.go)
// is reused as-is: its "target" cc session (registry entry bound to a live
// Unix listener) is the ORIGIN here, and "air" is the remote host entry.
// ---------------------------------------------------------------------------

const (
	remoteSessionID = "99999999-8888-4777-8666-555555555555"
	remotePID       = 777
	remoteProcStart = "Tue Sep 15 09:00:00 2026"
	remoteSession   = "foo"
	remoteEntryURL  = "http://air.invalid:7860"
	remoteToken     = "outbound-air"
)

// postCall is one captured call of the fake post seam.
type postCall struct {
	baseURL string
	bearer  string
	req     ipeers.DeliverRequest
}

// fetchCall is one captured call of the fake fetch seam.
type fetchCall struct {
	baseURL string
	bearer  string
}

type sendEnv struct {
	*deliverEnv
	mu      sync.Mutex
	fetches []fetchCall
	posts   []postCall

	// What the fakes answer; set before the request.
	env      ipeers.Envelope
	fetchErr error
	postResp ipeers.DeliverResponse
	postRem  *ipeers.RemoteError
	postErr  error
}

// remoteRow is one deliverable cc row as the remote host "air" reports it
// (its own alias/host_id/address — normalised by the sender).
func remoteRow(sessionName, sessionCode string) ipeers.PeerRecord {
	addr := remoteAlias + "/" + sessionName
	if sessionName == "" {
		addr = remoteAlias + "/cc:" + remoteSession
	}
	return ipeers.PeerRecord{
		Host:         remoteAlias,
		HostID:       remoteHostID,
		Address:      addr,
		SessionCode:  sessionCode,
		SessionName:  sessionName,
		TmuxInstance: "air-inst",
		Agent: &ipeers.AgentInfo{
			Type: "cc", SessionID: remoteSessionID, PeerName: remoteSession,
			PID: remotePID, ProcStart: remoteProcStart, Inbox: "/tmp/cc-socks/777.sock", Status: "idle", Version: "2.1.270",
		},
		Deliverable: true,
	}
}

func remoteEnvelope(rows ...ipeers.PeerRecord) ipeers.Envelope {
	if rows == nil {
		rows = []ipeers.PeerRecord{}
	}
	return ipeers.Envelope{HostID: remoteHostID, OK: true, Peers: rows}
}

func newSendEnv(t *testing.T, o envOpts) *sendEnv {
	t.Helper()
	s := &sendEnv{
		deliverEnv: newDeliverEnv(t, o),
		env:        remoteEnvelope(remoteRow(remoteSession, "fooc")),
		postResp:   ipeers.DeliverResponse{Result: ipeers.ResultDelivered, EffectiveMode: ipeers.ModePrompting},
	}
	s.m.fetch = func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		s.mu.Lock()
		s.fetches = append(s.fetches, fetchCall{baseURL, bearer})
		env, err := s.env, s.fetchErr
		s.mu.Unlock()
		if client != s.m.client {
			t.Errorf("fetch used client %p, want the module's shared client %p", client, s.m.client)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > remoteFetchTimeout {
			t.Errorf("fetch ctx deadline = %v/%v, want within %v", deadline, ok, remoteFetchTimeout)
		}
		return env, err
	}
	s.m.post = func(ctx context.Context, client *http.Client, baseURL, bearer string, req ipeers.DeliverRequest) (ipeers.DeliverResponse, *ipeers.RemoteError, error) {
		s.mu.Lock()
		s.posts = append(s.posts, postCall{baseURL, bearer, req})
		resp, rem, err := s.postResp, s.postRem, s.postErr
		s.mu.Unlock()
		if client != s.m.deliverClient {
			t.Errorf("post used client %p, want the module's deliverClient %p", client, s.m.deliverClient)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > ipeers.InterDaemonTimeout {
			t.Errorf("post ctx deadline = %v/%v, want within %v", deadline, ok, ipeers.InterDaemonTimeout)
		}
		// The only bearer that ever leaves this daemon is the entry's
		// outbound token, and only to the entry's URL.
		if bearer != remoteToken {
			t.Errorf("post bearer = %q, want exactly the entry's token %q", bearer, remoteToken)
		}
		if baseURL != remoteEntryURL {
			t.Errorf("post baseURL = %q, want the entry's URL %q", baseURL, remoteEntryURL)
		}
		if resp.MsgID == "" {
			resp.MsgID = req.MsgID
		}
		return resp, rem, err
	}
	return s
}

func (s *sendEnv) sendReq() ipeers.SendRequest {
	return ipeers.SendRequest{To: remoteAlias + "/" + remoteSession, Text: "hello from mlab", OriginInbox: s.targetSock}
}

func adminCtx() context.Context {
	return middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
}

// send serves one POST /api/peers/send through the mux with body
// (marshalled unless it is already a []byte) under ctx.
func (s *sendEnv) send(ctx context.Context, body any) *httptest.ResponseRecorder {
	s.t.Helper()
	var raw []byte
	switch b := body.(type) {
	case []byte:
		raw = b
	default:
		var err error
		if raw, err = json.Marshal(body); err != nil {
			s.t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	s.m.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/peers/send", bytes.NewReader(raw)).WithContext(ctx)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func (s *sendEnv) sendOK(req ipeers.SendRequest) ipeers.SendResponse {
	s.t.Helper()
	rr := s.send(adminCtx(), req)
	if rr.Code != http.StatusOK {
		s.t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp ipeers.SendResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		s.t.Fatalf("decode response: %v; body=%s", err, rr.Body.String())
	}
	return resp
}

func (s *sendEnv) fetchCalls() []fetchCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]fetchCall(nil), s.fetches...)
}

func (s *sendEnv) postCalls() []postCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]postCall(nil), s.posts...)
}

// onlyPost asserts exactly one post was made and returns it.
func (s *sendEnv) onlyPost() postCall {
	s.t.Helper()
	posts := s.postCalls()
	if len(posts) != 1 {
		s.t.Fatalf("post calls = %d, want 1: %+v", len(posts), posts)
	}
	return posts[0]
}

func (s *sendEnv) set(fn func(s *sendEnv)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

// ---------------------------------------------------------------------------
// Happy path and the wire contract
// ---------------------------------------------------------------------------

func TestSend_HappyPath(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	s.set(func(s *sendEnv) {
		s.postResp = ipeers.DeliverResponse{Result: ipeers.ResultDelivered, EffectiveMode: ipeers.ModePrompting, OneWay: false}
	})
	req := s.sendReq()
	req.Mode = ipeers.ModeBypass

	resp := s.sendOK(req)

	// The remote snapshot was taken with the entry's token, at its URL.
	fetches := s.fetchCalls()
	if len(fetches) != 1 || fetches[0].baseURL != remoteEntryURL || fetches[0].bearer != remoteToken {
		t.Errorf("fetches = %+v, want one at %s with the entry's token", fetches, remoteEntryURL)
	}

	post := s.onlyPost()
	wantFrom := ipeers.WireFrom{
		HostID:         localHostID,
		AgentSessionID: targetSessionID,
		PID:            targetPID,
		ProcStart:      targetProcStart,
		PeerName:       targetPeerName,
		SessionName:    "cc:" + targetPeerName, // outside tmux ⇒ cc:<peer_name>
		DeclaredMode:   ipeers.ModeBypass,
	}
	if post.req.From != wantFrom {
		t.Errorf("post from = %+v, want the origin row %+v", post.req.From, wantFrom)
	}
	wantTo := ipeers.WireTo{AgentSessionID: remoteSessionID, PID: remotePID, ProcStart: remoteProcStart}
	if post.req.To != wantTo {
		t.Errorf("post to = %+v, want the resolved tuple %+v", post.req.To, wantTo)
	}
	if post.req.Text != req.Text || post.req.HopChain != "" {
		t.Errorf("post text/hop_chain = %q/%q, want %q/\"\"", post.req.Text, post.req.HopChain, req.Text)
	}
	if !ipeers.IsUUID(post.req.MsgID) {
		t.Errorf("post msg_id = %q, want a UUID", post.req.MsgID)
	}

	want := ipeers.SendResponse{
		MsgID:         post.req.MsgID,
		ToHostID:      remoteHostID,
		ToAddress:     remoteAlias + "/" + remoteSession,
		To:            wantTo,
		Result:        ipeers.ResultDelivered,
		EffectiveMode: ipeers.ModePrompting, // the receiver clamped bypass
		OneWay:        false,
	}
	if resp != want {
		t.Errorf("response = %+v, want %+v", resp, want)
	}

	row := s.onlyRow()
	if row.MsgID != post.req.MsgID || row.Direction != store.DirOut ||
		row.FromHostID != localHostID || row.FromSessionID != targetSessionID ||
		row.ToHostID != remoteHostID || row.ToSessionID != remoteSessionID ||
		row.DeclaredMode != ipeers.ModeBypass || row.EffectiveMode != ipeers.ModePrompting ||
		row.Bytes != len(req.Text) || row.Result != ipeers.ResultDelivered || row.Error != "" {
		t.Errorf("audit row = %+v, want out/%s/%s→%s/%s/%s bypass→prompting %d bytes delivered", row, localHostID, targetSessionID, remoteHostID, remoteSessionID, post.req.MsgID, len(req.Text))
	}
	if row.TS.IsZero() {
		t.Errorf("audit row ts is zero")
	}
	s.assertNoLine() // nothing is written into a local inbox by a send
}

func TestSend_OneWayRecordedAsNoReturnRoute(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	s.set(func(s *sendEnv) {
		s.postResp = ipeers.DeliverResponse{Result: ipeers.ResultDeliveryUncertain, EffectiveMode: ipeers.ModePrompting, OneWay: true}
	})
	resp := s.sendOK(s.sendReq())
	if !resp.OneWay || resp.Result != ipeers.ResultDeliveryUncertain {
		t.Errorf("response = %+v, want one_way with delivery_uncertain echoed", resp)
	}
	row := s.onlyRow()
	if row.Result != ipeers.ResultDeliveryUncertain || row.Error != ipeers.ErrNoReturnRoute || row.EffectiveMode != ipeers.ModePrompting {
		t.Errorf("row = %+v, want delivery_uncertain / no_return_route / prompting", row)
	}
}

// TestSend_TmuxOriginUsesSessionName: an origin inside tmux is named by
// its tmux session, not cc:<peer_name>.
func TestSend_TmuxOriginUsesSessionName(t *testing.T) {
	s := newSendEnv(t, envOpts{
		sessions: []session.SessionInfo{{Code: "origc", Name: "orig-tmux", TmuxInstance: "inst1"}},
		owners:   map[string]agent.PaneOwner{"origc": {AgentType: "cc", SessionID: targetSessionID, TmuxPaneID: "%10"}},
	})
	s.sendOK(s.sendReq())
	post := s.onlyPost()
	if post.req.From.SessionName != "orig-tmux" || post.req.From.PeerName != targetPeerName {
		t.Errorf("from session_name/peer_name = %q/%q, want orig-tmux/%s", post.req.From.SessionName, post.req.From.PeerName, targetPeerName)
	}
}

// TestSend_OriginResolvesViaEntryRowDespitePartialInventory pins the v2
// delta over spec §4.2's partial semantics on the origin lookup (R2-A):
// with Peer Address v2's entry rows (spec §3.4), the origin's tmux
// session's owner lookup failing no longer means the inventory has no row
// for its inbox — the origin's own live registry entry still gets an entry
// row and is found through it, so the send proceeds to the remote fetch
// exactly as the (origin outside tmux) happy path does, rather than
// answering 503 not_ready before any fetch.
func TestSend_OriginResolvesViaEntryRowDespitePartialInventory(t *testing.T) {
	s := newSendEnv(t, envOpts{noRegistry: true, sessions: []session.SessionInfo{{Code: "s1", Name: "foo"}}})
	writeRegistryFixture(s.t, s.regDir, strconv.Itoa(targetPID)+".json", targetRegistryJSONInTmux(s.targetSock, "foo:@1.%1"))
	s.m.owners = &fakeOwners{errs: map[string]error{"s1": errors.New("resolver timeout")}}

	resp := s.sendOK(s.sendReq())
	if resp.Result != ipeers.ResultDelivered {
		t.Errorf("result = %q, want delivered", resp.Result)
	}
	if fetches := s.fetchCalls(); len(fetches) != 1 {
		t.Errorf("fetches = %+v, want exactly 1 (the origin resolved, unblocking the remote fetch)", fetches)
	}
	post := s.onlyPost()
	// The origin resolved through its ENTRY row (RowKind "entry"), which
	// never carries a SessionName — so wireFromRecord falls back to
	// "cc:<peer_name>" exactly as for any other outside-tmux origin, even
	// though the entry's own tmux field names a listed session.
	if post.req.From.AgentSessionID != targetSessionID || post.req.From.SessionName != "cc:"+targetPeerName {
		t.Errorf("post from = %+v, want the origin's entry row (session %q, name cc:%s)", post.req.From, targetSessionID, targetPeerName)
	}
}

// TestSend_EachSendMintsItsOwnMsgID: a retry must never reuse a msg_id
// (the receiver's dedup would burn it).
func TestSend_EachSendMintsItsOwnMsgID(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	a := s.sendOK(s.sendReq())
	b := s.sendOK(s.sendReq())
	if a.MsgID == b.MsgID {
		t.Errorf("two sends shared msg_id %q", a.MsgID)
	}
	posts := s.postCalls()
	if len(posts) != 2 || posts[0].req.MsgID == posts[1].req.MsgID {
		t.Errorf("posts = %+v, want two with distinct msg_ids", posts)
	}
}

// TestSend_RemoteRowsClaimingAnotherHostStillTargetTheEntry pins P2 #5:
// whatever host/host_id/address the remote's rows claim, the request is
// sent to the config entry (its URL, its token) and to_address carries the
// entry's alias.
func TestSend_RemoteRowsClaimingAnotherHostStillTargetTheEntry(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	row := remoteRow(remoteSession, "fooc")
	row.Host, row.HostID, row.Address = "evil", "evil:000", "evil/"+remoteSession
	s.set(func(s *sendEnv) { s.env = remoteEnvelope(row) })

	resp := s.sendOK(s.sendReq())
	post := s.onlyPost() // asserts baseURL/bearer are the entry's
	if resp.ToHostID != remoteHostID || resp.ToAddress != remoteAlias+"/"+remoteSession {
		t.Errorf("to_host_id/to_address = %q/%q, want %s/%s", resp.ToHostID, resp.ToAddress, remoteHostID, remoteAlias+"/"+remoteSession)
	}
	if resp.To.AgentSessionID != remoteSessionID || post.req.To.PID != remotePID {
		t.Errorf("to = %+v / post to = %+v, want the row's tuple", resp.To, post.req.To)
	}
	if row := s.onlyRow(); row.ToHostID != remoteHostID {
		t.Errorf("audit to_host_id = %q, want the entry's %q", row.ToHostID, remoteHostID)
	}
	if strings.Contains(post.bearer, "evil") || strings.Contains(post.baseURL, "evil") {
		t.Errorf("post reached %s with %q", post.baseURL, post.bearer)
	}
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

func TestSend_LocalTarget(t *testing.T) {
	for _, host := range []string{localAlias, localHostID, "MLAB"} {
		t.Run(host, func(t *testing.T) {
			s := newSendEnv(t, envOpts{})
			req := s.sendReq()
			req.To = host + "/" + remoteSession
			assertRefused(t, s.send(adminCtx(), req), http.StatusBadRequest, ipeers.ErrLocalTarget)
			if len(s.fetchCalls()) != 0 || len(s.postCalls()) != 0 || len(s.rows()) != 0 {
				t.Errorf("fetch/post/rows = %d/%d/%d, want none", len(s.fetchCalls()), len(s.postCalls()), len(s.rows()))
			}
		})
	}
}

func TestSend_HostMatchedByHostID(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	req := s.sendReq()
	req.To = remoteHostID + "/" + remoteSession
	resp := s.sendOK(req)
	if resp.ToAddress != remoteAlias+"/"+remoteSession {
		t.Errorf("to_address = %q, want the entry's alias form", resp.ToAddress)
	}
}

func TestSend_OriginProxyRows(t *testing.T) {
	t.Run("own helper", func(t *testing.T) {
		s := newSendEnv(t, envOpts{})
		// An inbound delivery spawns this daemon's own helper for "air";
		// its registry entry is a proxy row and never an origin.
		s.deliverOK(s.request())
		helperSock := helperSockOf(t, s.recvLine())
		req := s.sendReq()
		req.OriginInbox = helperSock
		assertRefused(t, s.send(adminCtx(), req), http.StatusBadRequest, ipeers.ErrOriginUnknown)
		if len(s.fetchCalls()) != 0 || len(s.postCalls()) != 0 {
			t.Errorf("fetch/post = %d/%d, want none", len(s.fetchCalls()), len(s.postCalls()))
		}
		if rows := s.rows(); len(rows) != 1 || rows[0].Direction != store.DirIn {
			t.Errorf("rows = %+v, want only the inbound delivery's row", rows)
		}
	})
	t.Run("foreign helper via IsProxy", func(t *testing.T) {
		s := newSendEnv(t, envOpts{proxyInfo: map[int]bool{555: true}})
		foreignSock := filepath.Join(s.root, "foreign.sock")
		ln, err := net.Listen("unix", foreignSock)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		writeRegistryFixture(t, s.regDir, "555.json", `{"pid":555,"sessionId":"55555555-5555-4555-8555-555555555555","cwd":"/w","procStart":"`+targetProcStart+`","messagingSocketPath":"`+foreignSock+`","name":"air/foo"}`)
		req := s.sendReq()
		req.OriginInbox = foreignSock
		assertRefused(t, s.send(adminCtx(), req), http.StatusBadRequest, ipeers.ErrOriginUnknown)
		if len(s.fetchCalls()) != 0 || len(s.postCalls()) != 0 || len(s.rows()) != 0 {
			t.Errorf("fetch/post/rows = %d/%d/%d, want none", len(s.fetchCalls()), len(s.postCalls()), len(s.rows()))
		}
	})
}

func TestSend_Ambiguous(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	a := remoteRow("", "")
	b := remoteRow("", "")
	b.Agent.PID, b.Agent.SessionID = 778, "99999999-8888-4777-8666-666666666666"
	// The remote claims another alias in its addresses: candidates must
	// come back normalised to the entry's alias.
	a.Address, b.Address = "zzz/cc:"+remoteSession, "zzz/cc:"+remoteSession
	s.set(func(s *sendEnv) { s.env = remoteEnvelope(a, b) })
	req := s.sendReq()
	req.To = remoteAlias + "/cc:" + remoteSession

	ae := assertRefused(t, s.send(adminCtx(), req), http.StatusConflict, ipeers.ErrAmbiguous)
	want := []string{remoteAlias + "/cc:" + remoteSession, remoteAlias + "/cc:" + remoteSession}
	if len(ae.Candidates) != 2 || ae.Candidates[0] != want[0] || ae.Candidates[1] != want[1] {
		t.Errorf("candidates = %v, want %v", ae.Candidates, want)
	}
	if len(s.postCalls()) != 0 || len(s.rows()) != 0 {
		t.Errorf("post/rows = %d/%d, want none", len(s.postCalls()), len(s.rows()))
	}
}

func TestSend_RemoteTargetGone(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	s.set(func(s *sendEnv) {
		s.postRem = &ipeers.RemoteError{Status: http.StatusConflict, Error: ipeers.ErrTargetGone, Detail: "pid does not match the live session"}
	})
	ae := assertRefused(t, s.send(adminCtx(), s.sendReq()), http.StatusBadGateway, ipeers.ErrRemoteError)
	if ae.Remote == nil || ae.Remote.Status != http.StatusConflict || ae.Remote.Error != ipeers.ErrTargetGone || ae.Remote.Detail != "pid does not match the live session" {
		t.Errorf("remote = %+v, want the receiver's {409 target_gone …}", ae.Remote)
	}
	row := s.onlyRow()
	if row.Result != ipeers.ErrTargetGone || row.Error != "pid does not match the live session" || row.EffectiveMode != "" {
		t.Errorf("row = %+v, want result target_gone with the remote detail and no effective mode", row)
	}
}

func TestSend_PostTransportError(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	s.set(func(s *sendEnv) { s.postErr = errors.New("dial tcp: connection refused") })
	ae := assertRefused(t, s.send(adminCtx(), s.sendReq()), http.StatusBadGateway, ipeers.ErrRemoteError)
	if ae.Remote == nil || ae.Remote.Status != 0 || !strings.Contains(ae.Remote.Error, "connection refused") {
		t.Errorf("remote = %+v, want status 0 with the transport error", ae.Remote)
	}
	row := s.onlyRow()
	if row.Result != "" || !strings.Contains(row.Error, "connection refused") {
		t.Errorf("row = %+v, want result \"\" with the transport error text", row)
	}
}

func TestSend_AfterStopNotReady(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	s.m.stopCancel()
	assertRefused(t, s.send(adminCtx(), s.sendReq()), http.StatusServiceUnavailable, ipeers.ErrNotReady)
	if len(s.fetchCalls()) != 0 || len(s.postCalls()) != 0 || len(s.rows()) != 0 {
		t.Errorf("fetch/post/rows = %d/%d/%d, want none", len(s.fetchCalls()), len(s.postCalls()), len(s.rows()))
	}
}

// TestSend_ErrorSteps walks every refusal in binding order: for each, the
// status/code, whether the remote snapshot was fetched, whether a
// delivery was posted, and whether an audit row exists (with its result).
func TestSend_ErrorSteps(t *testing.T) {
	type tc struct {
		name    string
		opts    envOpts
		ctx     context.Context
		body    any // nil ⇒ the default request after mutate
		mutate  func(*ipeers.SendRequest)
		prepare func(s *sendEnv)
		status  int
		code    string
		fetched bool
		posted  bool
		rows    int
		result  string // when rows == 1
		detail  string // substring of detail when non-empty
		remote  string // substring of remote.error when non-empty
	}
	hostCtx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalHost, Alias: remoteAlias, HostID: remoteHostID})
	cases := []tc{
		{name: "host principal", ctx: hostCtx, status: http.StatusForbidden, code: ipeers.ErrForbidden},
		{name: "no principal", ctx: context.Background(), status: http.StatusForbidden, code: ipeers.ErrForbidden},
		{name: "invalid JSON", body: []byte("{"), status: http.StatusBadRequest, code: ipeers.ErrBadRequest},
		{name: "empty text", mutate: func(r *ipeers.SendRequest) { r.Text = "" }, status: http.StatusBadRequest, code: ipeers.ErrBadRequest},
		{name: "text too large", mutate: func(r *ipeers.SendRequest) { r.Text = strings.Repeat("a", ipeers.MaxTextBytes+1) }, status: http.StatusBadRequest, code: ipeers.ErrTextTooLarge},
		{name: "bad mode", mutate: func(r *ipeers.SendRequest) { r.Mode = "yolo" }, status: http.StatusBadRequest, code: ipeers.ErrBadMode},
		{name: "bad address no slash", mutate: func(r *ipeers.SendRequest) { r.To = "foo" }, status: http.StatusBadRequest, code: ipeers.ErrBadAddress},
		{name: "bad address empty session", mutate: func(r *ipeers.SendRequest) { r.To = "air/" }, status: http.StatusBadRequest, code: ipeers.ErrBadAddress},
		{name: "bad address nested slash", mutate: func(r *ipeers.SendRequest) { r.To = "air/a/b" }, status: http.StatusBadRequest, code: ipeers.ErrBadAddress},
		{name: "origin_inbox empty", mutate: func(r *ipeers.SendRequest) { r.OriginInbox = "" }, status: http.StatusBadRequest, code: ipeers.ErrOriginUnknown},
		{name: "host unknown", mutate: func(r *ipeers.SendRequest) { r.To = "nope/foo" }, status: http.StatusNotFound, code: ipeers.ErrHostUnknown},
		{
			name:   "host unverified: no token",
			opts:   envOpts{hosts: []config.PeerHost{airHost("", false)}},
			status: http.StatusConflict, code: ipeers.ErrHostUnverified, detail: "set-token",
		},
		{
			name:   "host unverified: no host_id",
			opts:   envOpts{hosts: []config.PeerHost{{Alias: remoteAlias, URL: remoteEntryURL, Token: remoteToken}}},
			status: http.StatusConflict, code: ipeers.ErrHostUnverified,
		},
		{name: "origin unknown path", mutate: func(r *ipeers.SendRequest) { r.OriginInbox = "/nonexistent/x.sock" }, status: http.StatusBadRequest, code: ipeers.ErrOriginUnknown},
		{
			name: "origin not deliverable (registry entry missing)", opts: envOpts{noRegistry: true},
			status: http.StatusBadRequest, code: ipeers.ErrOriginUnknown,
		},
		{
			name:    "fetch error",
			prepare: func(s *sendEnv) { s.fetchErr = errors.New("HTTP 401") },
			status:  http.StatusBadGateway, code: ipeers.ErrRemoteError, fetched: true, remote: "HTTP 401",
		},
		{
			name:    "host_id mismatch",
			prepare: func(s *sendEnv) { s.env.HostID = "other:zzz" },
			status:  http.StatusBadGateway, code: ipeers.ErrRemoteError, fetched: true, remote: "host_id mismatch",
		},
		{
			name: "remote inventory not ok",
			prepare: func(s *sendEnv) {
				s.env = ipeers.Envelope{HostID: remoteHostID, OK: false, Error: "tmux down", Peers: []ipeers.PeerRecord{}}
			},
			status: http.StatusBadGateway, code: ipeers.ErrRemoteError, fetched: true, remote: "tmux down",
		},
		{
			name:   "peer not found",
			mutate: func(r *ipeers.SendRequest) { r.To = "air/nothere" },
			status: http.StatusNotFound, code: ipeers.ErrPeerNotFound, fetched: true,
		},
		{
			name: "not deliverable",
			prepare: func(s *sendEnv) {
				row := remoteRow(remoteSession, "fooc")
				row.Deliverable, row.Reason = false, "inbox_dead"
				s.env = remoteEnvelope(row)
			},
			status: http.StatusConflict, code: ipeers.ErrNotDeliverable, fetched: true, detail: "inbox_dead",
		},
		{
			name: "outbound request invalid: remote proc_start unparsable",
			prepare: func(s *sendEnv) {
				row := remoteRow(remoteSession, "fooc")
				row.Agent.ProcStart = "not a date"
				s.env = remoteEnvelope(row)
			},
			status: http.StatusBadRequest, code: ipeers.ErrBadRequest, fetched: true, detail: "to.proc_start",
		},
		{
			name: "outbound request invalid: origin session name over the label limit",
			opts: envOpts{
				sessions: []session.SessionInfo{{Code: "origc", Name: strings.Repeat("n", ipeers.MaxLabelBytes+44), TmuxInstance: "inst1"}},
				owners:   map[string]agent.PaneOwner{"origc": {AgentType: "cc", SessionID: targetSessionID, TmuxPaneID: "%10"}},
			},
			status: http.StatusBadRequest, code: ipeers.ErrBadRequest, fetched: true, detail: "from.session_name",
		},
		{
			name:    "audit insert failure",
			prepare: func(s *sendEnv) { s.f.audit.fail(errors.New("disk full"), nil) },
			status:  http.StatusServiceUnavailable, code: ipeers.ErrAuditUnavailable, fetched: true,
		},
		{
			name:    "remote refusal",
			prepare: func(s *sendEnv) { s.postRem = &ipeers.RemoteError{Status: 429, Error: ipeers.ErrRateLimited} },
			status:  http.StatusBadGateway, code: ipeers.ErrRemoteError, fetched: true, posted: true, rows: 1, result: ipeers.ErrRateLimited, remote: ipeers.ErrRateLimited,
		},
		{
			name:    "post transport error",
			prepare: func(s *sendEnv) { s.postErr = errors.New("i/o timeout") },
			status:  http.StatusBadGateway, code: ipeers.ErrRemoteError, fetched: true, posted: true, rows: 1, result: "", remote: "i/o timeout",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSendEnv(t, c.opts)
			if c.prepare != nil {
				s.set(c.prepare)
			}
			ctx := c.ctx
			if ctx == nil {
				ctx = adminCtx()
			}
			body := c.body
			if body == nil {
				req := s.sendReq()
				if c.mutate != nil {
					c.mutate(&req)
				}
				body = req
			}
			ae := assertRefused(t, s.send(ctx, body), c.status, c.code)
			if c.detail != "" && !strings.Contains(ae.Detail, c.detail) {
				t.Errorf("detail = %q, want it to contain %q", ae.Detail, c.detail)
			}
			if c.remote != "" && (ae.Remote == nil || !strings.Contains(ae.Remote.Error, c.remote)) {
				t.Errorf("remote = %+v, want error containing %q", ae.Remote, c.remote)
			}
			if got := len(s.fetchCalls()) > 0; got != c.fetched {
				t.Errorf("fetched = %v, want %v", got, c.fetched)
			}
			if got := len(s.postCalls()) > 0; got != c.posted {
				t.Errorf("posted = %v, want %v", got, c.posted)
			}
			rows := s.rows()
			if len(rows) != c.rows {
				t.Fatalf("rows = %d, want %d: %+v", len(rows), c.rows, rows)
			}
			if c.rows == 1 && rows[0].Result != c.result {
				t.Errorf("row result = %q, want %q", rows[0].Result, c.result)
			}
			s.assertNoLine()
		})
	}
}

// TestSend_RemoteTextBounded: every piece of remote text that reaches this
// daemon's own response body or log (a fetch error, a row's reason, a
// validation error quoting a remote field) is bounded first.
func TestSend_RemoteTextBounded(t *testing.T) {
	const limit = maxRemoteTextBytes + len("…")
	long := strings.Repeat("x", 5000)
	cases := []struct {
		name    string
		prepare func(s *sendEnv)
		status  int
		code    string
		text    func(ae ipeers.APIError) string
	}{
		{
			name:    "fetch error",
			prepare: func(s *sendEnv) { s.fetchErr = errors.New(long) },
			status:  http.StatusBadGateway, code: ipeers.ErrRemoteError,
			text: func(ae ipeers.APIError) string { return ae.Remote.Error },
		},
		{
			name: "not_deliverable reason",
			prepare: func(s *sendEnv) {
				row := remoteRow(remoteSession, "fooc")
				row.Deliverable, row.Reason = false, long
				s.env = remoteEnvelope(row)
			},
			status: http.StatusConflict, code: ipeers.ErrNotDeliverable,
			text: func(ae ipeers.APIError) string { return ae.Detail },
		},
		{
			name: "validation error quoting proc_start",
			prepare: func(s *sendEnv) {
				row := remoteRow(remoteSession, "fooc")
				row.Agent.ProcStart = long
				s.env = remoteEnvelope(row)
			},
			status: http.StatusBadRequest, code: ipeers.ErrBadRequest,
			text: func(ae ipeers.APIError) string { return ae.Detail },
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSendEnv(t, envOpts{})
			s.set(c.prepare)
			ae := assertRefused(t, s.send(adminCtx(), s.sendReq()), c.status, c.code)
			if got := c.text(ae); len(got) > limit+len("outbound request invalid: ") {
				t.Errorf("text length = %d, want bounded to ~%d", len(got), limit)
			}
			for _, line := range s.f.logs.all() {
				if len(line) > 2*limit+200 {
					t.Errorf("log line length = %d carries unbounded remote text", len(line))
				}
			}
			if len(s.postCalls()) != 0 || len(s.rows()) != 0 {
				t.Errorf("post/rows = %d/%d, want none", len(s.postCalls()), len(s.rows()))
			}
		})
	}
}

// TestSend_NotReadyBeforeAudit: a daemon that starts stopping after the
// remote resolve answers not_ready without an audit row or a post.
func TestSend_NotReadyBeforeAudit(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	inner := s.m.fetch
	s.m.fetch = func(ctx context.Context, client *http.Client, baseURL, bearer string) (ipeers.Envelope, error) {
		env, err := inner(ctx, client, baseURL, bearer)
		s.m.stopCancel()
		return env, err
	}
	assertRefused(t, s.send(adminCtx(), s.sendReq()), http.StatusServiceUnavailable, ipeers.ErrNotReady)
	if len(s.postCalls()) != 0 || len(s.rows()) != 0 {
		t.Errorf("post/rows = %d/%d, want none", len(s.postCalls()), len(s.rows()))
	}
}
