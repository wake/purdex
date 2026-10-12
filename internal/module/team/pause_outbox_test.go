// internal/module/team/pause_outbox_test.go — the pause outbox loop (unattended quota pause spec §3.1, §3.3, UP-2a).
package teammod

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/core"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// fakeFlow is peers' FlowSender: it records what it sends and fails the calls the test names.
type fakeFlow struct {
	mu    sync.Mutex
	sent  []ipeers.SendRequest
	errAt map[int]error // by call number (1-based)
	calls int
}

func (g *fakeFlow) SendFlow(ctx context.Context, req ipeers.SendRequest) (ipeers.SendResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if err := g.errAt[g.calls]; err != nil {
		return ipeers.SendResponse{}, err
	}
	g.sent = append(g.sent, req)
	return ipeers.SendResponse{Result: ipeers.ResultDelivered}, nil
}

func (g *fakeFlow) sentTexts() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for _, r := range g.sent {
		out = append(out, r.To+"|"+r.OriginInbox+"|"+r.Text)
	}
	return out
}

// fakeNotifier is the push module's PauseNotifier.
type fakeNotifier struct {
	mu   sync.Mutex
	got  [][2]string
	full []team.PausePush
	done bool
}

func (n *fakeNotifier) NotifyPause(p team.PausePush) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.done {
		n.got = append(n.got, [2]string{p.CollapseID, p.Text})
		n.full = append(n.full, p)
	}
	return n.done
}

func (f *fixture) outboxWorld() (*fakeFlow, *fakeNotifier) {
	f.t.Helper()
	f.m.pauseRetryGap = 0 // passes in a test follow each other at once; the pacing has its own test
	flow, push := &fakeFlow{}, &fakeNotifier{done: true}
	f.core.Registry.Register(peersmod.FlowSenderKey, flow)
	f.core.Registry.Register(team.PauseNotifyKey, push)
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Address: "h/_abc123", PID: 10, Tmux: "mt0:@1.%1"})
	f.origins.show(team.Origin{SessionID: "sid-2", Ref: "_def456", Address: "h/_def456", PID: 20})
	return flow, push
}

func (f *fixture) queue(sid, kind, text string, at int64) {
	f.t.Helper()
	if _, err := f.m.store.db.Exec(`INSERT INTO pause_outbox (session_id, kind, text, created_at) VALUES (?, ?, ?, ?)`, sid, kind, text, at); err != nil {
		f.t.Fatal(err)
	}
}

// The rows go out in seq order from the session's own inbox to its live address, and a sent row is deleted; the push goes to the push
// module with the collapse id pause:<session>:<since>.
func TestPauseOutbox_SendsInOrderAndDeletes(t *testing.T) {
	f := newFixture(t)
	flow, push := f.outboxWorld()
	f.queue("sid-1", team.PauseOutboxNotice, "first", 500)
	f.queue("sid-2", team.PauseOutboxNotice, "second", 500)
	f.queue("sid-1", team.PauseOutboxPush, "boss 已暫停：接力額度用完，等你接手", 500)
	f.m.drainPauseOutbox()
	got := flow.sentTexts()
	if len(got) != 2 || got[0] != "h/_abc123|/tmp/10.sock|first" || got[1] != "h/_def456|/tmp/20.sock|second" {
		t.Fatalf("flow sends = %q", got)
	}
	if len(push.got) != 1 || push.got[0] != [2]string{"pause:sid-1:500", "boss 已暫停：接力額度用完，等你接手"} {
		t.Fatalf("push = %q", push.got)
	}
	// the push names the session and the tmux session it runs in (the phone jumps to it), and when the pause began
	if p := push.full[0]; p.SessionID != "sid-1" || p.TmuxSession != "mt0" || p.SinceMs != 500 {
		t.Fatalf("push request = %+v", p)
	}
	if rows := f.outbox(); len(rows) != 0 {
		t.Fatalf("outbox after the pass = %+v, want empty", rows)
	}
}

// A head that fails blocks everything behind it (FIFO), is retried first on the next pass, and counts its attempts.
// Mutation gate: skip a failed head and go on → the second row is sent ahead of the first (red).
func TestPauseOutbox_AFailedHeadBlocksTheRowsBehindIt(t *testing.T) {
	f := newFixture(t)
	flow, _ := f.outboxWorld()
	flow.errAt = map[int]error{1: errors.New("socket write failed")}
	f.queue("sid-1", team.PauseOutboxNotice, "first", 500)
	f.queue("sid-2", team.PauseOutboxNotice, "second", 500)
	f.m.drainPauseOutbox()
	if len(flow.sentTexts()) != 0 {
		t.Fatalf("a row went out behind a failed head: %q", flow.sentTexts())
	}
	rows := f.outbox()
	if len(rows) != 2 || rows[0].Attempts != 1 || rows[1].Attempts != 0 {
		t.Fatalf("outbox = %+v, want both kept, the head with one attempt", rows)
	}
	f.m.drainPauseOutbox() // the second pass: the head goes first, then the rest
	got := flow.sentTexts()
	if len(got) != 2 || got[0] != "h/_abc123|/tmp/10.sock|first" || got[1] != "h/_def456|/tmp/20.sock|second" {
		t.Fatalf("flow sends = %q", got)
	}
	if len(f.outbox()) != 0 {
		t.Fatalf("outbox = %+v", f.outbox())
	}
}

// Sent, then the process dies before the row is deleted: the next pass sends it again — at least once, never lost.
// Mutation gate: delete the row before sending → the crash loses it (red).
func TestPauseOutbox_ACrashAfterTheSendBeforeTheDeleteResends(t *testing.T) {
	f := newFixture(t)
	flow, _ := f.outboxWorld()
	f.queue("sid-1", team.PauseOutboxNotice, "once", 500)
	f.m.pauseOutboxCrash = func(int64) bool { return true }
	f.m.drainPauseOutbox()
	f.m.pauseOutboxCrash = nil
	if len(flow.sentTexts()) != 1 || len(f.outbox()) != 1 {
		t.Fatalf("after the crash: sent %q, outbox %+v", flow.sentTexts(), f.outbox())
	}
	f.m.drainPauseOutbox()
	if got := flow.sentTexts(); len(got) != 2 || got[0] != got[1] {
		t.Fatalf("flow sends = %q, want the same notice twice", got)
	}
	if len(f.outbox()) != 0 {
		t.Fatalf("outbox = %+v", f.outbox())
	}
}

// A session that is no longer there has its rows dropped, and the rows behind them still go.
func TestPauseOutbox_ATargetThatIsGoneIsDropped(t *testing.T) {
	f := newFixture(t)
	flow, _ := f.outboxWorld()
	f.queue("sid-gone", team.PauseOutboxNotice, "nobody", 500) // not in the registry: no inbox
	f.queue("sid-2", team.PauseOutboxNotice, "second", 500)
	f.m.drainPauseOutbox()
	if got := flow.sentTexts(); len(got) != 1 || got[0] != "h/_def456|/tmp/20.sock|second" {
		t.Fatalf("flow sends = %q", got)
	}
	if len(f.outbox()) != 0 {
		t.Fatalf("outbox = %+v", f.outbox())
	}
	// the flow sender itself answering that the target is gone drops the row too
	flow.errAt = map[int]error{2: &peersmod.SendError{Status: http.StatusNotFound, API: ipeers.APIError{Error: ipeers.ErrPeerNotFound}}}
	f.queue("sid-1", team.PauseOutboxNotice, "late", 600)
	f.m.drainPauseOutbox()
	if len(f.outbox()) != 0 {
		t.Fatalf("outbox = %+v, want the row for the vanished target dropped", f.outbox())
	}
}

// A push the push side is not ready for stays (and blocks the rows behind it); no push module at all drops it.
func TestPauseOutbox_APushWaitsForThePushModule(t *testing.T) {
	f := newFixture(t)
	_, push := f.outboxWorld()
	push.done = false
	f.queue("sid-1", team.PauseOutboxPush, "text", 500)
	f.m.drainPauseOutbox()
	if rows := f.outbox(); len(rows) != 1 || rows[0].Attempts != 1 {
		t.Fatalf("outbox = %+v, want the push kept", rows)
	}
	push.done = true
	f.m.drainPauseOutbox()
	if len(f.outbox()) != 0 || len(push.got) != 1 {
		t.Fatalf("outbox %+v, pushes %q", f.outbox(), push.got)
	}
	// no push module registered at all (a daemon without one): the row is dropped, not kept for ever
	g := newFixture(t)
	g.core.Registry.Register(peersmod.FlowSenderKey, &fakeFlow{})
	g.queue("sid-1", team.PauseOutboxPush, "text", 500)
	g.m.drainPauseOutbox()
	if len(g.outbox()) != 0 {
		t.Fatalf("outbox = %+v", g.outbox())
	}
}

// With no flow sender registered nothing can be sent: the rows stay.
func TestPauseOutbox_NoFlowSenderKeepsTheRows(t *testing.T) {
	f := newFixture(t)
	f.queue("sid-1", team.PauseOutboxNotice, "text", 500)
	f.m.drainPauseOutbox()
	if len(f.outbox()) != 1 {
		t.Fatalf("outbox = %+v", f.outbox())
	}
}

// End to end through the module: a begin that pauses wakes the loop, and the notice and the push go out.
func TestPauseOutbox_ABeginThatPausesIsSentByTheRunningLoop(t *testing.T) {
	f := newFixture(t)
	flow, push := f.outboxWorld()
	f.quotaWorld()
	f.origins.show(team.Origin{SessionID: "sid-1", Ref: "_abc123", Title: "boss", Address: "h/boss", PID: 10})
	if err := f.m.Start(context.Background()); err != nil {
		t.Skipf("Start needs the full module set: %v", err)
	}
	if code, _ := f.beginRaw("sid-1"); code != http.StatusConflict {
		t.Fatal(code)
	}
	waitFor(t, func() bool { return len(flow.sentTexts()) == 1 && len(push.got) == 1 && len(f.outbox()) == 0 })
}

// stubPush is a push module that is mounted (Init done, ready) but has not started, so it has not published its notifier yet.
type stubPush struct{ ready bool }

func (stubPush) Name() string                  { return "push" }
func (stubPush) Dependencies() []string        { return nil }
func (stubPush) Init(*core.Core) error         { return nil }
func (stubPush) RegisterRoutes(*http.ServeMux) {}
func (stubPush) Start(context.Context) error   { return nil }
func (stubPush) Stop(context.Context) error    { return nil }
func (s stubPush) Status() map[string]any      { return map[string]any{"ready": s.ready} }

// Codex R1 (P2): at a restart the team module's outbox starts BEFORE the push module (push depends on team) and finds no notifier
// yet; a push module that is mounted and ready is only late, so the row stays. One that soft-failed (not ready) never will publish:
// its row is dropped. Mutation gate: drop whenever the notifier is missing → the persisted push is lost at every restart (red).
func TestPauseOutbox_APushRowSurvivesTheStartupOrder(t *testing.T) {
	f := newFixture(t)
	f.m.pauseRetryGap = 0
	f.core.AddModule(stubPush{ready: true})
	f.queue("sid-1", team.PauseOutboxPush, "text", 500)
	f.m.drainPauseOutbox()
	if rows := f.outbox(); len(rows) != 1 {
		t.Fatalf("outbox = %+v, want the push kept until the push module has started", rows)
	}
	push := &fakeNotifier{done: true}
	f.core.Registry.Register(team.PauseNotifyKey, push) // the push module started
	f.m.drainPauseOutbox()
	if len(f.outbox()) != 0 || len(push.got) != 1 {
		t.Fatalf("outbox %+v, pushes %q", f.outbox(), push.got)
	}

	g := newFixture(t)
	g.core.AddModule(stubPush{ready: false}) // soft-failed: it will never publish
	g.queue("sid-1", team.PauseOutboxPush, "text", 500)
	g.m.drainPauseOutbox()
	if len(g.outbox()) != 0 {
		t.Fatalf("outbox = %+v, want the push dropped for a module that is not ready", g.outbox())
	}
}

// Codex attack (high) and critic: a head that can never be sent must not hold every other session's notices for ever, and must not
// be thrown away either (at least once). FIFO is kept, but a row that failed pauseOutboxMaxAttempts passes is dead-lettered: it
// stays in the table (logged, recoverable), is not tried again, and the rows behind it go.
// Mutation gates: no dead-letter → the rows behind a poison head are never sent (red); deleting the dead row → it is gone (red).
func TestPauseOutbox_APoisonHeadIsDeadLetteredNotDropped(t *testing.T) {
	f := newFixture(t)
	f.m.pauseRetryGap = 0
	flow, _ := f.outboxWorld()
	flow.errAt = map[int]error{}
	for i := 1; i <= pauseOutboxMaxAttempts; i++ {
		flow.errAt[i] = errors.New("socket write failed") // the head fails every time it is tried
	}
	f.queue("sid-1", team.PauseOutboxNotice, "poison", 500)
	f.queue("sid-2", team.PauseOutboxNotice, "behind", 500)
	for i := 0; i < pauseOutboxMaxAttempts-1; i++ {
		f.m.drainPauseOutbox()
		if len(flow.sentTexts()) != 0 || len(f.outbox()) != 2 {
			t.Fatalf("pass %d: sent %q, outbox %+v, want the head retried and nothing behind it sent", i+1, flow.sentTexts(), f.outbox())
		}
	}
	f.m.drainPauseOutbox() // the last attempt: the head is dead-lettered and the row behind it goes in the same pass
	got := flow.sentTexts()
	rows := f.outbox()
	if len(got) != 1 || got[0] != "h/_def456|/tmp/20.sock|behind" {
		t.Fatalf("after the head gave up: sent %q", got)
	}
	if len(rows) != 1 || rows[0].Session != "sid-1" || rows[0].Attempts != pauseOutboxMaxAttempts {
		t.Fatalf("outbox = %+v, want the dead row kept with its attempts", rows)
	}
	f.m.pauseRetryMu.Lock()
	leaked := len(f.m.pauseRetryAt)
	f.m.pauseRetryMu.Unlock()
	if leaked != 0 {
		t.Fatalf("%d retry-state entries are left for dead or sent rows (they would accumulate)", leaked)
	}
	f.m.drainPauseOutbox() // a dead row is not tried again
	if flow.calls != pauseOutboxMaxAttempts+1 {
		t.Fatalf("flow calls = %d, want %d (the dead row must not be retried)", flow.calls, pauseOutboxMaxAttempts+1)
	}
}

// A failing head is retried at the pace of the timer, not at the pace of kicks: a flood of pauses cannot burn its attempts in a
// second. Mutation gate: no pacing → the second pass retries at once (red).
func TestPauseOutbox_AFailingHeadIsRetriedAtTheTimersPace(t *testing.T) {
	f := newFixture(t)
	flow, _ := f.outboxWorld()
	f.m.pauseRetryGap = time.Hour
	flow.errAt = map[int]error{1: errors.New("socket write failed")}
	f.queue("sid-1", team.PauseOutboxNotice, "first", 500)
	f.m.drainPauseOutbox() // fails
	f.m.drainPauseOutbox() // a kick-driven pass inside the gap: the head waits
	if rows := f.outbox(); len(rows) != 1 || rows[0].Attempts != 1 || flow.calls != 1 {
		t.Fatalf("outbox %+v, flow calls %d, want one attempt so far", rows, flow.calls)
	}
	f.m.pauseRetryMu.Lock()
	f.m.pauseRetryAt[1] = time.Now().Add(-time.Second) // the gap has passed
	f.m.pauseRetryMu.Unlock()
	f.m.drainPauseOutbox()
	if len(f.outbox()) != 0 || len(flow.sentTexts()) != 1 {
		t.Fatalf("outbox %+v, sent %q", f.outbox(), flow.sentTexts())
	}
}
