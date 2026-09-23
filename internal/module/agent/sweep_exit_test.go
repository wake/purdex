package agent

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/core"
)

// The sweep half of the exit envelope (agent-last-state spec, review
// decision 7): a root frame cleared for pid_dead / pid_reused carries
// pdx_exit with reason process-dead, built before the delete. Helpers
// (exitOf, seedRootWithIdentity, seedChildFrame) live in exit_test.go.

// --- Sweep ------------------------------------------------------------------

// newExitSweepModule is a sweep module that can broadcast and stamps a known
// generation.
func newExitSweepModule(t *testing.T) (*Module, *core.EventSubscriber) {
	t.Helper()
	m := newSweepTestModule(t)
	m.sessions = fakeProviderWithInstance(exitTestInstance)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: m.tmux}
	sub := m.core.Events.AddTestSubscriber()
	t.Cleanup(func() { m.core.Events.RemoveTestSubscriber(sub) })
	return m, sub
}

func withFixedNow(t *testing.T, at time.Time) {
	t.Helper()
	orig := nowFn
	nowFn = func() time.Time { return at }
	t.Cleanup(func() { nowFn = orig })
}

func TestExit_SweepRootFrame_CarriesProcessDeadEnvelope(t *testing.T) {
	cases := []struct {
		name   string
		alive  bool // false → pid_dead; true with another start time → pid_reused
		reason string
	}{
		{"pid_dead", false, "sweep:pid_dead"},
		{"pid_reused", true, "sweep:pid_reused"},
	}
	for _, agentType := range []string{"cc", "codex", "opencode"} {
		for _, tc := range cases {
			t.Run(agentType+"/"+tc.name, func(t *testing.T) {
				m, sub := newExitSweepModule(t)
				root := seedRootWithIdentity(t, m, "%5", agentType, 99999, "t-old", "S-"+agentType)
				if tc.alive {
					withLivePids(t, map[int]string{99999: "t-new"})
				} else {
					withLivePids(t, map[int]string{})
				}
				fixed := time.UnixMilli(1_788_740_123_456)
				withFixedNow(t, fixed)

				if err := m.sweepOnce(); err != nil {
					t.Fatalf("sweepOnce: %v", err)
				}
				if frames, _ := m.frames.ListByPane("%5"); len(frames) != 0 {
					t.Fatalf("frames = %+v, want the root cleared", frames)
				}
				ev := readSweepNormalizedEvent(t, sub)
				if ev.RawEventName != tc.reason {
					t.Fatalf("RawEventName = %q, want %q", ev.RawEventName, tc.reason)
				}
				e, ok := exitOf(t, ev)
				if !ok {
					t.Fatalf("sweep of a root frame carried no pdx_exit: detail=%+v", ev.Detail)
				}
				want := Exit{
					AgentType: agentType, SessionID: "S-" + agentType, TmuxPaneID: "%5",
					TmuxInstance: exitTestInstance, FrameID: root.FrameID,
					Reason: ExitReasonProcessDead, At: fixed.UnixMilli(),
				}
				if e != want {
					t.Fatalf("envelope = %+v, want %+v", e, want)
				}
			})
		}
	}
}

func TestExit_SweepChildFrame_NoEnvelope(t *testing.T) {
	m, sub := newExitSweepModule(t)
	parent := seedRootWithIdentity(t, m, "%5", "cc", 100, "t100", "P1")
	seedChildFrame(t, m, "%5", "cc", 99999, "t-dead", parent.FrameID)
	withLivePids(t, map[int]string{100: "t100"}) // the parent lives, the child is dead

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].FrameID != parent.FrameID {
		t.Fatalf("frames = %+v, want only the parent", frames)
	}
	ev := readSweepNormalizedEvent(t, sub)
	if ev.RawEventName != "sweep:pid_dead" {
		t.Fatalf("fixture: RawEventName = %q, want sweep:pid_dead", ev.RawEventName)
	}
	if e, ok := exitOf(t, ev); ok {
		t.Fatalf("a child frame's sweep must not carry pdx_exit, got %+v", e)
	}
}

// When the pane's session cannot be resolved (the pane is gone), nothing is
// broadcast — as before this change.
func TestExit_SweepUnresolvedSession_NoBroadcast(t *testing.T) {
	m, sub := newExitSweepModule(t)
	seedRootWithIdentity(t, m, "%9", "cc", 99999, "t-dead", "S1") // %9 belongs to no session
	withLivePids(t, map[int]string{})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	select {
	case msg := <-sub.SendCh():
		t.Fatalf("an unresolved pane must broadcast nothing, got %s", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

// Attacker #2 (#1381): one owner per exit. The SessionEnd hook ended the frame;
// a sweep still holding the pre-delete snapshot must claim nothing and send
// nothing — before the claim it broadcast a second "exit" that could overwrite
// session-end with process-dead.
func TestExit_SweepAfterSessionEnd_SendsNothing(t *testing.T) {
	m, sub := newExitSweepModule(t)
	m.registry.Register(&fakeAgentProvider{typeName: "cc", derive: deriveWithSessionDetail})
	root := seedRootWithIdentity(t, m, "%5", "cc", 200, "t200", "S1")

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"t200","purdex_name":"PdxSessionEnd","raw_event":{"session_id":"S1"},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if e, ok := exitOf(t, readSweepNormalizedEvent(t, sub)); !ok || e.Reason != ExitReasonSessionEnd {
		t.Fatalf("SessionEnd broadcast exit = %+v ok=%v", e, ok)
	}

	if err := m.clearFrame(root, "pid_dead"); err != nil { // the stale snapshot
		t.Fatalf("clearFrame: %v", err)
	}
	select {
	case msg := <-sub.SendCh():
		t.Fatalf("the losing sweep broadcast %s", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

// The race itself, under -race: SessionEnd and the sweep end one frame at the
// same time, many times over — exactly one envelope each time.
func TestExit_SessionEndAndSweepRace_ExactlyOneEnvelope(t *testing.T) {
	for i := 0; i < 40; i++ {
		m, sub := newExitSweepModule(t)
		m.registry.Register(&fakeAgentProvider{typeName: "cc", derive: deriveWithSessionDetail})
		root := seedRootWithIdentity(t, m, "%5", "cc", 200, "t200", "S1")

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"t200","purdex_name":"PdxSessionEnd","raw_event":{"session_id":"S1"},"agent_type":"cc"}`
			m.handleEvent(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body)))
		}()
		go func() {
			defer wg.Done()
			_ = m.clearFrame(root, "pid_dead")
		}()
		wg.Wait()

		exits := 0
	drain:
		for {
			select {
			case raw := <-sub.SendCh():
				if strings.Contains(string(raw), "pdx_exit") {
					exits++
				}
			case <-time.After(30 * time.Millisecond):
				break drain
			}
		}
		if exits != 1 {
			t.Fatalf("iteration %d: %d exit envelopes, want exactly 1", i, exits)
		}
	}
}
