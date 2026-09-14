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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/peers/proxyhelper/proxyhelpertest"
	"github.com/wake/purdex/internal/store"
)

// ---------------------------------------------------------------------------
// Scaffolding: a receiving daemon with one deliverable Claude Code target
// whose inbox is a real Unix listener, and a verified remote host "air".
// ---------------------------------------------------------------------------

const (
	localHostID  = "mlab:abc123"
	localAlias   = "mlab"
	remoteHostID = "air:def456"
	remoteAlias  = "air"

	targetSessionID = "fa5d4c07-d9d9-4184-9e13-e491f2f4bf7c"
	targetPID       = 76973
	targetProcStart = "Sun Sep 13 15:22:36 2026"
	targetPeerName  = "purdex-47"

	senderSessionID = "11111111-2222-4333-8444-555555555555"
	senderPID       = 4242
	senderProcStart = "Mon Sep 14 10:00:00 2026"
	senderSession   = "foo"
)

// listenerMode is what the fake target inbox does with a connection.
type listenerMode int

const (
	listenReadAndClose listenerMode = iota // a healthy Claude Code inbox
	listenReadAndHold                      // reads everything, never closes
	listenAbsent                           // a regular file where the socket should be
)

type envOpts struct {
	deliver    *bool             // nil ⇒ true
	hosts      []config.PeerHost // nil ⇒ one verified "air" entry with a return token
	listener   listenerMode
	fixture    fixtureOpts  // variant / readyTimeout / sockWriteTimeout / noSweep
	proxyInfo  map[int]bool // pids Liveness.Info classifies as pdx peer-proxy (D9)
	noRegistry bool         // do not write the target's registry entry
	sessions   []session.SessionInfo
	owners     map[string]agent.PaneOwner
}

func boolp(b bool) *bool { return &b }

func airHost(token string, allowBypass bool) config.PeerHost {
	return config.PeerHost{
		Alias:        remoteAlias,
		URL:          "http://air.invalid:7860",
		HostID:       remoteHostID,
		Token:        token,
		InboundToken: "inbound-air",
		AllowBypass:  allowBypass,
	}
}

// deliverEnv is one receiving daemon under test.
type deliverEnv struct {
	t          *testing.T
	f          *moduleFixture
	m          *Module
	root       string // short /tmp root: the target inbox lives here
	regDir     string // the module's (and the helper manager's) registry dir
	targetSock string
	lines      chan string   // every line the target inbox received
	release    chan struct{} // closed at cleanup: lets a held listener go
}

func newDeliverEnv(t *testing.T, o envOpts) *deliverEnv {
	t.Helper()
	deliver := true
	if o.deliver != nil {
		deliver = *o.deliver
	}
	hosts := o.hosts
	if hosts == nil {
		hosts = []config.PeerHost{airHost("outbound-air", false)}
	}
	cfg := &config.Config{
		HostID: localHostID,
		Peers:  config.PeersConfig{Alias: localAlias, Hosts: hosts, Deliver: deliver},
	}
	c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})

	// A short /tmp root of its own (socket paths are length-limited): the
	// target inbox lives at its top and the registry dir is handed to the
	// fixture, so the helper manager registers its helpers where
	// localEnvelope reads. The fixture's own sock dir holds helper sockets.
	_, regDir := proxyhelpertest.TempDirs(t)
	root := filepath.Dir(regDir)
	targetSock := filepath.Join(root, "target.sock")

	e := &deliverEnv{
		t:          t,
		root:       root,
		regDir:     regDir,
		targetSock: targetSock,
		lines:      make(chan string, 64),
		release:    make(chan struct{}),
	}
	t.Cleanup(func() { close(e.release) })
	e.startListener(o.listener)

	if !o.noRegistry {
		writeRegistryFixture(t, regDir, strconv.Itoa(targetPID)+".json", targetRegistryJSON(targetSock))
	}

	fo := o.fixture
	fo.core = c
	fo.sessions = &fakeSessions{sessions: o.sessions}
	fo.owners = &fakeOwners{owners: o.owners}
	fo.registryDir = regDir
	fo.liveness = deliverLiveness(o.proxyInfo)
	fo.budget = 2 * time.Second
	e.f = newTestModuleWith(t, fo)
	e.m = e.f.m
	return e
}

// targetRegistryJSON is fixture76973 rebound to a local inbox path and
// stripped of its tmux field, so the target is an outside-tmux cc: row.
func targetRegistryJSON(inbox string) string {
	return `{"pid":` + strconv.Itoa(targetPID) + `,"sessionId":"` + targetSessionID + `","cwd":"/w","procStart":"` + targetProcStart + `","version":"2.1.270","messagingSocketPath":"` + inbox + `","name":"` + targetPeerName + `","status":"idle"}`
}

// targetRegistryJSONInTmux is targetRegistryJSON with a tmux field naming
// a pane inside a listed session (e.g. "foo:@1.%1"): the target is then
// that session's row, reached only through its owner resolution.
func targetRegistryJSONInTmux(inbox, tmux string) string {
	return `{"pid":` + strconv.Itoa(targetPID) + `,"sessionId":"` + targetSessionID + `","cwd":"/w","procStart":"` + targetProcStart + `","version":"2.1.270","tmux":"` + tmux + `","messagingSocketPath":"` + inbox + `","name":"` + targetPeerName + `","status":"idle"}`
}

// deliverLiveness treats every registry entry whose inbox exists on disk
// as live: the fake helpers' entries carry proxyhelpertest.ProcStart(pid)
// and everything else fixture76973ProcStart. proxyInfo, when non-nil,
// enables the D9 Info seam and classifies the listed pids as peer-proxy.
func deliverLiveness(proxyInfo map[int]bool) ipeers.Liveness {
	startOf := func(pid int) time.Time {
		if pid >= 900000 {
			s, _ := proxyhelpertest.ProcStart(pid)
			ts, _ := ipeers.ParseProcStart(s)
			return ts
		}
		return fixture76973ProcStart
	}
	l := ipeers.Liveness{
		Stat: func(path string) error {
			_, err := os.Stat(path)
			return err
		},
		PidAlive:  func(int) bool { return true },
		StartTime: func(pid int) (time.Time, error) { return startOf(pid), nil },
	}
	if proxyInfo != nil {
		l.Info = func(pid int) (iagent.ProcessInfo, error) {
			argv := []string{"claude"}
			if proxyInfo[pid] {
				argv = []string{"/usr/local/bin/pdx", "peer-proxy"}
			}
			return iagent.ProcessInfo{PID: pid, Argv: argv, StartTime: startOf(pid)}, nil
		}
	}
	return l
}

func (e *deliverEnv) startListener(mode listenerMode) {
	e.t.Helper()
	if mode == listenAbsent {
		if err := os.WriteFile(e.targetSock, nil, 0o600); err != nil {
			e.t.Fatal(err)
		}
		return
	}
	ln, err := net.Listen("unix", e.targetSock)
	if err != nil {
		e.t.Fatalf("listen %s: %v", e.targetSock, err)
	}
	e.t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				data, _ := io.ReadAll(conn)
				if mode == listenReadAndHold {
					<-e.release
				}
				conn.Close()
				for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
					if len(line) > 0 {
						e.lines <- string(line)
					}
				}
			}()
		}
	}()
}

func (e *deliverEnv) request() ipeers.DeliverRequest {
	return ipeers.DeliverRequest{
		MsgID: uuid.NewString(),
		From: ipeers.WireFrom{
			HostID:         remoteHostID,
			AgentSessionID: senderSessionID,
			PID:            senderPID,
			ProcStart:      senderProcStart,
			PeerName:       senderSession,
			SessionName:    senderSession,
			DeclaredMode:   ipeers.ModePrompting,
		},
		To:   ipeers.WireTo{AgentSessionID: targetSessionID, PID: targetPID, ProcStart: targetProcStart},
		Text: "hello from air",
	}
}

func (e *deliverEnv) hostCtx() context.Context {
	return middleware.WithPrincipal(context.Background(), middleware.Principal{
		Kind: middleware.PrincipalHost, Alias: remoteAlias, HostID: remoteHostID,
	})
}

// post serves one POST /api/peers/deliver through the mux with body
// (marshalled unless it is already a []byte) under ctx.
func (e *deliverEnv) post(ctx context.Context, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var raw []byte
	switch b := body.(type) {
	case []byte:
		raw = b
	default:
		var err error
		if raw, err = json.Marshal(body); err != nil {
			e.t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	e.m.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/peers/deliver", bytes.NewReader(raw)).WithContext(ctx)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// deliverOK posts req as the verified host and asserts a 200; it returns
// the decoded response.
func (e *deliverEnv) deliverOK(req ipeers.DeliverRequest) ipeers.DeliverResponse {
	e.t.Helper()
	rr := e.post(e.hostCtx(), req)
	if rr.Code != http.StatusOK {
		e.t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp ipeers.DeliverResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		e.t.Fatalf("decode response: %v; body=%s", err, rr.Body.String())
	}
	return resp
}

// recvLine waits (bounded) for the next line on the target inbox.
func (e *deliverEnv) recvLine() string {
	e.t.Helper()
	select {
	case l := <-e.lines:
		return l
	case <-time.After(5 * time.Second):
		e.t.Fatal("target inbox received no line within 5 s")
		return ""
	}
}

// assertNoLine asserts nothing reached the target inbox in a short window.
func (e *deliverEnv) assertNoLine() {
	e.t.Helper()
	select {
	case l := <-e.lines:
		e.t.Fatalf("target inbox received an unexpected line: %s", l)
	case <-time.After(100 * time.Millisecond):
	}
}

func (e *deliverEnv) rows() []store.PeerMessage {
	e.t.Helper()
	rows, err := e.f.audit.Tail(100)
	if err != nil {
		e.t.Fatalf("Tail: %v", err)
	}
	return rows
}

// onlyRow asserts exactly one audit row exists and returns it.
func (e *deliverEnv) onlyRow() store.PeerMessage {
	e.t.Helper()
	rows := e.rows()
	if len(rows) != 1 {
		e.t.Fatalf("audit rows = %d, want 1: %+v", len(rows), rows)
	}
	return rows[0]
}

func decodeAPIError(t *testing.T, rr *httptest.ResponseRecorder) ipeers.APIError {
	t.Helper()
	var ae ipeers.APIError
	if err := json.Unmarshal(rr.Body.Bytes(), &ae); err != nil {
		t.Fatalf("decode error body: %v; body=%s", err, rr.Body.String())
	}
	return ae
}

func assertRefused(t *testing.T, rr *httptest.ResponseRecorder, status int, code string) ipeers.APIError {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, status, rr.Body.String())
	}
	ae := decodeAPIError(t, rr)
	if ae.Error != code {
		t.Fatalf("error = %q, want %q (detail %q)", ae.Error, code, ae.Detail)
	}
	return ae
}

// helperSockOf extracts the reply socket from a delivered frame line.
func helperSockOf(t *testing.T, line string) string {
	t.Helper()
	fr, err := ccuds.ParseFrame([]byte(line))
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	sock, ok := ccuds.FromSocket(fr.From)
	if !ok {
		t.Fatalf("frame from = %q, want uds:<sock>", fr.From)
	}
	return sock
}

// helperRegistrySessionID reads the fake helper's own registry entry.
func helperRegistrySessionID(t *testing.T, regDir string, pid int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(regDir, strconv.Itoa(pid)+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.SessionID
}

// fillHelperCap makes the manager believe HelperCap helpers of other
// origins may still be alive (unresolved records from a previous run),
// so the next Acquire of a new origin hits the cap without spawning.
func fillHelperCap(m *helperManager) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := 0; i < HelperCap; i++ {
		m.unresolved = append(m.unresolved, unresolvedRecord{
			proxyRecord: proxyRecord{PID: 700000 + i, Origin: originN(100 + i)},
			occupies:    true,
		})
	}
}

// ---------------------------------------------------------------------------
// Happy path and the frame contract
// ---------------------------------------------------------------------------

func TestDeliver_HappyPath(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	req := e.request()

	resp := e.deliverOK(req)
	if resp.MsgID != req.MsgID || resp.Result != ipeers.ResultDelivered || resp.EffectiveMode != ipeers.ModePrompting || resp.OneWay {
		t.Errorf("response = %+v, want {msg_id %s delivered prompting one_way=false}", resp, req.MsgID)
	}

	line := e.recvLine()
	e.assertNoLine()

	fr, err := ccuds.ParseFrame([]byte(line))
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	if fr.MsgID != req.MsgID {
		t.Errorf("frame msg_id = %q, want %q", fr.MsgID, req.MsgID)
	}
	if fr.Type != "user" || fr.MsgV != 1 {
		t.Errorf("frame type/msgV = %q/%d, want user/1", fr.Type, fr.MsgV)
	}
	sock, ok := ccuds.FromSocket(fr.From)
	if !ok || !strings.HasPrefix(sock, e.f.sockDir+"/") {
		t.Errorf("frame from = %q, want uds:<%s/…>", fr.From, e.f.sockDir)
	}
	w, ok := ccuds.Parse(fr.Message.Content)
	if !ok {
		t.Fatalf("content is not a cross-session-message wrapper: %q", fr.Message.Content)
	}
	if w.From != "uds:"+sock {
		t.Errorf("wrapper from = %q, want %q", w.From, "uds:"+sock)
	}
	if w.FromName != remoteAlias+"/"+senderSession {
		t.Errorf("wrapper from-name = %q, want %q", w.FromName, remoteAlias+"/"+senderSession)
	}
	if w.FromMode != ipeers.ModePrompting {
		t.Errorf("wrapper from-mode = %q, want prompting", w.FromMode)
	}
	if w.HopChain != "" {
		t.Errorf("wrapper hop-chain = %q, want empty", w.HopChain)
	}
	if w.Text != req.Text {
		t.Errorf("wrapper text = %q, want %q", w.Text, req.Text)
	}

	row := e.onlyRow()
	if row.MsgID != req.MsgID || row.Direction != store.DirIn {
		t.Errorf("row = %+v, want msg_id %s direction in", row, req.MsgID)
	}
	if row.FromHostID != remoteHostID || row.FromSessionID != senderSessionID || row.ToHostID != localHostID || row.ToSessionID != targetSessionID {
		t.Errorf("row tuple = %s/%s → %s/%s", row.FromHostID, row.FromSessionID, row.ToHostID, row.ToSessionID)
	}
	if row.DeclaredMode != ipeers.ModePrompting || row.EffectiveMode != ipeers.ModePrompting {
		t.Errorf("row modes = %q/%q, want prompting/prompting", row.DeclaredMode, row.EffectiveMode)
	}
	if row.Result != ipeers.ResultDelivered || row.Error != "" {
		t.Errorf("row result/error = %q/%q, want delivered/\"\"", row.Result, row.Error)
	}
	if row.Bytes != len(req.Text) {
		t.Errorf("row bytes = %d, want %d", row.Bytes, len(req.Text))
	}
	if e.f.postCalls.Load() != 0 {
		t.Errorf("post seam called %d times", e.f.postCalls.Load())
	}
}

// TestDeliver_HelperSurvivesRequestContext pins B1: the helper is owned
// by the manager, not the request. After the response the request context
// is cancelled; a frame written into the helper's socket must still reach
// onFrame — and, through it, the reply path (reply.go), which forwards it
// to the origin host.
func TestDeliver_HelperSurvivesRequestContext(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	forwarded := make(chan ipeers.DeliverRequest, 1)
	e.m.post = func(_ context.Context, _ *http.Client, _, _ string, req ipeers.DeliverRequest) (ipeers.DeliverResponse, *ipeers.RemoteError, error) {
		forwarded <- req
		return ipeers.DeliverResponse{MsgID: req.MsgID, Result: ipeers.ResultDelivered, EffectiveMode: ipeers.ModePrompting}, nil, nil
	}
	ctx, cancel := context.WithCancel(e.hostCtx())
	rr := e.post(ctx, e.request())
	cancel()
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	sock := helperSockOf(t, e.recvLine())

	reply := `{"msgV":1,"msg_id":"` + uuid.NewString() + `","type":"user","priority":"next","from":"uds:` + e.targetSock + `","message":{"role":"user","content":"pong"}}`
	proxyhelpertest.WriteToSock(t, sock, reply)

	select {
	case ev := <-e.f.frames:
		if ev.h == nil || ev.h.sock != sock {
			t.Errorf("onFrame helper sock = %q, want %q", ev.h.sock, sock)
		}
		if ev.line != reply {
			t.Errorf("onFrame line = %q, want %q", ev.line, reply)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("onFrame did not fire within 5 s after the request context was cancelled")
	}
	select {
	case req := <-forwarded:
		if req.Text != "pong" || req.To.AgentSessionID != senderSessionID {
			t.Errorf("forwarded reply = %+v, want text pong to the origin %s", req, senderSessionID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the reply was not forwarded to the origin within 5 s")
	}
}

func TestDeliver_ModeClamp(t *testing.T) {
	cases := []struct {
		name        string
		allowBypass bool
		declared    string
		want        string
	}{
		{"bypass refused", false, ipeers.ModeBypass, ipeers.ModePrompting},
		{"bypass allowed", true, ipeers.ModeBypass, ipeers.ModeBypass},
		{"prompting stays", true, ipeers.ModePrompting, ipeers.ModePrompting},
		{"empty means prompting", true, "", ipeers.ModePrompting},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newDeliverEnv(t, envOpts{hosts: []config.PeerHost{airHost("tok", c.allowBypass)}})
			req := e.request()
			req.From.DeclaredMode = c.declared
			resp := e.deliverOK(req)
			if resp.EffectiveMode != c.want {
				t.Errorf("effective_mode = %q, want %q", resp.EffectiveMode, c.want)
			}
			fr, _ := ccuds.ParseFrame([]byte(e.recvLine()))
			w, _ := ccuds.Parse(fr.Message.Content)
			if w.FromMode != c.want {
				t.Errorf("wrapper from-mode = %q, want %q", w.FromMode, c.want)
			}
			row := e.onlyRow()
			wantDeclared := c.declared
			if wantDeclared == "" {
				wantDeclared = ipeers.ModePrompting
			}
			if row.DeclaredMode != wantDeclared || row.EffectiveMode != c.want {
				t.Errorf("row modes = %q/%q, want %q/%q", row.DeclaredMode, row.EffectiveMode, wantDeclared, c.want)
			}
		})
	}
}

func TestDeliver_HopChainCarried(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	req := e.request()
	req.HopChain = "abc>def"
	e.deliverOK(req)
	fr, _ := ccuds.ParseFrame([]byte(e.recvLine()))
	w, ok := ccuds.Parse(fr.Message.Content)
	if !ok || w.HopChain != "abc>def" {
		t.Errorf("wrapper hop-chain = %q (ok %v), want abc>def", w.HopChain, ok)
	}
}

func TestDeliver_OneWayWithoutReturnToken(t *testing.T) {
	e := newDeliverEnv(t, envOpts{hosts: []config.PeerHost{airHost("", false)}})
	resp := e.deliverOK(e.request())
	if !resp.OneWay || resp.Result != ipeers.ResultDelivered {
		t.Errorf("response = %+v, want delivered one_way=true", resp)
	}
	e.recvLine()
	row := e.onlyRow()
	if row.Result != ipeers.ResultDelivered || row.Error != ipeers.ErrNoReturnRoute {
		t.Errorf("row result/error = %q/%q, want delivered/no_return_route", row.Result, row.Error)
	}
}

// ---------------------------------------------------------------------------
// Refusals and the audit coverage contract
// ---------------------------------------------------------------------------

func TestDeliver_AliasReboundToAnotherHostAfterAuth(t *testing.T) {
	// The principal was authenticated for air = remoteHostID, but by the
	// time the handler runs the alias names a different host (M1).
	e := newDeliverEnv(t, envOpts{hosts: []config.PeerHost{{
		Alias: remoteAlias, URL: "http://x", HostID: "other:999", Token: "t", InboundToken: "i",
	}}})
	rr := e.post(e.hostCtx(), e.request())
	assertRefused(t, rr, http.StatusForbidden, ipeers.ErrHostUnverified)
	e.assertNoLine()
	if n := len(e.rows()); n != 0 {
		t.Errorf("audit rows = %d, want 0", n)
	}
	if e.f.fake.Spawns() != 0 {
		t.Errorf("helper spawns = %d, want 0", e.f.fake.Spawns())
	}
}

func TestDeliver_DuplicateRefusedOnceDelivered(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	req := e.request()
	e.deliverOK(req)
	e.recvLine()
	rr := e.post(e.hostCtx(), req)
	assertRefused(t, rr, http.StatusConflict, ipeers.ErrDuplicate)
	e.assertNoLine()
	if n := len(e.rows()); n != 1 {
		t.Errorf("audit rows = %d, want 1 (the first attempt only)", n)
	}
}

func TestDeliver_TargetProxyRows(t *testing.T) {
	t.Run("own helper", func(t *testing.T) {
		e := newDeliverEnv(t, envOpts{})
		e.deliverOK(e.request())
		e.recvLine()
		pid := e.f.fake.LastPID()
		ps, _ := proxyhelpertest.ProcStart(pid)
		req := e.request()
		req.To = ipeers.WireTo{AgentSessionID: helperRegistrySessionID(t, e.regDir, pid), PID: pid, ProcStart: ps}
		rr := e.post(e.hostCtx(), req)
		assertRefused(t, rr, http.StatusConflict, ipeers.ErrTargetGone)
		e.assertNoLine()
		rows := e.rows()
		if len(rows) != 2 || rows[1].Result != ipeers.ErrTargetGone {
			t.Errorf("rows = %+v, want a second row with result target_gone", rows)
		}
	})
	t.Run("foreign helper via IsProxy", func(t *testing.T) {
		e := newDeliverEnv(t, envOpts{proxyInfo: map[int]bool{555: true}})
		foreignSock := filepath.Join(e.root, "foreign.sock")
		ln, err := net.Listen("unix", foreignSock)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		writeRegistryFixture(t, e.regDir, "555.json", `{"pid":555,"sessionId":"55555555-5555-4555-8555-555555555555","cwd":"/w","procStart":"`+targetProcStart+`","messagingSocketPath":"`+foreignSock+`","name":"air/foo"}`)
		req := e.request()
		req.To = ipeers.WireTo{AgentSessionID: "55555555-5555-4555-8555-555555555555", PID: 555, ProcStart: targetProcStart}
		rr := e.post(e.hostCtx(), req)
		assertRefused(t, rr, http.StatusConflict, ipeers.ErrTargetGone)
		e.assertNoLine()
		if row := e.onlyRow(); row.Result != ipeers.ErrTargetGone {
			t.Errorf("row result = %q, want target_gone", row.Result)
		}
	})
}

func TestDeliver_TargetGoneDetailNamesFieldOnly(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	req := e.request()
	req.To.PID = targetPID + 1
	ae := assertRefused(t, e.post(e.hostCtx(), req), http.StatusConflict, ipeers.ErrTargetGone)
	if !strings.Contains(ae.Detail, "pid") {
		t.Errorf("detail = %q, want it to name pid", ae.Detail)
	}
	for _, leak := range []string{strconv.Itoa(targetPID), targetPeerName, e.targetSock, targetProcStart} {
		if strings.Contains(ae.Detail, leak) {
			t.Errorf("detail %q leaks the target's %q", ae.Detail, leak)
		}
	}
}

// TestDeliver_ClientGoneWhileHelperStarts pins step 9's caller-gone
// branch: the request context is cancelled while the sender's helper is
// still starting (Barrier variant). The handler returns without writing
// anything — on an httptest.ResponseRecorder that is the recorder's
// default 200 with an empty body (net/http would send the same to a
// client that is, by definition, no longer there) — the audit row says
// client_gone, and the helper keeps starting under the manager: once the
// barrier opens, Acquire for the same origin succeeds.
func TestDeliver_ClientGoneWhileHelperStarts(t *testing.T) {
	e := newDeliverEnv(t, envOpts{fixture: fixtureOpts{variant: proxyhelpertest.Barrier}})
	ctx, cancel := context.WithCancel(e.hostCtx())
	defer cancel()

	req := e.request()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.post(ctx, req) }()

	// The fake's spawn counter moves inside the manager's startup
	// goroutine, which Acquire launched: from here on the handler is (or
	// is about to be) parked on the helper's ready channel.
	eventually(t, 5*time.Second, func() bool { return e.f.fake.Spawns() == 1 }, "helper spawn did not start")
	cancel()

	var rr *httptest.ResponseRecorder
	select {
	case rr = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return within 5 s of the request context being cancelled")
	}
	if rr.Code != http.StatusOK || rr.Body.Len() != 0 {
		t.Errorf("recorder = %d %q, want the untouched recorder default (200, empty body)", rr.Code, rr.Body.String())
	}
	if row := e.onlyRow(); row.Result != resultClientGone || row.Error != "" {
		t.Errorf("row result/error = %q/%q, want client_gone/\"\"", row.Result, row.Error)
	}
	e.assertNoLine()

	e.f.fake.Release()
	acquireCtx, acquireCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer acquireCancel()
	h, err := e.m.helpers.Acquire(acquireCtx, req.From.Key(), "air/foo")
	if err != nil {
		t.Fatalf("Acquire after the barrier opened: %v", err)
	}
	if h.sock == "" || e.f.fake.Spawns() != 1 {
		t.Errorf("helper = %+v spawns = %d, want the same single instance ready", h, e.f.fake.Spawns())
	}
}

// TestDeliver_InventoryUnavailableDetailFixed pins that a local inventory
// failure is this daemon's own trouble, not a verdict on the target: it
// answers 503 not_ready with a fixed detail (never target_gone, which
// would make the origin reap the sender's helper), and the local error
// text is kept in the audit row only.
func TestDeliver_InventoryUnavailableDetailFixed(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	e.m.sessions = &fakeSessions{err: errFakeProvider}
	ae := assertRefused(t, e.post(e.hostCtx(), e.request()), http.StatusServiceUnavailable, ipeers.ErrNotReady)
	if ae.Detail != "inventory unavailable" {
		t.Errorf("detail = %q, want the fixed %q", ae.Detail, "inventory unavailable")
	}
	row := e.onlyRow()
	if row.Result != ipeers.ErrNotReady || !strings.Contains(row.Error, errFakeProvider.Error()) {
		t.Errorf("row result/error = %q/%q, want not_ready with the local error text", row.Result, row.Error)
	}
	if strings.Contains(ae.Detail, errFakeProvider.Error()) {
		t.Errorf("detail %q leaks the local error text", ae.Detail)
	}
}

// TestDeliver_PartialInventoryIsNotReady pins spec §4.2's partial
// semantics on the receiver (R2-A). Two of its original subtests (below)
// now deliver rather than answer not_ready: Peer Address v2 gives every
// live, non-proxy registry entry its own entry row (spec §3.4) whether or
// not its tmux session is listed, so a target whose tmux session's owner
// lookup failed/never ran is still found and resolved through its own
// entry — the old "no row for an unresolved session's tmux name" premise
// no longer holds. The not_ready guard returns as a new case (an
// undecodable/unknown registry file) in a later task. A tuple that is
// genuinely missing from a complete inventory is still target_gone.
func TestDeliver_PartialInventoryIsNotReady(t *testing.T) {
	inTmux := envOpts{noRegistry: true, sessions: []session.SessionInfo{{Code: "s1", Name: "foo"}}}
	t.Run("owner lookup fails ⇒ delivered via the target's entry row", func(t *testing.T) {
		e := newDeliverEnv(t, inTmux)
		writeRegistryFixture(t, e.regDir, strconv.Itoa(targetPID)+".json", targetRegistryJSONInTmux(e.targetSock, "foo:@1.%1"))
		e.m.owners = &fakeOwners{errs: map[string]error{"s1": errors.New("resolver timeout")}}

		e.deliverOK(e.request())
		e.recvLine()
	})
	t.Run("owner lookup exceeds the budget ⇒ delivered via the target's entry row", func(t *testing.T) {
		e := newDeliverEnv(t, inTmux)
		writeRegistryFixture(t, e.regDir, strconv.Itoa(targetPID)+".json", targetRegistryJSONInTmux(e.targetSock, "foo:@1.%1"))
		e.m.owners = &fakeOwners{owners: map[string]agent.PaneOwner{"s1": {AgentType: "cc", SessionID: targetSessionID, TmuxPaneID: "%1"}}}
		e.m.budget = 0 // the deadline is spent before the first resolution starts

		e.deliverOK(e.request())
		e.recvLine()
	})
	t.Run("owner resolves ⇒ delivered", func(t *testing.T) {
		// The same tuple, once its session's owner lookup completes, is
		// the target: the partial answer above was about the inventory,
		// not the session.
		e := newDeliverEnv(t, inTmux)
		writeRegistryFixture(t, e.regDir, strconv.Itoa(targetPID)+".json", targetRegistryJSONInTmux(e.targetSock, "foo:@1.%1"))
		e.m.owners = &fakeOwners{owners: map[string]agent.PaneOwner{"s1": {AgentType: "cc", SessionID: targetSessionID, TmuxPaneID: "%1"}}}
		e.deliverOK(e.request())
		e.recvLine()
	})
	t.Run("missing from a complete inventory ⇒ target_gone", func(t *testing.T) {
		e := newDeliverEnv(t, envOpts{noRegistry: true, sessions: []session.SessionInfo{{Code: "s1", Name: "foo"}}, owners: map[string]agent.PaneOwner{}})
		ae := assertRefused(t, e.post(e.hostCtx(), e.request()), http.StatusConflict, ipeers.ErrTargetGone)
		if !strings.Contains(ae.Detail, "no live cc session") {
			t.Errorf("detail = %q, want the no-session detail", ae.Detail)
		}
		if row := e.onlyRow(); row.Result != ipeers.ErrTargetGone {
			t.Errorf("row result = %q, want target_gone", row.Result)
		}
	})
	t.Run("partial but a candidate with another pid ⇒ target_gone", func(t *testing.T) {
		// The tuple's session id IS in the inventory (an outside-tmux row)
		// with a different pid: that is a verdict on the session, whatever
		// else is unresolved.
		e := newDeliverEnv(t, envOpts{sessions: []session.SessionInfo{{Code: "s1", Name: "other"}}})
		e.m.owners = &fakeOwners{errs: map[string]error{"s1": errors.New("resolver timeout")}}
		req := e.request()
		req.To.PID = targetPID + 1
		assertRefused(t, e.post(e.hostCtx(), req), http.StatusConflict, ipeers.ErrTargetGone)
	})
}

// TestDeliver_UnknownRegistryFileIsNotReady pins that an alive-but-
// undecodable registry file makes ANY negative verdict on the target
// untrustworthy, even one findTarget reaches by way of a genuine
// candidate row (the target's own session id, just with a mismatched
// field): the target's own registry file is corrupted after its tmux
// session already resolved the owner, so findTarget finds a candidate row
// (the owner-fallback session record) whose pid does not match — normally
// target_gone, but the unknown file must force not_ready instead, so the
// origin never reaps the sender's helper over what may just be a registry
// write race.
func TestDeliver_UnknownRegistryFileIsNotReady(t *testing.T) {
	inTmux := envOpts{noRegistry: true, sessions: []session.SessionInfo{{Code: "s1", Name: "foo"}}}
	e := newDeliverEnv(t, inTmux)
	writeRegistryFixture(t, e.regDir, strconv.Itoa(targetPID)+".json", targetRegistryJSONInTmux(e.targetSock, "foo:@1.%1"))
	e.m.owners = &fakeOwners{owners: map[string]agent.PaneOwner{"s1": {AgentType: "cc", SessionID: targetSessionID, TmuxPaneID: "%1"}}}

	// Corrupt the target's own registry file: its pid is alive (the fake
	// liveness treats every pid as alive) ⇒ an alive unknown.
	writeRegistryFixture(t, e.regDir, strconv.Itoa(targetPID)+".json", "{")

	ae := assertRefused(t, e.post(e.hostCtx(), e.request()), http.StatusServiceUnavailable, ipeers.ErrNotReady)
	if ae.Detail != detailInventoryPartial {
		t.Errorf("detail = %q, want %q", ae.Detail, detailInventoryPartial)
	}
	if row := e.onlyRow(); row.Result != ipeers.ErrNotReady {
		t.Errorf("row result = %q, want not_ready", row.Result)
	}
}

func TestDeliver_RateLimited31st(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	for i := 0; i < ipeers.PairRateLimit; i++ {
		e.deliverOK(e.request())
		e.recvLine()
	}
	rr := e.post(e.hostCtx(), e.request())
	assertRefused(t, rr, http.StatusTooManyRequests, ipeers.ErrRateLimited)
	e.assertNoLine()
	rows := e.rows()
	if len(rows) != ipeers.PairRateLimit+1 || rows[len(rows)-1].Result != ipeers.ErrRateLimited {
		t.Errorf("rows = %d (last %q), want %d with the last rate_limited", len(rows), rows[len(rows)-1].Result, ipeers.PairRateLimit+1)
	}
	if e.f.fake.Spawns() != 1 {
		t.Errorf("helper spawns = %d, want 1 (one origin)", e.f.fake.Spawns())
	}
}

// TestDeliver_HostRateLimited121st pins the per-host admission limit
// (R2-E): the 121st /deliver from one authenticated host within a minute
// is refused 429 rate_limited right after the entry binding — before the
// body is decoded, before dedup, before the audit insert and before any
// inventory is built — unaudited, with one warn line; another host is
// unaffected.
func TestDeliver_HostRateLimited121st(t *testing.T) {
	const (
		seaHostID = "sea:0011"
		seaAlias  = "sea"
	)
	e := newDeliverEnv(t, envOpts{hosts: []config.PeerHost{
		airHost("outbound-air", false),
		{Alias: seaAlias, URL: "http://sea.invalid:7860", HostID: seaHostID, Token: "outbound-sea", InboundToken: "inbound-sea"},
	}})
	sessions := e.m.sessions.(*fakeSessions)
	for i := 0; i < ipeers.HostRateLimit; i++ {
		if !e.m.hostLimit.Allow(remoteHostID) {
			t.Fatalf("host admission #%d refused, want the first %d allowed", i+1, ipeers.HostRateLimit)
		}
	}
	listed := sessions.listCalls.Load()

	// Even a body that would otherwise be a 400 is refused 429: the
	// admission check precedes the decode.
	ae := assertRefused(t, e.post(e.hostCtx(), []byte("{nope")), http.StatusTooManyRequests, ipeers.ErrRateLimited)
	if ae.Detail != "host rate limit exceeded" {
		t.Errorf("detail = %q, want the fixed %q", ae.Detail, "host rate limit exceeded")
	}
	req := e.request()
	assertRefused(t, e.post(e.hostCtx(), req), http.StatusTooManyRequests, ipeers.ErrRateLimited)
	e.assertNoLine()
	if n := sessions.listCalls.Load(); n != listed {
		t.Errorf("inventory built %d times for refused requests, want 0", n-listed)
	}
	if n := len(e.rows()); n != 0 {
		t.Errorf("audit rows = %d, want 0 (unaudited)", n)
	}
	if e.m.dedup.Seen(req.MsgID) {
		t.Errorf("msg_id recorded by dedup before admission")
	}
	if e.f.fake.Spawns() != 0 {
		t.Errorf("helper spawns = %d, want 0", e.f.fake.Spawns())
	}
	var warned int
	for _, l := range e.f.logs.all() {
		if strings.Contains(l, ipeers.ErrRateLimited) && strings.Contains(l, "host rate limit exceeded") && strings.Contains(l, remoteAlias) {
			warned++
		}
	}
	if warned != 2 {
		t.Errorf("warn lines = %d, want one per refusal: %v", warned, e.f.logs.all())
	}

	// The other host's admission is its own: it delivers.
	seaCtx := middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalHost, Alias: seaAlias, HostID: seaHostID})
	seaReq := e.request()
	seaReq.From.HostID = seaHostID
	rr := e.post(seaCtx, seaReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("sea: status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	e.recvLine()
	if rows := e.rows(); len(rows) != 1 || rows[0].FromHostID != seaHostID || rows[0].Result != ipeers.ResultDelivered {
		t.Errorf("rows = %+v, want one delivered row from %s", rows, seaHostID)
	}
}

// TestHostLimiter_WindowSlides pins the host limiter on the shared clock
// seam: HostRateLimit admissions, the next refused, admitted again once
// the window has slid past the first.
func TestHostLimiter_WindowSlides(t *testing.T) {
	clock := &manualClock{t: time.Unix(0, 0)}
	l := newHostLimiter(ipeers.HostRateLimit, ipeers.HostRateWindow, clock.Now)
	for i := 0; i < ipeers.HostRateLimit; i++ {
		if !l.Allow("air:1") {
			t.Fatalf("Allow #%d = false", i+1)
		}
	}
	if l.Allow("air:1") {
		t.Errorf("Allow #%d = true, want refused", ipeers.HostRateLimit+1)
	}
	if !l.Allow("sea:1") {
		t.Errorf("another host refused")
	}
	clock.Advance(ipeers.HostRateWindow)
	if !l.Allow("air:1") {
		t.Errorf("Allow after the window = false, want admitted")
	}
}

func TestDeliver_AuditInsertFailure(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	e.f.audit.fail(errors.New("disk full"), nil)
	rr := e.post(e.hostCtx(), e.request())
	assertRefused(t, rr, http.StatusServiceUnavailable, ipeers.ErrAuditUnavailable)
	e.assertNoLine()
	if e.f.fake.Spawns() != 0 {
		t.Errorf("helper spawns = %d, want 0", e.f.fake.Spawns())
	}
}

func TestDeliver_SetResultFailureLoggedNotSurfaced(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	e.f.audit.fail(nil, errors.New("locked"))
	resp := e.deliverOK(e.request())
	if resp.Result != ipeers.ResultDelivered {
		t.Errorf("result = %q, want delivered", resp.Result)
	}
	e.recvLine()
	row := e.onlyRow()
	if row.Result != "" {
		t.Errorf("row result = %q, want \"\" (SetResult failed)", row.Result)
	}
	logs := strings.Join(e.f.logs.all(), "\n")
	if !strings.Contains(logs, "locked") || !strings.Contains(logs, strconv.FormatInt(row.ID, 10)) {
		t.Errorf("logs lack the SetResult failure with row id %d: %s", row.ID, logs)
	}
}

func TestDeliver_AfterStopNotReady(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	if err := e.m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	rr := e.post(e.hostCtx(), e.request())
	assertRefused(t, rr, http.StatusServiceUnavailable, ipeers.ErrNotReady)
	e.assertNoLine()
	if n := len(e.rows()); n != 0 {
		t.Errorf("audit rows = %d, want 0", n)
	}
}

// TestDeliver_ProxyPIDsHideOwnHelper pins that the helper's own registry
// entry is reported as a proxy row by GET /api/peers.
func TestDeliver_ProxyPIDsHideOwnHelper(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	e.deliverOK(e.request())
	e.recvLine()
	pid := e.f.fake.LastPID()

	rr := doGetPeers(t, e.m, "/api/peers")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/peers = %d", rr.Code)
	}
	var env ipeers.Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, rec := range env.Peers {
		if rec.Agent != nil && rec.Agent.PID == pid {
			found = true
			if rec.Agent.Type != "proxy" || rec.Reason != "proxy" || rec.Deliverable {
				t.Errorf("helper row = %+v (agent %+v), want a non-deliverable proxy row", rec, rec.Agent)
			}
		}
	}
	if !found {
		t.Errorf("no row for helper pid %d in %+v", pid, env.Peers)
	}
}

// TestDeliver_AuditCoverage is the audit coverage contract: every refusal
// before the audit insert leaves no row and a warn line naming the
// principal alias; every outcome after it leaves a row with that result.
func TestDeliver_AuditCoverage(t *testing.T) {
	type tc struct {
		name    string
		opts    envOpts
		prepare func(t *testing.T, e *deliverEnv)
		ctx     func(e *deliverEnv) context.Context
		body    func(e *deliverEnv) any
		status  int
		code    string
		audited bool
		after   func(t *testing.T, e *deliverEnv, ae ipeers.APIError)
	}
	hostCtx := func(e *deliverEnv) context.Context { return e.hostCtx() }
	plain := func(e *deliverEnv) any { return e.request() }
	cases := []tc{
		{
			name: "admin_not_allowed", ctx: func(*deliverEnv) context.Context {
				return middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalAdmin})
			},
			body: plain, status: http.StatusForbidden, code: ipeers.ErrAdminNotAllowed,
		},
		{
			name: "host_unverified/empty host id", ctx: func(*deliverEnv) context.Context {
				return middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalHost, Alias: remoteAlias})
			},
			body: plain, status: http.StatusForbidden, code: ipeers.ErrHostUnverified,
		},
		{
			name: "host_unverified/no principal", ctx: func(*deliverEnv) context.Context { return context.Background() },
			body: plain, status: http.StatusForbidden, code: ipeers.ErrHostUnverified,
		},
		{
			name: "host_unverified/alias unknown", ctx: func(*deliverEnv) context.Context {
				return middleware.WithPrincipal(context.Background(), middleware.Principal{Kind: middleware.PrincipalHost, Alias: "ghost", HostID: remoteHostID})
			},
			body: plain, status: http.StatusForbidden, code: ipeers.ErrHostUnverified,
		},
		{
			name: "host_unverified/alias rebound", opts: envOpts{hosts: []config.PeerHost{{Alias: remoteAlias, URL: "http://x", HostID: "other:1", InboundToken: "i"}}},
			ctx: hostCtx, body: plain, status: http.StatusForbidden, code: ipeers.ErrHostUnverified,
		},
		{
			name: "host_unverified/from.host_id mismatch", ctx: hostCtx,
			body:   func(e *deliverEnv) any { r := e.request(); r.From.HostID = "other:1"; return r },
			status: http.StatusForbidden, code: ipeers.ErrHostUnverified,
		},
		{
			name: "deliver_disabled", opts: envOpts{deliver: boolp(false)}, ctx: hostCtx, body: plain,
			status: http.StatusForbidden, code: ipeers.ErrDeliverDisabled,
		},
		{
			name: "rate_limited/host admission", ctx: hostCtx, body: plain,
			prepare: func(t *testing.T, e *deliverEnv) {
				for i := 0; i < ipeers.HostRateLimit; i++ {
					e.m.hostLimit.Allow(remoteHostID)
				}
			},
			status: http.StatusTooManyRequests, code: ipeers.ErrRateLimited,
		},
		{
			name: "bad_request/invalid json", ctx: hostCtx, body: func(*deliverEnv) any { return []byte("{nope") },
			status: http.StatusBadRequest, code: ipeers.ErrBadRequest,
		},
		{
			name: "bad_request/bad msg_id", ctx: hostCtx,
			body:   func(e *deliverEnv) any { r := e.request(); r.MsgID = "not-a-uuid"; return r },
			status: http.StatusBadRequest, code: ipeers.ErrBadRequest,
		},
		{
			name: "text_too_large/65537 bytes", ctx: hostCtx,
			body: func(e *deliverEnv) any {
				r := e.request()
				r.Text = strings.Repeat("x", ipeers.MaxTextBytes+1)
				return r
			},
			status: http.StatusBadRequest, code: ipeers.ErrTextTooLarge,
		},
		{
			name: "bad_mode", ctx: hostCtx,
			body:   func(e *deliverEnv) any { r := e.request(); r.From.DeclaredMode = "yolo"; return r },
			status: http.StatusBadRequest, code: ipeers.ErrBadMode,
		},
		{
			name: "target_gone/wrong pid", ctx: hostCtx,
			body:   func(e *deliverEnv) any { r := e.request(); r.To.PID = targetPID + 1; return r },
			status: http.StatusConflict, code: ipeers.ErrTargetGone, audited: true,
		},
		{
			name: "target_gone/wrong proc_start", ctx: hostCtx,
			body:   func(e *deliverEnv) any { r := e.request(); r.To.ProcStart = "Mon Sep 14 00:00:00 2026"; return r },
			status: http.StatusConflict, code: ipeers.ErrTargetGone, audited: true,
		},
		{
			name: "target_gone/inbox_dead row",
			opts: envOpts{
				noRegistry: true,
				sessions:   []session.SessionInfo{{Code: "s1", Name: "foo"}},
				owners:     map[string]agent.PaneOwner{"s1": {AgentType: "cc", SessionID: targetSessionID}},
			},
			ctx: hostCtx, body: plain, status: http.StatusConflict, code: ipeers.ErrTargetGone, audited: true,
		},
		{
			name: "rate_limited", ctx: hostCtx, body: plain,
			prepare: func(t *testing.T, e *deliverEnv) {
				k := pairKey{From: e.request().From.Key(), To: e.request().To.Key(localHostID)}
				for i := 0; i < ipeers.PairRateLimit; i++ {
					e.m.pairs.Allow(k)
				}
			},
			status: http.StatusTooManyRequests, code: ipeers.ErrRateLimited, audited: true,
		},
		{
			name: "not_ready/inventory unavailable", ctx: hostCtx, body: plain,
			prepare: func(t *testing.T, e *deliverEnv) { e.m.sessions = &fakeSessions{err: errFakeProvider} },
			status:  http.StatusServiceUnavailable, code: ipeers.ErrNotReady, audited: true,
			after: func(t *testing.T, e *deliverEnv, ae ipeers.APIError) {
				if ae.Detail != "inventory unavailable" {
					t.Errorf("detail = %q, want the fixed %q", ae.Detail, "inventory unavailable")
				}
			},
		},
		{
			// v2: the target's tmux session's owner lookup still fails,
			// but the target's own registry entry now gets an entry row
			// (spec §3.4) regardless — so it resolves and delivers rather
			// than answering not_ready (mirrored, RED/GREEN, by
			// TestDeliver_PartialInventoryIsNotReady's first subtest).
			name: "delivered/target resolves via its entry row despite the session owner lookup failing",
			opts: envOpts{noRegistry: true, sessions: []session.SessionInfo{{Code: "s1", Name: "foo"}}},
			ctx:  hostCtx, body: plain,
			prepare: func(t *testing.T, e *deliverEnv) {
				writeRegistryFixture(t, e.regDir, strconv.Itoa(targetPID)+".json", targetRegistryJSONInTmux(e.targetSock, "foo:@1.%1"))
				e.m.owners = &fakeOwners{errs: map[string]error{"s1": errors.New("resolver timeout")}}
			},
			status: http.StatusOK, code: ipeers.ResultDelivered, audited: true,
		},
		{
			name: "not_ready/before sweep", opts: envOpts{fixture: fixtureOpts{noSweep: true}}, ctx: hostCtx, body: plain,
			status: http.StatusServiceUnavailable, code: ipeers.ErrNotReady, audited: true,
		},
		{
			name: "proxy_limit", ctx: hostCtx, body: plain,
			prepare: func(t *testing.T, e *deliverEnv) { fillHelperCap(e.m.helpers) },
			status:  http.StatusServiceUnavailable, code: ipeers.ErrProxyLimit, audited: true,
		},
		{
			name: "proxy_spawn_failed", opts: envOpts{fixture: fixtureOpts{variant: proxyhelpertest.Broken, readyTimeout: 100 * time.Millisecond}},
			ctx: hostCtx, body: plain, status: http.StatusBadGateway, code: ipeers.ErrProxySpawnFailed, audited: true,
			after: func(t *testing.T, e *deliverEnv, ae ipeers.APIError) {
				// The spawn error names local paths (registry dir,
				// proxies.json): the peer gets a fixed detail, the audit
				// row keeps the cause.
				if ae.Detail != "helper could not be started" {
					t.Errorf("detail = %q, want the fixed %q", ae.Detail, "helper could not be started")
				}
				if row := e.onlyRow(); !strings.Contains(row.Error, "did not become ready") {
					t.Errorf("row error = %q, want the spawn cause", row.Error)
				}
			},
		},
		{
			name: "socket_write_failed/listener absent", opts: envOpts{listener: listenAbsent},
			ctx: hostCtx, body: plain, status: http.StatusBadGateway, code: ipeers.ErrSocketWriteFailed, audited: true,
			after: func(t *testing.T, e *deliverEnv, ae ipeers.APIError) {
				if row := e.onlyRow(); row.Error == "" {
					t.Errorf("row error empty, want the dial error")
				}
			},
		},
		{
			name: "delivery_uncertain/listener holds", opts: envOpts{listener: listenReadAndHold, fixture: fixtureOpts{sockWriteTimeout: 100 * time.Millisecond}},
			ctx: hostCtx, body: plain, status: http.StatusOK, code: ipeers.ResultDeliveryUncertain, audited: true,
		},
		{
			name: "delivered", ctx: hostCtx, body: plain,
			status: http.StatusOK, code: ipeers.ResultDelivered, audited: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newDeliverEnv(t, c.opts)
			if c.prepare != nil {
				c.prepare(t, e)
			}
			rr := e.post(c.ctx(e), c.body(e))
			if rr.Code != c.status {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, c.status, rr.Body.String())
			}
			var ae ipeers.APIError
			if c.status == http.StatusOK {
				var resp ipeers.DeliverResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
					t.Fatal(err)
				}
				if resp.Result != c.code {
					t.Fatalf("result = %q, want %q", resp.Result, c.code)
				}
			} else {
				ae = assertRefused(t, rr, c.status, c.code)
				e.assertNoLine()
			}
			rows := e.rows()
			if c.audited {
				if len(rows) != 1 {
					t.Fatalf("audit rows = %d, want 1: %+v", len(rows), rows)
				}
				if rows[0].Result != c.code {
					t.Errorf("row result = %q, want %q (error %q)", rows[0].Result, c.code, rows[0].Error)
				}
				if rows[0].Direction != store.DirIn || rows[0].FromHostID != remoteHostID {
					t.Errorf("row = %+v, want direction in from %s", rows[0], remoteHostID)
				}
			} else {
				if len(rows) != 0 {
					t.Errorf("audit rows = %d, want 0 (refused before the insert): %+v", len(rows), rows)
				}
				var warned bool
				for _, l := range e.f.logs.all() {
					if strings.Contains(l, c.code) && strings.Contains(l, "deliver") {
						warned = true
					}
				}
				if !warned {
					t.Errorf("no warn line naming %q in %v", c.code, e.f.logs.all())
				}
			}
			if c.after != nil {
				c.after(t, e, ae)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Module lifecycle: Start / Stop
// ---------------------------------------------------------------------------

func TestModuleStart_SweepErrorReturned(t *testing.T) {
	e := newDeliverEnv(t, envOpts{fixture: fixtureOpts{noSweep: true}})
	e.m.helpers.proxiesPath = filepath.Join(e.root, "missing", "proxies.json")
	if err := e.m.Start(context.Background()); err == nil {
		t.Fatal("Start: want the Sweep error, got nil")
	}
}

func TestModuleStartStop_JoinsReapLoop(t *testing.T) {
	e := newDeliverEnv(t, envOpts{fixture: fixtureOpts{noSweep: true}})
	if err := e.m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	e.deliverOK(e.request()) // Start swept: Acquire works
	e.recvLine()
	done := make(chan struct{})
	go func() { e.m.Stop(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within 5 s")
	}
	if _, ok := e.m.helpers.FindBySock(helperSockOfLast(t, e)); ok {
		t.Error("helper still registered after Stop")
	}
	if e.f.fake.Stops() != 1 {
		t.Errorf("helper stops = %d, want 1", e.f.fake.Stops())
	}
}

func helperSockOfLast(t *testing.T, e *deliverEnv) string {
	t.Helper()
	return filepath.Join(e.f.sockDir, strconv.Itoa(e.f.fake.LastPID())+".sock")
}

func TestClampMode(t *testing.T) {
	cases := []struct {
		declared string
		allow    bool
		want     string
	}{
		{ipeers.ModeBypass, true, ipeers.ModeBypass},
		{ipeers.ModeBypass, false, ipeers.ModePrompting},
		{ipeers.ModePrompting, true, ipeers.ModePrompting},
		{"", true, ipeers.ModePrompting},
		{"", false, ipeers.ModePrompting},
	}
	for _, c := range cases {
		if got := clampMode(c.declared, c.allow); got != c.want {
			t.Errorf("clampMode(%q, %v) = %q, want %q", c.declared, c.allow, got, c.want)
		}
	}
}
