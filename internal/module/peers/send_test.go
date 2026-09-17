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
	"os"
	"path/filepath"
	"reflect"
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
//
// Ref is set because a v3-or-later daemon sets it on every row with a live cc
// entry, and Resolve reads a live cc row WITHOUT one as proof that the whole
// batch came from a pre-v3 daemon (ErrRemoteTooOld). Leaving it empty here
// would have made every send test in this file run against a v2 peer without
// saying so.
func remoteRow(sessionName, sessionCode string) ipeers.PeerRecord {
	addr := remoteAlias + "/" + sessionName
	if sessionName == "" {
		addr = remoteAlias + "/cc:" + remoteSession
	}
	return ipeers.PeerRecord{
		Host:         remoteAlias,
		HostID:       remoteHostID,
		Address:      addr,
		Ref:          ipeers.RefID(remoteSessionID),
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
		// v4 (§5.6): the origin row's bare ref at its label row's revision
		// (no label row ⇒ rev 0). The ref is the conversation's own,
		// whether or not it has a label.
		Address:      ipeers.RefID(targetSessionID),
		AddressRev:   0,
		DeclaredMode: ipeers.ModeBypass,
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

// ---------------------------------------------------------------------------
// Local delivery (spec §4.1): the scaffolding
// ---------------------------------------------------------------------------

const (
	localPeerSessionID = "c1c2c3c4-d5d6-4d78-8a9b-0c1d2e3f4a5b"
	localPeerPID       = 76974
	localPeerName      = "purdex-b0"
)

// localPeer is a SECOND live Claude Code session on this host: a real Unix
// listener plus its own registry entry, which is all localEnvelope needs in
// order to build a deliverable row for it. The deliverEnv's own "target"
// session is the ORIGIN of every send test, so a local send needs somewhere
// else to go.
type localPeer struct {
	t     *testing.T
	sock  string
	lines chan string
}

// addLocalPeer registers one and returns it. Its procStart is the target's
// because deliverLiveness reports fixture76973ProcStart for every pid below
// 900000 — any other string would make the entry look dead and the row would
// never reach the inventory at all.
func (s *sendEnv) addLocalPeer(name, sessionID string, pid int) *localPeer {
	s.t.Helper()
	p := &localPeer{
		t:     s.t,
		sock:  filepath.Join(s.root, "peer"+strconv.Itoa(pid)+".sock"),
		lines: make(chan string, 8),
	}
	ln, err := net.Listen("unix", p.sock)
	if err != nil {
		s.t.Fatalf("listen %s: %v", p.sock, err)
	}
	s.t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				data, _ := io.ReadAll(conn)
				conn.Close()
				for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
					if len(line) > 0 {
						p.lines <- string(line)
					}
				}
			}()
		}
	}()
	s.registerLocalPeer(name, sessionID, pid, p.sock)
	return p
}

// addLocalPeerWithDeadInbox registers a second local session whose
// messagingSocketPath is a REGULAR FILE rather than a listener — the same
// trick deliverEnv's listenAbsent uses (deliver_test.go). The registry row
// is otherwise indistinguishable from a live one, so resolution succeeds
// and the send gets all the way to the inbox write, which is the only way
// to reach the socket-write failure arm of send.go's local branch.
func (s *sendEnv) addLocalPeerWithDeadInbox(name, sessionID string, pid int) string {
	s.t.Helper()
	sock := filepath.Join(s.root, "peer"+strconv.Itoa(pid)+".sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		s.t.Fatal(err)
	}
	s.registerLocalPeer(name, sessionID, pid, sock)
	return sock
}

// registerLocalPeer writes the registry entry both constructors share.
func (s *sendEnv) registerLocalPeer(name, sessionID string, pid int, sock string) {
	s.t.Helper()
	writeRegistryFixture(s.t, s.regDir, strconv.Itoa(pid)+".json",
		`{"pid":`+strconv.Itoa(pid)+`,"sessionId":"`+sessionID+`","cwd":"/w2","procStart":"`+targetProcStart+
			`","version":"2.1.270","messagingSocketPath":"`+sock+`","name":"`+name+`","status":"idle"}`)
}

// recvLine waits (bounded) for the next line on the local peer's inbox.
func (p *localPeer) recvLine() string {
	p.t.Helper()
	select {
	case l := <-p.lines:
		return l
	case <-time.After(5 * time.Second):
		p.t.Fatal("local peer inbox received no line within 5 s")
		return ""
	}
}

// assertNoLine asserts nothing else reached the local peer's inbox.
func (p *localPeer) assertNoLine() {
	p.t.Helper()
	select {
	case l := <-p.lines:
		p.t.Fatalf("local peer inbox received an unexpected line: %s", l)
	case <-time.After(100 * time.Millisecond):
	}
}

// localSendReq addresses the extra local peer by name, from the origin.
func (s *sendEnv) localSendReq() ipeers.SendRequest {
	return ipeers.SendRequest{To: localAlias + "/" + localPeerName, Text: "hello from next door", OriginInbox: s.targetSock}
}

// TestSend_LocalTarget pins that all three host-segment forms HostMatches
// accepts — the local alias, the local host id, and a case-mismatched alias —
// route to the LOCAL branch and deliver (spec L1).
//
// Until Peer Local Delivery this same table asserted the opposite: all three
// were refused local_target. It is kept rather than replaced because it is the
// only coverage that every HostMatches form is treated alike. The fetch/post
// assertions are the originals, and now say something stronger than "nothing
// left this host before the refusal": the delivery took no HTTP hop at all.
func TestSend_LocalTarget(t *testing.T) {
	for _, host := range []string{localAlias, localHostID, "MLAB"} {
		t.Run(host, func(t *testing.T) {
			s := newSendEnv(t, envOpts{})
			p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
			req := s.localSendReq()
			req.To = host + "/" + localPeerName

			resp := s.sendOK(req)
			if resp.Result != ipeers.ResultDelivered {
				t.Errorf("result = %q, want %q", resp.Result, ipeers.ResultDelivered)
			}
			p.recvLine()
			p.assertNoLine()
			if len(s.fetchCalls()) != 0 || len(s.postCalls()) != 0 {
				t.Errorf("fetch/post = %d/%d, want none", len(s.fetchCalls()), len(s.postCalls()))
			}
		})
	}
}

// TestSend_LocalDeliveryCarriesBothFroms asserts BOTH from-fields of a
// locally delivered frame, because they are different fields with different
// readers (spec §4.1): BuildFrame's socket argument becomes the NDJSON
// frame's top-level `from`, which is the address Claude Code replies to,
// while Wrapper.From is an attribute inside the rendered content, which is
// what the receiving agent reads. A test on one would not catch the other
// being wrong.
func TestSend_LocalDeliveryCarriesBothFroms(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	req := s.localSendReq()

	s.sendOK(req)

	w, sock := wrapperOf(t, p.recvLine())
	p.assertNoLine()
	if sock != s.targetSock {
		t.Errorf("frame from = uds:%s, want the sender's own inbox uds:%s (no helper stands in locally)", sock, s.targetSock)
	}
	if w.From != "uds:"+s.targetSock {
		t.Errorf("wrapper from = %q, want %q", w.From, "uds:"+s.targetSock)
	}
	if w.FromName != localAlias+"/"+targetPeerName {
		t.Errorf("wrapper from-name = %q, want the origin's own address %q", w.FromName, localAlias+"/"+targetPeerName)
	}
	if w.FromMode != ipeers.ModePrompting {
		t.Errorf("wrapper from-mode = %q, want %q", w.FromMode, ipeers.ModePrompting)
	}
	if w.HopChain != "" {
		t.Errorf("wrapper hop-chain = %q, want empty (a CLI send is the first hop)", w.HopChain)
	}
	if w.Text != req.Text {
		t.Errorf("wrapper text = %q, want %q", w.Text, req.Text)
	}
	s.assertNoLine() // the sender's own inbox is untouched
}

// TestSend_LocalDeliveryUsesNoHelperAndNoDeliverCall pins spec §4.2: local
// delivery is resolve → write. The assertions are on the seams themselves —
// the spawn count and the post fake's call log — not on the result looking
// right, which it would whether or not a helper had been started.
func TestSend_LocalDeliveryUsesNoHelperAndNoDeliverCall(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)

	s.sendOK(s.localSendReq())
	p.recvLine()

	if n := s.f.fake.Spawns(); n != 0 {
		t.Errorf("helper spawns = %d, want 0 (a local sender already has a socket next door)", n)
	}
	if posts := s.postCalls(); len(posts) != 0 {
		t.Errorf("postDeliver calls = %+v, want none", posts)
	}
	if fetches := s.fetchCalls(); len(fetches) != 0 {
		t.Errorf("inventory fetches = %+v, want none (step 4's envelope is the inventory)", fetches)
	}
}

// TestSend_LocalDeliverySendResponse pins the answer a local send gives its
// caller: this host's own host id, the target row's address, delivered, and
// never one_way — there is no helper whose absence could make it so.
func TestSend_LocalDeliverySendResponse(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)

	resp := s.sendOK(s.localSendReq())
	p.recvLine()

	if !ipeers.IsUUID(resp.MsgID) {
		t.Errorf("msg_id = %q, want a UUID", resp.MsgID)
	}
	want := ipeers.SendResponse{
		MsgID:         resp.MsgID,
		ToHostID:      localHostID,
		ToAddress:     localAlias + "/" + localPeerName,
		To:            ipeers.WireTo{AgentSessionID: localPeerSessionID, PID: localPeerPID, ProcStart: targetProcStart},
		Result:        ipeers.ResultDelivered,
		EffectiveMode: ipeers.ModePrompting,
		OneWay:        false,
	}
	if resp != want {
		t.Errorf("response = %+v, want %+v", resp, want)
	}
}

// TestSend_LocalPartialInventoryNotReady pins that the local branch resolves
// under the SAME snapshot rules as a remote one (spec §4.1): a partial local
// inventory refuses 503 not_ready rather than falling back to the bare tmux
// tier that would otherwise have matched. Being this daemon's own inventory
// is not a licence to guess.
func TestSend_LocalPartialInventoryNotReady(t *testing.T) {
	s := newSendEnv(t, envOpts{sessions: []session.SessionInfo{{Code: "s1", Name: "ghost"}}})
	// An owner lookup that FAILS (not one that finds no owner) marks the
	// envelope partial, which is the local equivalent of a remote
	// envelope's Partial flag.
	s.m.owners = &fakeOwners{errs: map[string]error{"s1": errors.New("resolver timeout")}}
	req := s.localSendReq()
	req.To = localAlias + "/ghost" // would match the tmux fallback if it were reached

	rr := s.send(adminCtx(), req)

	ae := assertRefused(t, rr, http.StatusServiceUnavailable, ipeers.ErrNotReady)
	if !ae.Partial {
		t.Errorf("partial = %v, want true", ae.Partial)
	}
	if !strings.Contains(ae.Detail, localAlias) {
		t.Errorf("detail = %q, want it to name this host %q", ae.Detail, localAlias)
	}
	if len(s.rows()) != 0 {
		t.Errorf("audit rows = %d, want none (a resolution failure is unaudited)", len(s.rows()))
	}
}

// ---------------------------------------------------------------------------
// Policy on the local path (spec §4.3)
// ---------------------------------------------------------------------------

const (
	twinPeerSessionID = "e1e2e3e4-f5f6-4f78-8a9b-0c1d2e3f4a5c"
	twinPeerPID       = 76975
)

// floodPairLimit drives PairRateLimit successful local sends from the origin
// to p, draining p's inbox each time, and returns the request the NEXT send
// should use — the one the limit must refuse.
func (s *sendEnv) floodPairLimit(p *localPeer) ipeers.SendRequest {
	s.t.Helper()
	req := s.localSendReq()
	for i := 0; i < ipeers.PairRateLimit; i++ {
		s.sendOK(req)
		p.recvLine()
	}
	return req
}

// TestSend_LocalPairRateLimited pins the one /deliver policy that §4.3 keeps
// on the local path. It is not about trust between daemons — that is what the
// host limit is for, and §4.3 drops that one here — but about protecting the
// RECEIVING session from a flood, which is as wanted from next door as from
// another host.
func TestSend_LocalPairRateLimited(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	req := s.floodPairLimit(p)

	assertRefused(t, s.send(adminCtx(), req), http.StatusTooManyRequests, ipeers.ErrRateLimited)
	p.assertNoLine() // the 31st frame did not reach the target
}

// TestSend_LocalRateLimitedRefusalIsAudited is the audited side of §4.3's
// boundary, and the target of mutation M3: the pair-limit check sits AFTER
// the step-7 insert, so a refused send is still an attempt this daemon
// recorded having made. Moving the check above the insert leaves the refusal
// in the log and nowhere else, and this is the test that says so.
func TestSend_LocalRateLimitedRefusalIsAudited(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	req := s.floodPairLimit(p)

	assertRefused(t, s.send(adminCtx(), req), http.StatusTooManyRequests, ipeers.ErrRateLimited)

	rows := s.rows()
	if len(rows) != ipeers.PairRateLimit+1 {
		t.Fatalf("audit rows = %d, want %d (the refusal is recorded too)", len(rows), ipeers.PairRateLimit+1)
	}
	last := rows[len(rows)-1]
	if last.Result != ipeers.ErrRateLimited {
		t.Errorf("last row result = %q, want %q", last.Result, ipeers.ErrRateLimited)
	}
	if last.Direction != store.DirOut || last.ToHostID != localHostID || last.ToSessionID != localPeerSessionID {
		t.Errorf("last row = %+v, want an out row to the local target on %s", last, localHostID)
	}
}

// TestSend_LocalResolutionFailureIsUnaudited is the other side of that
// boundary: steps 1–6 are logged, not audited, on the local path exactly as
// on the remote one. §4.3 puts each refusal where it sits REMOTELY rather
// than where it happens to be written, and a resolution failure sits before
// the insert on both.
func TestSend_LocalResolutionFailureIsUnaudited(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	req := s.localSendReq()
	req.To = localAlias + "/no-such-conversation"

	assertRefused(t, s.send(adminCtx(), req), http.StatusNotFound, ipeers.ErrPeerNotFound)

	if rows := s.rows(); len(rows) != 0 {
		t.Errorf("audit rows = %+v, want none", rows)
	}
	p.assertNoLine()
}

// TestSend_LocalDeliveryWritesExactlyOneAuditRow pins the first row of
// §4.3's table. A remote send leaves two rows because two daemons each
// record what they saw; here one daemon saw one thing, and a DirIn row
// beside the DirOut one would not be corroboration — it would be the same
// observation written twice.
func TestSend_LocalDeliveryWritesExactlyOneAuditRow(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	req := s.localSendReq()

	resp := s.sendOK(req)
	p.recvLine()

	row := s.onlyRow() // "exactly one" is this call, not the field checks below
	if row.Direction != store.DirOut {
		t.Errorf("direction = %v, want %v (the same daemon is both ends)", row.Direction, store.DirOut)
	}
	if row.MsgID != resp.MsgID || row.FromHostID != localHostID || row.FromSessionID != targetSessionID ||
		row.ToHostID != localHostID || row.ToSessionID != localPeerSessionID ||
		row.Result != ipeers.ResultDelivered || row.Bytes != len(req.Text) {
		t.Errorf("audit row = %+v, want %s → %s on %s, delivered, %d bytes", row, targetSessionID, localPeerSessionID, localHostID, len(req.Text))
	}
}

// TestSend_LocalWriteFailureIsAuditedAsSocketWriteFailed pins the RESULT a
// failed local write leaves behind, not the answer the caller gets. §4.3 says
// the local path's results use the same mapping /deliver uses, and /deliver's
// refuse() writes the refusal CODE into result (deliver.go). A local write
// failure that left result empty would render as a blank cell in
// `pdx msg log` (cmd/pdx/msg.go) where the identical remote failure renders
// socket_write_failed — the same event, told two different ways, on the one
// column an operator reads to find out what happened.
//
// The error text stays in the error column: result says WHAT the outcome was,
// error says why.
func TestSend_LocalWriteFailureIsAuditedAsSocketWriteFailed(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	s.addLocalPeerWithDeadInbox(localPeerName, localPeerSessionID, localPeerPID)
	req := s.localSendReq()

	assertRefused(t, s.send(adminCtx(), req), http.StatusBadGateway, ipeers.ErrSocketWriteFailed)

	row := s.onlyRow()
	if row.Result != ipeers.ErrSocketWriteFailed {
		t.Errorf("audit result = %q, want %q", row.Result, ipeers.ErrSocketWriteFailed)
	}
	if row.Error == "" {
		t.Errorf("audit error = %q, want the write's error text preserved", row.Error)
	}
}

// TestSend_LocalModeIsTakenAsDeclared pins §4.3's mode row, and says why it
// is not "remote, unchanged": AllowBypass is a PEER-HOST trust flag, and a
// local target has no host entry to carry one. The authorisation is the
// admin route plus live origin attribution instead — so the fixture removes
// every host entry, and a send that consulted one would have none to consult.
func TestSend_LocalModeIsTakenAsDeclared(t *testing.T) {
	s := newSendEnv(t, envOpts{hosts: []config.PeerHost{}})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	req := s.localSendReq()
	req.Mode = ipeers.ModeBypass

	resp := s.sendOK(req)

	w, _ := wrapperOf(t, p.recvLine())
	if w.FromMode != ipeers.ModeBypass {
		t.Errorf("wrapper from-mode = %q, want %q", w.FromMode, ipeers.ModeBypass)
	}
	if resp.EffectiveMode != ipeers.ModeBypass {
		t.Errorf("effective_mode = %q, want %q (nothing clamped it)", resp.EffectiveMode, ipeers.ModeBypass)
	}
	if row := s.onlyRow(); row.DeclaredMode != ipeers.ModeBypass || row.EffectiveMode != ipeers.ModeBypass {
		t.Errorf("audit modes = %q/%q, want bypass/bypass", row.DeclaredMode, row.EffectiveMode)
	}
}

// TestSend_LocalInvalidModeStillRefused: taking the declared mode as given
// is not taking any string as given. ValidateMode still runs, and still
// refuses before anything is recorded.
func TestSend_LocalInvalidModeStillRefused(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	req := s.localSendReq()
	req.Mode = "yolo"

	assertRefused(t, s.send(adminCtx(), req), http.StatusBadRequest, ipeers.ErrBadMode)
	p.assertNoLine()
	if rows := s.rows(); len(rows) != 0 {
		t.Errorf("audit rows = %+v, want none (validation is unaudited)", rows)
	}
}

// TestSend_LocalRefusalsNameThisHost is the guard on A1's substitution. On
// the local path `entry` is the zero config.PeerHost, so an arm still
// formatting entry.Alias renders "" where the host should be — a failure
// that reads as a cosmetic blemish and is in fact a refusal that no longer
// says which host it is about.
//
// Scoped to the arms that FORMAT THE TARGET HOST, which is the set A1
// substituted. Validation, origin attribution, the pair limit and a write
// failure are excluded on purpose: none of them names a host, so an alias
// assertion there would pin wording nobody chose.
//
// Two of A1's sites are absent, and their absence is a fact about the local
// path rather than a gap here:
//
//   - remote_too_old needs a live cc row with an empty Ref
//     (hasStaleVersionRows, address.go). Every local row for a live cc entry
//     gets Ref = RefID(sessionID) from applyIdentity (record.go), which is
//     never empty, so this daemon cannot report its own inventory as pre-v4.
//   - the toAddress fallback needs target.Address == "", which only
//     normalizeRemoteRows produces, and the local branch does not call it.
//     It is also not a refusal.
func TestSend_LocalRefusalsNameThisHost(t *testing.T) {
	cases := []struct {
		name   string
		opts   envOpts
		setup  func(s *sendEnv) string // returns the address to send to
		status int
		code   string
		// inDetail says WHERE the arm formats the host. Four arms put it in
		// the operator-facing detail. The ambiguous arm does not: its detail
		// is Resolve's own text about the session part, and targetAlias goes
		// to the log line — so that is where this asserts, rather than
		// pretending one uniform claim covers both.
		inDetail bool
	}{
		{
			name: "ambiguous",
			setup: func(s *sendEnv) string {
				s.addLocalPeer("twins", localPeerSessionID, localPeerPID)
				s.addLocalPeer("twins", twinPeerSessionID, twinPeerPID)
				return localAlias + "/twins"
			},
			status: http.StatusConflict, code: ipeers.ErrAmbiguous,
		},
		{
			// A partial inventory reached through the tmux fallback: a
			// name-tier address would not be not_ready at all, because
			// Resolve consults snap.Partial only below tiers 1–3.
			name: "not ready",
			opts: envOpts{sessions: []session.SessionInfo{{Code: "s1", Name: "ghost"}}},
			setup: func(s *sendEnv) string {
				s.m.owners = &fakeOwners{errs: map[string]error{"s1": errors.New("resolver timeout")}}
				return localAlias + "/ghost"
			},
			status: http.StatusServiceUnavailable, code: ipeers.ErrNotReady, inDetail: true,
		},
		{
			name:   "not found",
			setup:  func(s *sendEnv) string { return localAlias + "/no-such-conversation" },
			status: http.StatusNotFound, code: ipeers.ErrPeerNotFound, inDetail: true,
		},
		{
			// The combined form whose typed name is not the ref's current
			// name: understood, and refused.
			name: "name mismatch",
			setup: func(s *sendEnv) string {
				s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
				return localAlias + "/purdex-zz [" + strings.TrimPrefix(ipeers.RefID(localPeerSessionID), "_") + "]"
			},
			status: http.StatusConflict, code: ipeers.ErrCodeNameMismatch, inDetail: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSendEnv(t, c.opts)
			req := s.localSendReq()
			req.To = c.setup(s)

			ae := assertRefused(t, s.send(adminCtx(), req), c.status, c.code)

			if c.inDetail {
				if !strings.Contains(ae.Detail, localAlias) {
					t.Errorf("detail = %q, want it to name this host %q", ae.Detail, localAlias)
				}
				return
			}
			named := false
			for _, l := range s.f.logs.all() {
				if strings.Contains(l, c.code) && strings.Contains(l, localAlias) {
					named = true
				}
			}
			if !named {
				t.Errorf("no %s log line names this host %q: %v", c.code, localAlias, s.f.logs.all())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Sending to yourself (spec §4.5)
// ---------------------------------------------------------------------------

// TestSend_SelfTargetRefused pins spec §4.5. Local delivery made a case
// reachable that the bridge never had — the origin and the target are one
// session — and it is refused rather than written into the caller's own
// inbox with that same inbox as the reply address.
//
// Both address forms are exercised because the comparison is on the
// identity tuple, not on the string the caller typed: `pdx msg whoami`
// prints a name form and a ref form of the SAME session, so a check on the
// typed address would refuse one and deliver the other, and the one it
// delivered would be just as much a loop.
func TestSend_SelfTargetRefused(t *testing.T) {
	forms := []struct{ name, session string }{
		{"by name", targetPeerName},
		{"by ref", ipeers.RefID(targetSessionID)},
	}
	for _, f := range forms {
		t.Run(f.name, func(t *testing.T) {
			s := newSendEnv(t, envOpts{})
			req := s.localSendReq()
			req.To = localAlias + "/" + f.session

			assertRefused(t, s.send(adminCtx(), req), http.StatusBadRequest, ipeers.ErrSelfTarget)

			s.assertNoLine() // the caller's own inbox is where this would have landed
			if len(s.rows()) != 0 {
				t.Errorf("audit rows = %d, want none: §4.3 puts this refusal with the other step-6 resolution refusals", len(s.rows()))
			}
			if len(s.fetchCalls()) != 0 || len(s.postCalls()) != 0 {
				t.Errorf("fetch/post = %d/%d, want none", len(s.fetchCalls()), len(s.postCalls()))
			}
		})
	}
}

// TestSend_RemoteTargetSharingTheOriginsTupleIsNotSelfTarget pins the SCOPE
// of §4.5 rather than its comparison: L7 refuses "a local send to the origin
// itself", and the step table (spec §3, step 6b) puts the check in the local
// column with a dash opposite it. A remote target is on a different host by
// definition, so the identity tuple — session id, pid, proc start, all of
// them host-local facts — says nothing about whether it is this caller.
//
// Run unconditionally, the check reads that tuple as if it were globally
// unique and refuses a perfectly ordinary remote send 400 self_target. The
// collision is not hypothetical: a cloned machine or a restored home
// directory carries the registry's session ids with it, and beyond that the
// tuple arrives in a peer's self-report, which this daemon does not audit.
//
// The fixture is the one TestSend_RefusalCodesMatchPerAddressForm already
// uses — the peer host reporting this daemon's own inventory as its own —
// aimed at the origin's own row, which is the single step that test does not
// take.
func TestSend_RemoteTargetSharingTheOriginsTupleIsNotSelfTarget(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	mirrored := s.m.localEnvelope(context.Background(), remoteHostID, remoteAlias)
	if !mirrored.OK {
		t.Fatalf("local envelope not ok: %s", mirrored.Error)
	}
	s.set(func(s *sendEnv) { s.env = mirrored })

	req := s.sendReq()
	req.To = remoteAlias + "/" + targetPeerName // the origin's own name, on the OTHER host

	resp := s.sendOK(req)
	if resp.Result != ipeers.ResultDelivered {
		t.Errorf("result = %q, want %q", resp.Result, ipeers.ResultDelivered)
	}
	post := s.onlyPost()
	if post.req.To.AgentSessionID != targetSessionID || post.req.To.PID != targetPID {
		t.Errorf("post to = %+v, want the remote row's tuple (session %q pid %d)", post.req.To, targetSessionID, targetPID)
	}
	// What a self-target refusal exists to prevent, and the proof that taking
	// the remote path did not cause it: the local session whose tuple this is
	// never sees the frame.
	s.assertNoLine()
}

// TestSend_AgentlessLocalTargetIsNotDeliverable guards the ORDER of two
// checks rather than either check on its own. A `tmux:<name>` address can
// resolve to a row with no agent at all, so a self-target comparison placed
// where "after Resolve" most obviously reads — before the deliverable guard
// — dereferences a nil target.Agent and turns this refusal into a panic.
func TestSend_AgentlessLocalTargetIsNotDeliverable(t *testing.T) {
	const agentless = "ghost" // a tmux session with no agent: a row, but not a deliverable one
	s := newSendEnv(t, envOpts{sessions: []session.SessionInfo{{Code: "s1", Name: agentless}}})
	req := s.localSendReq()
	req.To = localAlias + "/tmux:" + agentless

	assertRefused(t, s.send(adminCtx(), req), http.StatusConflict, ipeers.ErrNotDeliverable)
	s.assertNoLine()
	if len(s.rows()) != 0 {
		t.Errorf("audit rows = %d, want none", len(s.rows()))
	}
}

// TestSend_RefusalCodesMatchPerAddressForm is the property L1 actually
// promises (spec §6.1): for every address FORM, a local target and an
// equivalent remote target are refused with the SAME code. It deliberately
// does not enumerate "this form yields that code" — that would encode the
// resolver's current internals into the test instead of the parity.
//
// The peer host reports this daemon's OWN inventory as its own, so both
// branches resolve over rows that are identical by construction rather than
// by a second fixture somebody has to keep in step.
func TestSend_RefusalCodesMatchPerAddressForm(t *testing.T) {
	const agentless = "ghost" // a tmux session with no agent: a row, but not a deliverable one
	forms := []struct{ name, session string }{
		{"tmux prefix", "tmux:" + agentless},
		{"bare tier-4 name", agentless},
		{"name tier", "no-such-conversation"},
		{"ref tier", "_zzzzzz"},
	}
	for _, f := range forms {
		t.Run(f.name, func(t *testing.T) {
			s := newSendEnv(t, envOpts{sessions: []session.SessionInfo{{Code: "s1", Name: agentless}}})
			mirrored := s.m.localEnvelope(context.Background(), remoteHostID, remoteAlias)
			if !mirrored.OK {
				t.Fatalf("local envelope not ok: %s", mirrored.Error)
			}
			s.set(func(s *sendEnv) { s.env = mirrored })

			localCode := sendRefusalCode(t, s, localAlias+"/"+f.session)
			remoteCode := sendRefusalCode(t, s, remoteAlias+"/"+f.session)
			if localCode == "" || remoteCode == "" {
				t.Fatalf("local/remote = %q/%q, want both refused (this form must not deliver)", localCode, remoteCode)
			}
			if localCode != remoteCode {
				t.Errorf("local = %q, remote = %q for session %q; one address form must refuse the same way on either host", localCode, remoteCode, f.session)
			}
		})
	}
}

// sendRefusalCode sends to `to` and returns the wire error code, or "" when
// the send was not refused at all.
func sendRefusalCode(t *testing.T, s *sendEnv, to string) string {
	t.Helper()
	req := s.localSendReq()
	req.To = to
	rr := s.send(adminCtx(), req)
	if rr.Code == http.StatusOK {
		return ""
	}
	return decodeAPIError(t, rr).Error
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

// TestSend_Ambiguous pins the v3 shape of ambiguity: ONE conversation with
// two live processes, so both rows carry the same canonical id and the one
// address they share cannot pick between them. The labels are deliberately
// different — under D3 a label is not what made this ambiguous and could
// not have resolved it either.
func TestSend_Ambiguous(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	canonical := ipeers.RefID(remoteSessionID)
	a := remoteRow("", "")
	b := remoteRow("", "")
	b.Agent.PID = 778
	a.Ref, b.Ref = canonical, canonical
	a.Title, b.Title = "purdex-tester", "purdex-tester-2"
	// What the operator needs in order to tell the two apart, and what the
	// refusal must therefore carry (spec §4.1/§6.4): agent name, pid, cwd.
	// The REF is the thing that cannot do it — they share it, which is what
	// made the send ambiguous.
	a.Agent.PeerName, b.Agent.PeerName = "twin-1", "twin-2"
	a.Cwd, b.Cwd = "/w/one", "/w/two"
	// The remote claims another alias, and the shared ref, in both addresses.
	// Neither survives: normalizeRemoteRows derives each row's address from
	// its own fields under the entry's alias, so the two processes come back
	// separately addressable by name even though the ref they share is not.
	a.Address, b.Address = "zzz/"+canonical, "zzz/"+canonical
	s.set(func(s *sendEnv) { s.env = remoteEnvelope(a, b) })
	req := s.sendReq()
	req.To = remoteAlias + "/" + canonical

	ae := assertRefused(t, s.send(adminCtx(), req), http.StatusConflict, ipeers.ErrAmbiguous)
	// Each candidate carries its ref, and here both carry the SAME one —
	// that is what "one conversation, two processes" means, and it is why
	// this case needs agent name/pid/cwd instead. The ref is still reported:
	// suppressing it when it happens not to discriminate would make its
	// presence a second, undocumented signal.
	want := []ipeers.AmbiguousCandidate{
		{Address: remoteAlias + "/twin-1", Ref: canonical, AgentName: "twin-1", PID: remotePID, Cwd: "/w/one"},
		{Address: remoteAlias + "/twin-2", Ref: canonical, AgentName: "twin-2", PID: 778, Cwd: "/w/two"},
	}
	if !reflect.DeepEqual(ae.Candidates, want) {
		t.Errorf("candidates = %+v, want %+v", ae.Candidates, want)
	}
	if len(s.postCalls()) != 0 || len(s.rows()) != 0 {
		t.Errorf("post/rows = %d/%d, want none", len(s.postCalls()), len(s.rows()))
	}
}

// TestSend_AddressRevIsZeroAfterRelabel pins spec §4.4: TitleRev keeps
// counting label changes, but a v3 address cannot change, so the revision
// a v3 sender reports for its ADDRESS is 0 — permanently, however many
// times the conversation has renamed itself. Sending TitleRev there (the
// old wireFromRecord) announced an address change that never happened,
// and the receiver's stale-rev/helper-rename path believed it.
func TestSend_AddressRevIsZeroAfterRelabel(t *testing.T) {
	s := newSendEnv(t, envOpts{})

	// Claim once, then claim a different label: two revisions of the label
	// row, neither of them a revision of the address.
	if res := s.m.claim(s.targetSock, "purdex-tester"); res.err != nil {
		t.Fatalf("first claim: %+v", res.err)
	}
	res := s.m.claim(s.targetSock, "purdex-tester-2")
	if res.err != nil {
		t.Fatalf("second claim: %+v", res.err)
	}
	if res.rec.TitleRev != 2 || res.rec.Title != "purdex-tester-2" {
		t.Fatalf("after re-claim: label %q rev %d, want purdex-tester-2 rev 2", res.rec.Title, res.rec.TitleRev)
	}

	s.sendOK(s.sendReq())

	from := s.onlyPost().req.From
	if from.Address != ipeers.RefID(targetSessionID) {
		t.Errorf("from.address = %q, want the bare ref (a label never addresses)", from.Address)
	}
	if from.AddressRev != 0 {
		t.Errorf("from.address_rev = %d, want 0: the label moved twice, the address never did", from.AddressRev)
	}
}

// TestSend_LegacyCCAddress pins that the retired v1 "cc:<name>" address
// form is always peer_not_found with the legacy hint, never resolved
// against a label or a tmux session name — and never consults env.Partial.
func TestSend_LegacyCCAddress(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	req := s.sendReq()
	req.To = remoteAlias + "/cc:foo"

	ae := assertRefused(t, s.send(adminCtx(), req), http.StatusNotFound, ipeers.ErrPeerNotFound)
	if !strings.Contains(ae.Detail, "pdx peers --all") {
		t.Errorf("detail = %q, want it to contain the legacy hint", ae.Detail)
	}
	if len(s.postCalls()) != 0 || len(s.rows()) != 0 {
		t.Errorf("post/rows = %d/%d, want none", len(s.postCalls()), len(s.rows()))
	}
}

// TestSend_PeerNotFoundTeachesTheV4AddressForms pins the peer_not_found
// detail (v4 spec §5.5).
//
// This hint has been confidently backwards twice. Before v3 it taught the
// tmux session name as the address, which v3 made false. v3 taught the
// eight-digit canonical id, which v4 made false — no such form is minted
// any more, so a reader following it hunts a string nothing produces. A
// hint that is wrong is worse than none: it sends someone who typed a
// correct address off to find an impossible one.
//
// The assertions below are therefore about coverage, not wording. The
// detail must name all three v4 forms rather than crowning one, must keep
// the clause that the self-declared title never addresses, and must point
// at the two commands that print a live address. The negative assertions
// pin the two retired teachings so neither can return. The wire "error"
// code stays peer_not_found — assertRefused checks that — so nothing
// matching on it breaks.
func TestSend_PeerNotFoundTeachesTheV4AddressForms(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	req := s.sendReq()
	req.To = remoteAlias + "/_ab12cd" // a ref that matches no row

	ae := assertRefused(t, s.send(adminCtx(), req), http.StatusNotFound, ipeers.ErrPeerNotFound)
	for _, want := range []string{
		`"_ab12cd"`, `"` + remoteAlias + `"`,
		"<host>/<name>", "[<ref>]", "<host>/_<ref>",
		"title", "pdx peers --all", "pdx msg whoami",
	} {
		if !strings.Contains(ae.Detail, want) {
			t.Errorf("detail = %q, want it to contain %s", ae.Detail, want)
		}
	}
	for _, gone := range []string{"tmux session name", "canonical"} {
		if strings.Contains(ae.Detail, gone) {
			t.Errorf("detail = %q, still teaches the retired %q form", ae.Detail, gone)
		}
	}
	if len(s.postCalls()) != 0 {
		t.Errorf("posts = %d, want none", len(s.postCalls()))
	}
}

// TestSend_RemoteTooOld pins the mixed-version refusal at the HTTP edge.
// The upgrade is not atomic: while "air" still runs a pre-v3 daemon it
// keeps PRINTING a label as the address head, so that is what an operator
// on this side reads and types. Its rows carry no canonical, tier 1 cannot
// match, and the fallback used to try the same string as a tmux session
// NAME — which is how a message addressed to one conversation was
// delivered to whichever one happened to sit in a tmux session of that
// name (see the resolver's own regression test).
//
// The refusal gets its own code rather than peer_not_found because the two
// prescribe opposite actions: peer_not_found says check the address, this
// says upgrade the other host. It is 409, not 404 — the request is well
// formed and the target host's state is what refuses it.
func TestSend_RemoteTooOld(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	v2 := remoteRow(remoteSession, "fooc")
	v2.Ref = "" // a pre-v3 daemon has never heard of the field
	v2.Title, v2.TitleSource = "purdex-tester", "user"
	v2.Address = remoteAlias + "/purdex-tester:foo-purdex-b0"
	s.env = remoteEnvelope(v2)

	req := s.sendReq()
	req.To = remoteAlias + "/purdex-tester" // the head that daemon prints
	ae := assertRefused(t, s.send(adminCtx(), req), http.StatusConflict, ipeers.ErrCodeRemoteTooOld)
	for _, want := range []string{`"` + remoteAlias + `"`, "upgrade", "tmux:<name>"} {
		if !strings.Contains(ae.Detail, want) {
			t.Errorf("detail = %q, want it to contain %s", ae.Detail, want)
		}
	}
	if len(s.postCalls()) != 0 {
		t.Errorf("posts = %d, want none — nothing may leave on a guess", len(s.postCalls()))
	}

	// The escape hatch the detail names has to be real: "tmux:<name>" says
	// a place outright, and a v2 daemon reports SessionName exactly as a v3
	// one does, so it must still go through against the same old peer.
	req.To = remoteAlias + "/tmux:" + remoteSession
	if rr := s.send(adminCtx(), req); rr.Code != http.StatusOK {
		t.Fatalf("tmux form: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}

// TestSend_TmuxFormResolves pins the explicit "tmux:<name>" address form:
// it matches PeerRecord.SessionName directly, bypassing the label tier.
func TestSend_TmuxFormResolves(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	req := s.sendReq()
	req.To = remoteAlias + "/tmux:" + remoteSession

	resp := s.sendOK(req)
	if resp.Result != ipeers.ResultDelivered {
		t.Errorf("result = %q, want delivered", resp.Result)
	}
	post := s.onlyPost()
	if post.req.To.AgentSessionID != remoteSessionID {
		t.Errorf("post to = %+v, want the tmux row's tuple", post.req.To)
	}
}

// deadHolderTmuxName is the live target's tmux session name in
// TestSend_DeadHolderLabelFallsToTmuxSession. It is a perfectly ordinary,
// VALID user label — the dead session below really holds it, exactly as
// the claim path would have written it. What keeps the live agent off it
// is the second process, not the spelling: see deadHolderRows.
const deadHolderTmuxName = "foo-bar"

// The live target's second process, in another tmux session.
const (
	deadHolderSpanPID      = 778
	deadHolderSpanTmuxName = "elsewhere"
)

// Readable arguments for deadHolderRows' spansTwoTmuxSessions parameter.
const (
	targetInOneTmuxSession     = false
	targetSpansTwoTmuxSessions = true
)

// deadHolderRows builds the X2 fixture out of the real ipeers.Build: tmux
// session "stale" is owned by a cc conversation that has NO live registry
// entry but still holds the user label tmuxName (an inbox_dead
// owner-fallback row, PID 0), and tmux session tmuxName carries the live
// target.
//
// spansTwoTmuxSessions gives that same live target a SECOND live process,
// in tmux session "elsewhere". Under v2 that flag decided which tier
// answered the bare name, because it decided whether the target derived
// tmuxName as its default label. Under D4 nothing is derived, so both
// shapes now behave identically and only the spanning one is still
// exercised; the parameter stays because the shape it builds (one
// conversation, two tmux sessions) is real and worth keeping buildable.
func deadHolderRows(t *testing.T, tmuxName string, spansTwoTmuxSessions bool) []ipeers.PeerRecord {
	t.Helper()
	const deadSID = "dddddddd-4444-4444-8444-444444444444"
	in := ipeers.BuildInput{
		HostID: remoteHostID, Alias: remoteAlias,
		Sessions: []ipeers.SessionSummary{{Code: "stalec", Name: "stale", Cwd: "/w"}, {Code: "fooc", Name: tmuxName, Cwd: "/w"}},
		Owners: map[string]ipeers.Owner{
			"stalec": {AgentType: "cc", SessionID: deadSID, TmuxPaneID: "%1"},
			"fooc":   {AgentType: "cc", SessionID: remoteSessionID, TmuxPaneID: "%2"},
		},
		Entries: []ipeers.Entry{{
			PID: remotePID, SessionID: remoteSessionID, Name: remoteSession, Cwd: "/w",
			Tmux: tmuxName + ":@1.%2", Inbox: "/tmp/cc-socks/777.sock", ProcStart: remoteProcStart, Version: "2.1.270", Status: "idle",
		}},
		Titles: map[string]ipeers.TitleInfo{deadSID: {Title: tmuxName, Rev: 3}},
	}
	if spansTwoTmuxSessions {
		in.Sessions = append(in.Sessions, ipeers.SessionSummary{Code: "elsec", Name: deadHolderSpanTmuxName, Cwd: "/w"})
		in.Owners["elsec"] = ipeers.Owner{AgentType: "cc", SessionID: remoteSessionID, TmuxPaneID: "%5"}
		in.Entries = append(in.Entries, ipeers.Entry{
			PID: deadHolderSpanPID, SessionID: remoteSessionID, Name: remoteSession + "-2", Cwd: "/w",
			Tmux: deadHolderSpanTmuxName + ":@2.%5", Inbox: "/tmp/cc-socks/778.sock", ProcStart: remoteProcStart, Version: "2.1.270", Status: "idle",
		})
	}
	return ipeers.Build(in)
}

// TestSend_DeadHolderLabelFallsToTmuxSession pins X2 at the module level
// (spec §3.3: a row whose holder is not live is inert). "air/foo-bar" must
// not stop at the dead holder with 409 not_deliverable: tier 1 ignores it
// and tier 2 delivers to the tmux session.
//
// Why the live target does NOT hold the tmux-derived default "foo-bar" is
// a premise of this test, not a coincidence: it has a second live process
// in tmux session "elsewhere" (deadHolderRows' spansTwoTmuxSessions), and
// Under v3 no live row is labelled at all unless it claimed one, so
// nothing live carries "foo-bar" and tier 2 is always what answers the
// bare name here.
func TestSend_DeadHolderLabelFallsToTmuxSession(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	rows := deadHolderRows(t, deadHolderTmuxName, targetSpansTwoTmuxSessions)
	// Sanity: the fixture really is the X2 shape — a dead holder of
	// "foo-bar" and a deliverable tmux session named "foo-bar" with
	// another label. The second condition is also what keeps this test on
	// the tier-2 path.
	var sawDead, sawLive bool
	for _, r := range rows {
		switch {
		case r.SessionName == "stale":
			sawDead = r.Reason == "inbox_dead" && r.Title == deadHolderTmuxName && r.Agent != nil && r.Agent.PID == 0
		case r.SessionName == deadHolderTmuxName:
			// Spelled out rather than "anything but the name": the tier-2
			// path exists only while nothing LIVE holds the label, so a
			// fixture change that quietly labels this row fails here
			// instead of silently retargeting the test at tier 1.
			sawLive = r.Deliverable && r.Title == "" && r.Ref == ipeers.RefID(remoteSessionID)
		}
	}
	if !sawDead || !sawLive {
		t.Fatalf("fixture rows = %+v, want an inbox_dead holder of %q and an unlabelled deliverable tmux session %q", rows, deadHolderTmuxName, deadHolderTmuxName)
	}
	s.set(func(s *sendEnv) { s.env = remoteEnvelope(rows...) })
	req := s.sendReq()
	req.To = remoteAlias + "/" + deadHolderTmuxName

	resp := s.sendOK(req)
	if resp.Result != ipeers.ResultDelivered {
		t.Errorf("result = %q, want delivered", resp.Result)
	}
	if want := remoteAlias + "/" + remoteSession; resp.ToAddress != want {
		t.Errorf("to_address = %q, want %q — the tmux session %q's own row, addressed by its registry name", resp.ToAddress, want, deadHolderTmuxName)
	}
	post := s.onlyPost()
	if post.req.To.AgentSessionID != remoteSessionID || post.req.To.PID != remotePID {
		t.Errorf("post to = %+v, want the tmux session %q's live tuple", post.req.To, deadHolderTmuxName)
	}
}

// TestSend_SingleHitUnderUnknownRegistryFileNotReady pins X1 at the module
// level: the remote reports one alive-but-undecodable registry file and a
// single live row carrying the addressed canonical id. That file may be a
// second process of the same conversation, so the send is 503 not_ready
// (Partial:true) with nothing posted — not a delivery to the one process
// that happened to be readable.
func TestSend_SingleHitUnderUnknownRegistryFileNotReady(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	row := remoteRow("", "")
	row.Ref = ipeers.RefID(remoteSessionID)
	row.Title, row.TitleSource = "purdex-tester", "user"
	s.set(func(s *sendEnv) {
		s.env = ipeers.Envelope{HostID: remoteHostID, OK: true, Partial: true, Peers: []ipeers.PeerRecord{row},
			UnknownRegistryFiles: []string{"/reg/778.json"}}
	})
	req := s.sendReq()
	req.To = remoteAlias + "/" + row.Ref

	rr := s.send(adminCtx(), req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var ae ipeers.APIError
	if err := json.Unmarshal(rr.Body.Bytes(), &ae); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rr.Body.String())
	}
	if ae.Error != ipeers.ErrNotReady || !ae.Partial {
		t.Errorf("body = %+v, want not_ready with partial:true", ae)
	}
	if len(s.postCalls()) != 0 || len(s.rows()) != 0 {
		t.Errorf("post/rows = %d/%d, want none", len(s.postCalls()), len(s.rows()))
	}

	// The same row with the registry complete (Partial for another
	// reason) is a plain single hit and delivers.
	s.set(func(s *sendEnv) {
		s.env = ipeers.Envelope{HostID: remoteHostID, OK: true, Partial: true, Peers: []ipeers.PeerRecord{row}}
	})
	if resp := s.sendOK(req); resp.Result != ipeers.ResultDelivered {
		t.Errorf("registry complete: result = %q, want delivered", resp.Result)
	}
}

// TestSend_PartialInventoryNotReady pins the v2 delta on step 6: when the
// remote's envelope is partial, a miss in the deciding tiers is 503 not_ready
// with Partial:true in the body rather than falling back to the bare tmux-name
// tier — even though that tier would otherwise have matched.
func TestSend_PartialInventoryNotReady(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	s.set(func(s *sendEnv) {
		row := remoteRow(remoteSession, "fooc") // carries no Label
		// remoteRow gives the row a registry name equal to its tmux session
		// name. Peer Address v4 added a NAME tier above the tmux fallback, so
		// leaving them equal would resolve at that tier and never exercise the
		// fallback this test is about. Give the name its own value.
		row.Agent.PeerName = remoteSession + "-b0"
		s.env = ipeers.Envelope{HostID: remoteHostID, OK: true, Partial: true, Peers: []ipeers.PeerRecord{row}}
	})
	req := s.sendReq() // To: remoteAlias + "/" + remoteSession — would match the tmux fallback if reached

	rr := s.send(adminCtx(), req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var ae ipeers.APIError
	if err := json.Unmarshal(rr.Body.Bytes(), &ae); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rr.Body.String())
	}
	if ae.Error != ipeers.ErrNotReady {
		t.Errorf("error = %q, want %q", ae.Error, ipeers.ErrNotReady)
	}
	if !ae.Partial {
		t.Errorf("partial = %v, want true", ae.Partial)
	}
	if len(s.postCalls()) != 0 || len(s.rows()) != 0 {
		t.Errorf("post/rows = %d/%d, want none (refused before the audit insert)", len(s.postCalls()), len(s.rows()))
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
			// Task 5 review finding 2: a bare label-store Snapshot
			// failure marks local.Partial true but hides no rows at
			// all (Peer Address v2's entry rows mean a live origin
			// always has one — TestSend_OriginResolvesViaEntryRowDespitePartialInventory).
			// A dead origin_inbox with no matching row must stay a
			// non-retryable 400 origin_unknown even while the label
			// store is down, never 503 not_ready.
			name:    "origin unknown path: label-store failure alone stays origin_unknown",
			prepare: func(s *sendEnv) { s.m.titles = failingTitles{} },
			mutate:  func(r *ipeers.SendRequest) { r.OriginInbox = "/nonexistent/x.sock" },
			status:  http.StatusBadRequest, code: ipeers.ErrOriginUnknown,
		},
		{
			// The other half of the same fix: an alive-but-undecodable
			// registry file at an UNRELATED pid — the origin_inbox
			// still names no row — turns the same "no row" case into a
			// retryable 503 not_ready, since that file could be the
			// one that would have decoded into the origin's own row.
			name:    "origin unknown path: unrelated unknown registry file is not_ready",
			prepare: func(s *sendEnv) { writeRegistryFixture(s.t, s.regDir, "4242.json", "{") },
			mutate:  func(r *ipeers.SendRequest) { r.OriginInbox = "/nonexistent/x.sock" },
			status:  http.StatusServiceUnavailable, code: ipeers.ErrNotReady,
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

// TestSend_CombinedNameMismatchRefused pins §5.4's refusal of the combined
// form `<name> [<ref>]` when the typed name is not the ref's current name —
// and, more to the point, pins its DETAIL.
//
// The refusal itself is cheap to get right and worthless on its own. What
// the operator has to decide is which of two things happened: the peer
// renamed itself (harmless, re-read the address) or someone handed them
// `trusted-name [attackerRef]` (not harmless at all). That call cannot be
// made from the code, only from the three values — the name they typed, the
// name the ref answers to now, and the ref itself. Before this test the arm
// did not exist: ErrNameMismatch fell through to `default:` and came back as
// a 404 peer_not_found reading "no session %q on %q", which discards two of
// the three and buries the third inside a sentence saying the address was
// never found — the opposite of what happened. It was found, and refused.
//
// 409, not 404, for the same reason ErrRemoteTooOld is 409: the address was
// understood and declined, and "check the address" is the wrong instruction.
func TestSend_CombinedNameMismatchRefused(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	ref := ipeers.RefID(remoteSessionID)
	bare := strings.TrimPrefix(ref, "_")

	req := s.sendReq()
	req.To = remoteAlias + "/decoy-name [" + bare + "]"
	ae := assertRefused(t, s.send(adminCtx(), req), http.StatusConflict, ipeers.ErrCodeNameMismatch)
	for _, want := range []string{`"decoy-name"`, `"` + remoteSession + `"`, bare} {
		if !strings.Contains(ae.Detail, want) {
			t.Errorf("detail = %q, want it to contain %s", ae.Detail, want)
		}
	}
	if len(s.postCalls()) != 0 {
		t.Errorf("posts = %d, want none — a mismatch must not deliver and warn", len(s.postCalls()))
	}

	// The override the refusal exists to leave open: `_<ref>` says "the ref,
	// whatever it is called now" outright, and must still go through.
	req.To = remoteAlias + "/" + ref
	if rr := s.send(adminCtx(), req); rr.Code != http.StatusOK {
		t.Fatalf("ref form: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	// And the matching combined form delivers: the check is a check, not a
	// blanket refusal of the form the peers table prints.
	req.To = remoteAlias + "/" + remoteSession + " [" + bare + "]"
	if rr := s.send(adminCtx(), req); rr.Code != http.StatusOK {
		t.Fatalf("matching combined form: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
}
