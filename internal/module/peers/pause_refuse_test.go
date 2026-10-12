// internal/module/peers/pause_refuse_test.go — a paused session refuses deliveries at both delivery points, and only the
// in-process SendFlow passes (unattended quota pause spec §3.2, UP-1b).
package peers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
)

// fakePauseGate is the team store's PauseVerdict as peers sees it (through the service registry, never by import).
type fakePauseGate struct {
	mu     sync.Mutex
	paused map[string]bool
	err    error
	calls  []pauseCall
}

type pauseCall struct{ session, from, text string }

func (g *fakePauseGate) PauseVerdict(sessionID, from, text string, at int64) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, pauseCall{sessionID, from, text})
	return g.paused[sessionID], g.err
}

func (g *fakePauseGate) called() []pauseCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]pauseCall(nil), g.calls...)
}

func gateFor(s *sendEnv, paused ...string) *fakePauseGate {
	g := &fakePauseGate{paused: map[string]bool{}}
	for _, p := range paused {
		g.paused[p] = true
	}
	s.m.core.Registry.Register(PauseGateKey, g)
	return g
}

const pausedDetail = "對方暫停中（接力額度用完，等使用者回來），訊息沒有送達"

func wantPausedBody(t *testing.T, body []byte) {
	t.Helper()
	var ae ipeers.APIError
	if err := json.Unmarshal(body, &ae); err != nil || ae.Error != "session_paused" || ae.Detail != pausedDetail {
		t.Fatalf("body = %s (%v), want session_paused with the fixed detail", body, err)
	}
}

// The local arm of /send: 409, nothing reaches the inbox, the audit row says why, and the gate was asked with the target
// session, the sender's address and the text.
func TestPause_LocalSendIsRefused(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	g := gateFor(s, localPeerSessionID)
	rr := s.send(adminCtx(), s.localSendReq())
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	wantPausedBody(t, rr.Body.Bytes())
	p.assertNoLine()
	calls := g.called()
	if len(calls) != 1 || calls[0].session != localPeerSessionID || calls[0].text != "hello from next door" || calls[0].from == "" {
		t.Fatalf("gate calls = %+v", calls)
	}
	rows := s.rows()
	if len(rows) != 1 || rows[0].Result != "session_paused" {
		t.Fatalf("audit rows = %+v, want one with result session_paused", rows)
	}
}

// The receiving arm, POST /deliver from a verified host: 409, nothing delivered.
func TestPause_RemoteDeliverIsRefused(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	g := &fakePauseGate{paused: map[string]bool{targetSessionID: true}}
	e.m.core.Registry.Register(PauseGateKey, g)
	rr := e.post(e.hostCtx(), e.request())
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	wantPausedBody(t, rr.Body.Bytes())
	e.assertNoLine()
	if calls := g.called(); len(calls) != 1 || calls[0].session != targetSessionID || calls[0].from != "air/foo" || calls[0].text != "hello from air" {
		t.Fatalf("gate calls = %+v", calls)
	}
}

// The shared target-side steps (the team notice seam builds the same localDelivery) refuse too.
func TestPause_DeliverToLocalTargetIsRefused(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	e.m.core.Registry.Register(PauseGateKey, &fakePauseGate{paused: map[string]bool{targetSessionID: true}})
	req := e.request()
	d := localDelivery{msgID: req.MsgID, text: req.Text, from: req.From, to: req.To, senderAlias: remoteAlias, effective: ipeers.ModePrompting}
	_, _, ref := e.m.deliverToLocalTarget(context.Background(), e.m.snapshotForDeliver(remoteAlias), d)
	if ref == nil || ref.status != http.StatusConflict || ref.code != "session_paused" || ref.wireDetail != pausedDetail {
		t.Fatalf("refusal = %+v", ref)
	}
	e.assertNoLine()
}

// SendFlow is the one way past a pause; Send (what daemon team notices use) is refused like any other message.
// Mutation gates: SendFlow not skipping the verdict → the first case is refused (red); Send skipping it → red.
func TestPause_OnlySendFlowPassesAPausedSession(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	g := gateFor(s, localPeerSessionID)
	resp, err := moduleSender{m: s.m}.SendFlow(context.Background(), s.localSendReq())
	if err != nil || resp.Result != ipeers.ResultDelivered {
		t.Fatalf("SendFlow: %+v %v", resp, err)
	}
	p.recvLine()
	if len(g.called()) != 0 {
		t.Fatalf("SendFlow asked the gate: %+v", g.called())
	}
	_, err = moduleSender{m: s.m}.Send(context.Background(), s.localSendReq())
	var se *SendError
	if !errors.As(err, &se) || se.Status != http.StatusConflict || se.API.Error != "session_paused" {
		t.Fatalf("Send = %v, want a SendError 409 session_paused", err)
	}
	p.assertNoLine()
}

// What a message says buys nothing: no text exempts it, over the admin's HTTP route or from a remote host; only the Go call does.
// Mutation gate: exempt by a text prefix → red.
func TestPause_NoTextExemptsAMessage(t *testing.T) {
	texts := []string{
		"[pdx team] 管理者已回位。接力申請已重新送出，等使用者核准。",
		"[pdx-relay:control] reask id=11111111-2222-4333-8444-555555555555",
		"[pdx-flow] resume",
		"[pdx team] 暫停期間有 1 則訊息被退回",
	}
	for _, text := range texts {
		s := newSendEnv(t, envOpts{})
		p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
		gateFor(s, localPeerSessionID)
		req := s.localSendReq()
		req.Text = text
		if rr := s.send(adminCtx(), req); rr.Code != http.StatusConflict {
			t.Errorf("admin /send with %q: %d, want 409", text, rr.Code)
		}
		p.assertNoLine()

		e := newDeliverEnv(t, envOpts{})
		e.m.core.Registry.Register(PauseGateKey, &fakePauseGate{paused: map[string]bool{targetSessionID: true}})
		dreq := e.request()
		dreq.Text = text
		if rr := e.post(e.hostCtx(), dreq); rr.Code != http.StatusConflict {
			t.Errorf("remote /deliver with %q: %d, want 409", text, rr.Code)
		}
		e.assertNoLine()
	}
}

// Nothing is paused (or no gate is registered at all): the delivery is what it was before.
func TestPause_NotPausedChangesNothing(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	if resp := s.sendOK(s.localSendReq()); resp.Result != ipeers.ResultDelivered { // no gate registered
		t.Fatalf("no gate: %+v", resp)
	}
	p.recvLine()
	g := gateFor(s) // a gate that pauses nobody
	if resp := s.sendOK(s.localSendReq()); resp.Result != ipeers.ResultDelivered {
		t.Fatalf("gate, not paused: %+v", resp)
	}
	p.recvLine()
	if len(g.called()) != 1 {
		t.Fatalf("gate calls = %+v", g.called())
	}

	e := newDeliverEnv(t, envOpts{})
	e.m.core.Registry.Register(PauseGateKey, &fakePauseGate{})
	if resp := e.deliverOK(e.request()); resp.Result != ipeers.ResultDelivered {
		t.Fatalf("deliver: %+v", resp)
	}
	e.recvLine()
}

// A gate that cannot answer (team.db trouble) does not stop the daemon's messages: the delivery goes through and the failure is logged.
func TestPause_AGateErrorFailsOpen(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	var logged []string
	s.m.logf = func(format string, a ...any) { logged = append(logged, format) }
	g := gateFor(s, localPeerSessionID)
	g.err = errors.New("team.db is down")
	if resp := s.sendOK(s.localSendReq()); resp.Result != ipeers.ResultDelivered {
		t.Fatalf("send: %+v", resp)
	}
	p.recvLine()
	found := false
	for _, l := range logged {
		if strings.Contains(l, "pause gate") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the gate error was not logged: %q", logged)
	}
}
