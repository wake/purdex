// internal/module/peers/pause_refuse_test.go — a paused session refuses deliveries at both delivery points, and only the
// in-process SendFlow passes (unattended quota pause spec §3.2, UP-1b).
package peers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/core"
	ipeers "github.com/wake/purdex/internal/peers"
)

// fakePauseGate is the team store's PauseVerdict as peers sees it (through the service registry, never by import).
type fakePauseGate struct {
	mu     sync.Mutex
	paused map[string]bool
	detail map[string]string
	err    error
	calls  []pauseCall
}

type pauseCall struct{ session, from, text string }

func (g *fakePauseGate) PauseVerdict(sessionID, from, text string, at int64) (bool, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, pauseCall{sessionID, from, text})
	return g.paused[sessionID], g.detail[sessionID], g.err
}

func (g *fakePauseGate) called() []pauseCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]pauseCall(nil), g.calls...)
}

func gateFor(s *sendEnv, paused ...string) *fakePauseGate {
	g := &fakePauseGate{paused: map[string]bool{}, detail: map[string]string{}}
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

// The same on the receiving arm, and a registered service that is not a gate counts as one that cannot answer.
func TestPause_ADeliverThatCannotBeJudgedIsHeld(t *testing.T) {
	e := newDeliverEnv(t, envOpts{})
	g := &fakePauseGate{err: errors.New("team.db is down")}
	e.m.core.Registry.Register(PauseGateKey, g)
	if rr := e.post(e.hostCtx(), e.request()); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("gate error: %d %s", rr.Code, rr.Body.String())
	}
	e.assertNoLine()

	e2 := newDeliverEnv(t, envOpts{})
	e2.m.core.Registry.Register(PauseGateKey, "not a gate")
	if rr := e2.post(e2.hostCtx(), e2.request()); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("wrong type: %d %s", rr.Code, rr.Body.String())
	}
	e2.assertNoLine()
}

// The shared registry's Sender cannot reach the exemption: SendFlow lives on its own interface, under its own key, which only
// the pause outbox (UP-2a) takes. Mutation gate: SendFlow back on Sender → red.
func TestPause_TheSharedSenderHasNoSendFlow(t *testing.T) {
	if n := reflect.TypeOf((*Sender)(nil)).Elem().NumMethod(); n != 1 {
		t.Fatalf("Sender has %d methods, want only Send", n)
	}
	s := newSendEnv(t, envOpts{})
	reg := core.NewServiceRegistry()
	s.m.registerSenders(reg)
	shared, ok := reg.Get(SenderKey)
	if !ok {
		t.Fatal("no shared sender registered")
	}
	if _, bypass := shared.(FlowSender); bypass {
		t.Fatalf("the shared Sender %T can be asserted to a FlowSender", shared)
	}
	if _, ok := shared.(Sender); !ok {
		t.Fatalf("the shared service %T is not a Sender", shared)
	}
	svc, ok := reg.Get(FlowSenderKey)
	if !ok {
		t.Fatal("no flow sender registered")
	}
	if _, ok := svc.(FlowSender); !ok {
		t.Fatalf("%T is not a FlowSender", svc)
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

// A gate that cannot answer (team.db busy, disk full) holds the message: nothing is delivered, the sender is told to retry
// (503 not_ready), the failure is logged and the audit row says so. Mutation gate: let it through → delivered (red).
func TestPause_AGateThatCannotAnswerHoldsTheMessage(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	p := s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	var logged []string
	s.m.logf = func(format string, a ...any) { logged = append(logged, format) }
	g := gateFor(s, localPeerSessionID)
	g.err = errors.New("team.db is down")
	rr := s.send(adminCtx(), s.localSendReq())
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var ae ipeers.APIError
	if err := json.Unmarshal(rr.Body.Bytes(), &ae); err != nil || ae.Error != ipeers.ErrNotReady {
		t.Fatalf("body = %s", rr.Body.String())
	}
	p.assertNoLine()
	if rows := s.rows(); len(rows) != 1 || rows[0].Result != ipeers.ErrNotReady {
		t.Fatalf("audit rows = %+v", rows)
	}
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

// The pause's flow notices go out from the paused session's own inbox (the daemon has no other): a self-addressed SendFlow is
// delivered to that session, where an ordinary send to oneself is still refused (self_target). Mutation gate: no flow exception ->
// the flow notice is refused (red); the exception for every send -> the ordinary one is delivered (red).
func TestPause_ASessionIsToldByItsOwnInboxThroughSendFlowOnly(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	req := s.localSendReq()
	req.To = localAlias + "/" + ipeers.RefID(targetSessionID)
	req.Text = "[pdx team] 接力額度用完（無人值守），先暫停：手邊這一步收尾後不要開始新的工作，等使用者回來。"
	assertRefused(t, s.send(adminCtx(), req), http.StatusBadRequest, ipeers.ErrSelfTarget)
	resp, err := moduleSender{m: s.m}.SendFlow(context.Background(), req)
	if err != nil || resp.Result != ipeers.ResultDelivered {
		t.Fatalf("SendFlow to itself: %+v %v", resp, err)
	}
	s.recvLine()
}

// The gate's detail rides in the 409 on both paths (RT-2a2, T19); an empty one falls back to the fixed text (the tests above).
// Mutation gates: ignore the gate's detail on the local arm → the first body is the fixed text (red); on the remote arm → the
// second (red).
func TestPause_TheGatesDetailIsTheRefusals(t *testing.T) {
	const custom = "我目前暫停作業，等待使用者確認中"
	s := newSendEnv(t, envOpts{})
	s.addLocalPeer(localPeerName, localPeerSessionID, localPeerPID)
	g := gateFor(s, localPeerSessionID)
	g.detail[localPeerSessionID] = custom
	rr := s.send(adminCtx(), s.localSendReq())
	var ae ipeers.APIError
	if err := json.Unmarshal(rr.Body.Bytes(), &ae); rr.Code != http.StatusConflict || err != nil || ae.Error != "session_paused" || ae.Detail != custom {
		t.Fatalf("local: %d %s (%v), want 409 session_paused with %q", rr.Code, rr.Body.String(), err, custom)
	}
	if rows := s.rows(); len(rows) != 1 || rows[0].Error != custom {
		t.Fatalf("audit rows = %+v, want the custom detail", rows)
	}

	e := newDeliverEnv(t, envOpts{})
	e.m.core.Registry.Register(PauseGateKey, &fakePauseGate{paused: map[string]bool{targetSessionID: true}, detail: map[string]string{targetSessionID: custom}})
	rr = e.post(e.hostCtx(), e.request())
	ae = ipeers.APIError{}
	if err := json.Unmarshal(rr.Body.Bytes(), &ae); rr.Code != http.StatusConflict || err != nil || ae.Detail != custom {
		t.Fatalf("remote: %d %s (%v), want 409 with %q", rr.Code, rr.Body.String(), err, custom)
	}
}

// A team lead is told about a member of another host from its own inbox (SendSelfNotice): delivered to it, where an ordinary send to
// oneself is refused self_target - and the pause still applies to it. Mutation gates: no exception → the notice is refused (red); the
// exception for every send → the ordinary one is delivered (red, TestPause_ASessionIsToldByItsOwnInboxThroughSendFlowOnly); the pause
// skipped → the paused lead takes it (red).
func TestSelfNotice_DeliveredToTheSessionItself_PauseStillApplies(t *testing.T) {
	s := newSendEnv(t, envOpts{})
	req := s.localSendReq()
	req.To = localAlias + "/" + ipeers.RefID(targetSessionID)
	req.Text = "[pdx team] member air26/_abc123 已用 90%"
	assertRefused(t, s.send(adminCtx(), req), http.StatusBadRequest, ipeers.ErrSelfTarget)
	resp, err := moduleSender{m: s.m}.SendSelfNotice(context.Background(), req)
	if err != nil || resp.Result != ipeers.ResultDelivered {
		t.Fatalf("SendSelfNotice to itself: %+v %v", resp, err)
	}
	// the frame offers no way to answer: its reply route is not the session's own socket (a reply would loop back unaudited)
	line := fmt.Sprint(s.recvLine())
	if strings.Contains(line, req.OriginInbox) || !strings.Contains(line, "uds:"+systemNoticeFrom) {
		t.Fatalf("frame = %s, want the reply route %q and not the origin's socket %q", line, systemNoticeFrom, req.OriginInbox)
	}
	// each privileged key shows only its own method: the registered services cannot be asserted to the other's interface
	reg := core.NewServiceRegistry()
	s.m.registerSenders(reg)
	if svc, _ := reg.Get(SelfNoticeSenderKey); svc == nil {
		t.Fatal("no self-notice sender registered")
	} else if _, ok := svc.(FlowSender); ok {
		t.Fatal("the self-notice service can be asserted to FlowSender")
	}
	if svc, _ := reg.Get(FlowSenderKey); svc == nil {
		t.Fatal("no flow sender registered")
	} else if _, ok := svc.(SelfNoticeSender); ok {
		t.Fatal("the flow service can be asserted to SelfNoticeSender")
	}
	gateFor(s, targetSessionID) // the lead is paused: a notice bounces like any message
	var se *SendError
	if _, err := (moduleSender{m: s.m}).SendSelfNotice(context.Background(), req); !errors.As(err, &se) || se.Status != http.StatusConflict {
		t.Fatalf("SendSelfNotice to a paused session: %v, want 409 session_paused", err)
	}
}
