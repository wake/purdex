package agent

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// PU-3a: every live tmux `hook` frame goes to in-process subscribers from the single place frames are
// broadcast, without ever making the emitter wait.

func notifyModule(t *testing.T) (*Module, chan NotifyEvent) {
	t.Helper()
	m := newTurnEndModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: tmux.NewFakeExecutor()}
	got := make(chan NotifyEvent, 16)
	t.Cleanup(m.SubscribeNotify(func(ev NotifyEvent) { got <- ev }))
	return m, got
}

func wantNotify(t *testing.T, got chan NotifyEvent) NotifyEvent {
	t.Helper()
	select {
	case ev := <-got:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no NotifyEvent")
	}
	return NotifyEvent{}
}

func wantNoNotify(t *testing.T, got chan NotifyEvent) {
	t.Helper()
	select {
	case ev := <-got:
		t.Fatalf("published: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

func frameOf(status string) func(*SessionProjection, error) (agentpkg.NormalizedEvent, bool) {
	return func(*SessionProjection, error) (agentpkg.NormalizedEvent, bool) {
		return agentpkg.NormalizedEvent{AgentType: "cc", Status: status, BroadcastTs: 7}, true
	}
}

// Each source of a tmux `hook` frame publishes it exactly once, with the code and the session's name. Mutation
// gate: drop the publish call in emitSlot → red for every kind.
func TestNotify_EverySourcePublishesOnce(t *testing.T) {
	for _, kind := range []slotKind{kindHook, kindWorker, kindSweep, kindProbe} {
		t.Run(string(kind), func(t *testing.T) {
			m, got := notifyModule(t)
			if !m.emitSessionWith(kind, "code-1", "sess-1", frameOf("waiting")) {
				t.Fatal("the frame did not go out")
			}
			ev := wantNotify(t, got)
			if ev.SessionCode != "code-1" || ev.SessionName != "sess-1" || ev.Event.Status != "waiting" || ev.Event.BroadcastTs != 7 {
				t.Fatalf("event = %+v", ev)
			}
			if ev.Event.Seq == 0 {
				t.Fatalf("the event is not the frame as broadcast (seq %d): it was published before the slot stamped it", ev.Event.Seq)
			}
			wantNoNotify(t, got)
		})
	}
}

// The minimal probe frame passes no session name to the slot (nothing to read), but it is a tmux session's frame:
// it publishes under the probe's session name. Mutation gate: pass "" as notifyName → red.
func TestNotify_MinimalProbeFramePublishesUnderTheProbedSession(t *testing.T) {
	m, got := notifyModule(t)
	m.sessions = fakeProviderWithInstance("inst-1")
	if !m.emitSlot(kindProbe, "code-p", "", "probed", frameOf("idle")) {
		t.Fatal("the frame did not go out")
	}
	if ev := wantNotify(t, got); ev.SessionName != "probed" || ev.SessionCode != "code-p" {
		t.Fatalf("event = %+v", ev)
	}
}

// A session outside tmux is not part of the feed. Mutation gate: drop the kindNonTmux test → red.
func TestNotify_NonTmuxFrameIsNotPublished(t *testing.T) {
	m, got := notifyModule(t)
	if !m.emitSession(kindNonTmux, "n-code", "", func(*SessionProjection) (agentpkg.NormalizedEvent, bool) {
		return agentpkg.NormalizedEvent{AgentType: "cc", Status: "waiting"}, true
	}) {
		t.Fatal("the frame did not go out")
	}
	wantNoNotify(t, got)
	// The kind alone excludes it, even if a caller handed a name (the empty name is a second line of defence).
	m.emitSlot(kindNonTmux, "n-code", "", "named", frameOf("waiting"))
	wantNoNotify(t, got)
}

// A frame that went nowhere (declined by its builder, no code, no bus) is not published either.
func TestNotify_NothingPublishedWhenNothingWasBroadcast(t *testing.T) {
	m, got := notifyModule(t)
	m.emitSessionWith(kindHook, "code-1", "sess-1", func(*SessionProjection, error) (agentpkg.NormalizedEvent, bool) {
		return agentpkg.NormalizedEvent{}, false // declined
	})
	m.emitSessionWith(kindHook, "", "sess-1", frameOf("idle")) // no code: the view syncs, nothing is sent
	wantNoNotify(t, got)
	m.core.Events = nil // no bus
	m.emitSessionWith(kindHook, "code-1", "sess-1", frameOf("idle"))
	wantNoNotify(t, got)
}

// The agent's own session id comes from the projection's top frame. Mutation gate: leave SessionID "" → red.
func TestNotify_CarriesTheAgentsSessionID(t *testing.T) {
	m, got := notifyModule(t)
	m.publishNotify(kindHook, "c", "s", &SessionProjection{TopFrame: &store.Frame{SessionID: "top"}, PrimaryFrame: &store.Frame{SessionID: "primary"}}, agentpkg.NormalizedEvent{})
	if ev := wantNotify(t, got); ev.SessionID != "top" {
		t.Fatalf("session id = %q, want the top frame's", ev.SessionID)
	}
	m.publishNotify(kindHook, "c", "s", &SessionProjection{PrimaryFrame: &store.Frame{SessionID: "primary"}}, agentpkg.NormalizedEvent{})
	if ev := wantNotify(t, got); ev.SessionID != "primary" {
		t.Fatalf("session id = %q, want the primary frame's", ev.SessionID)
	}
	m.publishNotify(kindHook, "c", "s", nil, agentpkg.NormalizedEvent{})
	if ev := wantNotify(t, got); ev.SessionID != "" {
		t.Fatalf("session id = %q, want empty without a projection", ev.SessionID)
	}
}

// A hook the daemon answers 500 before its frame went out publishes nothing (the sender retries it). Mutation
// gate: publish at the handler's entry → red.
func TestNotify_HookAnswered500PublishesNothing(t *testing.T) {
	m, got := notifyModule(t)
	startSession(t, m, "S500")
	if ev := wantNotify(t, got); ev.SessionID != "S500" || ev.SessionName == "" { // a real hook publishes, with the agent's session id
		t.Fatalf("SessionStart event = %+v", ev)
	}
	old := eventsDeleteFn
	eventsDeleteFn = func(*Module, string) error { return errors.New("disk I/O error") }
	defer func() { eventsDeleteFn = old }()
	if code := postStop(t, m, `{"hook_event_name":"Stop","session_id":"S500","last_assistant_message":"x"}`); code != http.StatusInternalServerError {
		t.Fatalf("answered %d, want 500", code)
	}
	wantNoNotify(t, got)
}

// A subscribe-time replay (the Snapshot frame) does not go through the slot, so it is never published.
func TestNotify_SnapshotReplayIsNotPublished(t *testing.T) {
	m, got := notifyModule(t)
	if m.emitSessionWith(kindHook, "code-1", "sess-1", frameOf("idle")) {
		wantNotify(t, got)
	}
	// What a subscriber receives on subscribing: built and sent to that one client, outside emitSlot.
	m.core.Events.Broadcast("code-1", "hook", `{"snapshot":true}`)
	wantNoNotify(t, got)
}

// A subscriber that never reads cannot slow the emitter: the sends are non-blocking, the overflow is counted,
// and another subscriber still gets every frame. Mutation gate: a blocking send → the test times out.
func TestNotify_AStuckSubscriberDoesNotSlowTheEmitter(t *testing.T) {
	m, got := notifyModule(t)
	release := make(chan struct{})
	var entered atomic.Bool
	defer m.SubscribeNotify(func(NotifyEvent) { entered.Store(true); <-release })()
	defer close(release)
	done := make(chan struct{})
	go func() {
		for i := 0; i < notifySubBuffer*3; i++ {
			m.emitSessionWith(kindHook, "code-1", "sess-1", frameOf("running"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the emitter was held up by a stuck subscriber")
	}
	if m.NotifyDropped() == 0 {
		t.Fatal("overflow was not counted")
	}
	n := 0
	for len(got) > 0 {
		<-got
		n++
	}
	if n == 0 {
		t.Fatal("the healthy subscriber got nothing")
	}
}

// A subscriber that panics does not end delivery; unsubscribing twice is harmless and later frames do not reach it.
func TestNotify_PanicIsolatedAndUnsubscribeIdempotent(t *testing.T) {
	m, got := notifyModule(t)
	var calls atomic.Int32
	unsub := m.SubscribeNotify(func(NotifyEvent) { calls.Add(1); panic("boom") })
	m.emitSessionWith(kindHook, "code-1", "sess-1", frameOf("idle"))
	wantNotify(t, got)
	m.emitSessionWith(kindHook, "code-1", "sess-1", frameOf("running"))
	wantNotify(t, got)
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Fatalf("a panicking subscriber was called %d times, want 2 (delivery continues)", calls.Load())
	}
	unsub()
	unsub()
	m.emitSessionWith(kindHook, "code-1", "sess-1", frameOf("idle"))
	wantNotify(t, got)
}
