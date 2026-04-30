// internal/module/agent/probe_intent_dispatcher_integration_test.go
//
// W6-3 P2-T7: end-to-end integration test for the ProbeIntent pipeline:
// production codex ProcessDead detector + dispatcher + applyProbeGuards +
// core.Events broadcast. Each test wires a Module with a real
// EventsBroadcaster, drives the codex detector via cross-package test
// seams (codex.SetIsPidAliveFnForTest + codex.SetProcessDeadPollIntervalForTest),
// and asserts the broadcast envelope SPA clients would observe.
//
// Tests in this file are independent — every test owns its own Module +
// subscriber and uses t.Cleanup for teardown. installCodexDetectorSeams
// orders stopAll → seam restoration so detector goroutines exit before the
// test process unwinds the seams (race-clean).
package agent

import (
	"encoding/json"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// wireBroadcastSubscriber installs a core.Core with a fresh EventsBroadcaster
// on the module and registers a test subscriber whose channel the test will
// drain. The subscriber is removed via t.Cleanup so concurrent tests don't
// leak through the package-global Module construction. Returns the
// subscriber for direct channel reads.
func wireBroadcastSubscriber(t *testing.T, m *Module) *core.EventSubscriber {
	t.Helper()
	fakeTmux, ok := m.tmux.(*tmux.FakeExecutor)
	if !ok {
		t.Fatalf("expected fake tmux executor, got %T", m.tmux)
	}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}},
	}
	sub := m.core.Events.AddTestSubscriber()
	t.Cleanup(func() { m.core.Events.RemoveTestSubscriber(sub) })
	return sub
}

// readBroadcastNormalized waits up to dur for one broadcast envelope and
// decodes the inner NormalizedEvent payload. Fatals on timeout or unmarshal
// failure. Caller passes the expected session code so the assertion stays
// near the expectation site.
func readBroadcastNormalized(t *testing.T, sub *core.EventSubscriber, dur time.Duration, wantSessionCode string) agentpkg.NormalizedEvent {
	t.Helper()
	select {
	case msg := <-sub.SendCh():
		var env struct {
			Type    string `json:"type"`
			Session string `json:"session"`
			Value   string `json:"value"`
		}
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatalf("unmarshal envelope: %v (raw: %s)", err, string(msg))
		}
		if env.Type != "hook" {
			t.Fatalf("envelope type = %q, want hook", env.Type)
		}
		if env.Session != wantSessionCode {
			t.Fatalf("envelope session = %q, want %q", env.Session, wantSessionCode)
		}
		var normalized agentpkg.NormalizedEvent
		if err := json.Unmarshal([]byte(env.Value), &normalized); err != nil {
			t.Fatalf("unmarshal normalized: %v (raw: %s)", err, env.Value)
		}
		return normalized
	case <-time.After(dur):
		t.Fatalf("timed out waiting for broadcast")
	}
	return agentpkg.NormalizedEvent{}
}

// expectNoBroadcast asserts no broadcast envelope arrives within dur. Used by
// negative-case tests (idle session, dispatcher tear-down) where any
// emission would indicate a regression.
func expectNoBroadcast(t *testing.T, sub *core.EventSubscriber, dur time.Duration) {
	t.Helper()
	select {
	case msg := <-sub.SendCh():
		t.Fatalf("unexpected broadcast within %v: %s", dur, string(msg))
	case <-time.After(dur):
		// expected: no broadcast
	}
}

// -----------------------------------------------------------------------------
// 1. ProcessDead, pane alive → broadcast error
// -----------------------------------------------------------------------------

// TestIntegration_CodexProcessDead_PaneAlive_BroadcastsError covers the W6-3
// canonical path: codex pid dies while its pane survives. The production
// detector polls (override pid=dead, fake tmux retains the pane id) → emits
// Signal{PaneAlive=true} → applyProbeGuards flips status to error and
// broadcasts a NormalizedEvent on the session WS channel.
func TestIntegration_CodexProcessDead_PaneAlive_BroadcastsError(t *testing.T) {
	m := newDispatcherTestModule(t)
	sub := wireBroadcastSubscriber(t, m)

	// pid dead + pane alive → PaneAlive=true → OnSignal → StatusError.
	installCodexDetectorSeams(t, m, func(int) bool { return false })
	if fake, ok := getFakeTmux(t, m); ok {
		fake.SetPanes([]string{"%5"})
	}
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	got := readBroadcastNormalized(t, sub, 2*time.Second, "code-work")
	if got.Status != string(agentpkg.StatusError) {
		t.Fatalf("normalized.Status = %q, want %q", got.Status, agentpkg.StatusError)
	}
	if got.AgentType != "codex" {
		t.Fatalf("normalized.AgentType = %q, want codex", got.AgentType)
	}
}

// -----------------------------------------------------------------------------
// 2. ProcessDead, pane gone → broadcast clear
// -----------------------------------------------------------------------------

// TestIntegration_CodexProcessDead_PaneGone_BroadcastsClear covers the W6-4
// path: codex pid dies AND its pane has already been closed. The production
// detector emits Signal{PaneAlive=false} → OnSignal → StatusClear → broadcast
// envelope status=clear.
func TestIntegration_CodexProcessDead_PaneGone_BroadcastsClear(t *testing.T) {
	m := newDispatcherTestModule(t)
	sub := wireBroadcastSubscriber(t, m)

	installCodexDetectorSeams(t, m, func(int) bool { return false })
	// pane list intentionally OMITS %5 → HasPane(%5)=false → PaneAlive=false.
	if fake, ok := getFakeTmux(t, m); ok {
		fake.SetPanes([]string{"%9"})
	}
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	got := readBroadcastNormalized(t, sub, 2*time.Second, "code-work")
	if got.Status != string(agentpkg.StatusClear) {
		t.Fatalf("normalized.Status = %q, want %q", got.Status, agentpkg.StatusClear)
	}
	if got.AgentType != "codex" {
		t.Fatalf("normalized.AgentType = %q, want codex", got.AgentType)
	}
}

// -----------------------------------------------------------------------------
// 3. Idle session → ProbeIntent not armed → no broadcast
// -----------------------------------------------------------------------------

// TestIntegration_CodexNormalExit_NoErrorBroadcast pins the gating contract:
// when applyStatus runs with StatusIdle (not in OnEntryStatus = {Running,
// Waiting}), the dispatcher does NOT arm a detector even if pid is dead. No
// broadcast is emitted. Validates the negative case via select+timeout.
func TestIntegration_CodexNormalExit_NoErrorBroadcast(t *testing.T) {
	m := newDispatcherTestModule(t)
	sub := wireBroadcastSubscriber(t, m)

	// Even if a hypothetical detector polled, pid=dead — but lifecycle
	// gating means the detector never starts because Idle ∉ OnEntryStatus.
	installCodexDetectorSeams(t, m, func(int) bool { return false })
	if fake, ok := getFakeTmux(t, m); ok {
		fake.SetPanes([]string{"%5"})
	}
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)
	// Override seedRunningFrame's Running default with Idle: applyStatus
	// reads currentStatus inside applyIntentLifecycle so this is the single
	// source of truth for the lifecycle decision.
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusIdle)

	// Active entry must remain absent.
	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead); ok {
		t.Fatalf("active entry present for idle session, want absent")
	}
	// And no broadcast within a generous window.
	expectNoBroadcast(t, sub, 100*time.Millisecond)
}

// -----------------------------------------------------------------------------
// 4. Active target mismatch (lifecycle case 5) → cancel + re-arm
// -----------------------------------------------------------------------------

// TestIntegration_CodexRestart_ActiveTargetMismatch_ReArms exercises lifecycle
// case 5 with the production codex detector wiring. First arm targets
// senderPID=4242; replacing the top frame with senderPID=5555 (codex restarted
// in the same pane) forces a cancel + re-arm with a fresh generation. The new
// detector then emits PaneAlive=true (pid=dead override) → broadcast carries
// Status=error.
func TestIntegration_CodexRestart_ActiveTargetMismatch_ReArms(t *testing.T) {
	m := newDispatcherTestModule(t)
	sub := wireBroadcastSubscriber(t, m)

	// First arm window: pid alive so the detector keeps polling without
	// emitting; then we flip pid=dead via SetIsPidAliveFnForTest before the
	// re-arm so only the second detector emits.
	t.Cleanup(codex.SetProcessDeadPollIntervalForTest(time.Millisecond))
	aliveRestore := codex.SetIsPidAliveFnForTest(func(int) bool { return true })
	t.Cleanup(aliveRestore)
	t.Cleanup(func() { m.probeIntentDisp.stopAll() })

	if fake, ok := getFakeTmux(t, m); ok {
		fake.SetPanes([]string{"%5"})
	}
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	cur1, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
	if !ok {
		t.Fatalf("first arm did not record active entry")
	}
	if cur1.senderPID != 4242 {
		t.Fatalf("first arm senderPID = %d, want 4242", cur1.senderPID)
	}

	// Simulate codex restart: same pane, new sender PID. Insert a later
	// frame so projection picks it as TopFrame.
	if _, err := m.frames.Upsert(store.Frame{
		FrameID:          "frame-work-codex-restart",
		PaneID:           "%5",
		AgentType:        "codex",
		PID:              5555,
		PPID:             1,
		ProcessStartTime: "Sun Apr 20 02:30:00 2026",
		Status:           agentpkg.StatusRunning,
		StartedAt:        200,
		LastSeenAt:       220,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert restart frame: %v", err)
	}

	// Now flip pid=dead so the new detector emits on its first tick.
	codex.SetIsPidAliveFnForTest(func(int) bool { return false })

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	got := readBroadcastNormalized(t, sub, 2*time.Second, "code-work")
	if got.Status != string(agentpkg.StatusError) {
		t.Fatalf("normalized.Status = %q, want %q after re-arm", got.Status, agentpkg.StatusError)
	}

	// After the new detector emits + applyProbeGuards applies, consumeSignals
	// re-runs applyStatus(error) → case 3 tears down the active entry.
	waitFor(t, 2*time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		return !ok
	}, "active entry torn down after re-armed detector emits")

	// Sanity: the generation changed across the re-arm. We can't read the
	// post-teardown generation directly (entry is gone), but the broadcast
	// proves the re-armed (5555) detector — not the stale (4242) one — was
	// the source.
}

// -----------------------------------------------------------------------------
// 5. Cross-provider switch (codex → cc) → reconcile tears down stale entry
// -----------------------------------------------------------------------------

// TestIntegration_AgentSwitchCodexToCC_OldDetectorTornDown verifies the
// cross-provider reconcile path: applyStatus("work", "codex") arms the
// ProcessDead detector; switching to a "cc" provider that declares NO
// ProbeIntents triggers reconcileSessionActive to drop the stale codex entry,
// even though the cc lifecycle never starts a new detector. The codex
// detector ctx is canceled (no broadcast emitted from the abandoned poll).
func TestIntegration_AgentSwitchCodexToCC_OldDetectorTornDown(t *testing.T) {
	m := newDispatcherTestModule(t)
	sub := wireBroadcastSubscriber(t, m)

	// Keep pid alive while codex is armed; the codex detector then has no
	// reason to emit before reconcile cancels it.
	t.Cleanup(codex.SetProcessDeadPollIntervalForTest(time.Millisecond))
	t.Cleanup(codex.SetIsPidAliveFnForTest(func(int) bool { return true }))
	t.Cleanup(func() { m.probeIntentDisp.stopAll() })

	if fake, ok := getFakeTmux(t, m); ok {
		fake.SetPanes([]string{"%5"})
	}
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead); !ok {
		t.Fatalf("codex detector did not arm")
	}

	// Register a cc provider with no ProbeIntents; switch the session top
	// agent to it. reconcileSessionActive should drop the codex entry.
	m.registry.Register(&fakeAgentProvider{typeName: "cc-noprobes"})
	m.probeIntentDisp.applyStatus("work", "cc-noprobes", agentpkg.StatusRunning)

	waitFor(t, time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		return !ok
	}, "codex active entry torn down after cross-provider switch")

	// No broadcast should arrive — the codex detector was canceled before it
	// could emit, and cc declares no intents.
	expectNoBroadcast(t, sub, 100*time.Millisecond)
}

// -----------------------------------------------------------------------------
// 6. Replay path, real detector, pane gone → broadcast clear
// -----------------------------------------------------------------------------

// TestIntegration_ReplayWithRealDetector_PaneGone_BroadcastsClear simulates a
// daemon-restart recovery (per plan-review P1-2): hydrated state has the codex
// frame.pid stored, the pane has already been closed, and currentStatus is
// Running. replayStatus walks the session and arms the production codex
// detector, which observes pid=dead + pane=gone on its first tick → emits
// Signal{PaneAlive=false} → broadcast Status=clear.
func TestIntegration_ReplayWithRealDetector_PaneGone_BroadcastsClear(t *testing.T) {
	m := newDispatcherTestModule(t)
	sub := wireBroadcastSubscriber(t, m)

	installCodexDetectorSeams(t, m, func(int) bool { return false })
	// Pane list OMITS %5 → HasPane(%5)=false → PaneAlive=false.
	if fake, ok := getFakeTmux(t, m); ok {
		fake.SetPanes([]string{"%9"})
	}
	// Hydrate the way replayFromDB would: frame + currentStatus=Running.
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	// Drive replayStatus directly — equivalent to the Module.Start step
	// after replayFromDB + startSweep have hydrated state.
	m.probeIntentDisp.replayStatus()

	got := readBroadcastNormalized(t, sub, 2*time.Second, "code-work")
	if got.Status != string(agentpkg.StatusClear) {
		t.Fatalf("normalized.Status = %q, want %q (replay pane-gone path)", got.Status, agentpkg.StatusClear)
	}
	if got.AgentType != "codex" {
		t.Fatalf("normalized.AgentType = %q, want codex", got.AgentType)
	}
}

// -----------------------------------------------------------------------------
// 7. Replay path, real detector, pane alive → broadcast error
// -----------------------------------------------------------------------------

// TestIntegration_ReplayWithRealDetector_PaneAlive_BroadcastsError mirrors #6
// but the pane survived the daemon restart. Real detector emits
// Signal{PaneAlive=true} → broadcast Status=error. Together with #6 these
// two tests pin the canonical issue-#698 fix: a daemon restart correctly
// re-evaluates ProbeIntent gating against live state, no hook required.
func TestIntegration_ReplayWithRealDetector_PaneAlive_BroadcastsError(t *testing.T) {
	m := newDispatcherTestModule(t)
	sub := wireBroadcastSubscriber(t, m)

	installCodexDetectorSeams(t, m, func(int) bool { return false })
	if fake, ok := getFakeTmux(t, m); ok {
		fake.SetPanes([]string{"%5"})
	}
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.replayStatus()

	got := readBroadcastNormalized(t, sub, 2*time.Second, "code-work")
	if got.Status != string(agentpkg.StatusError) {
		t.Fatalf("normalized.Status = %q, want %q (replay pane-alive path)", got.Status, agentpkg.StatusError)
	}
	if got.AgentType != "codex" {
		t.Fatalf("normalized.AgentType = %q, want codex", got.AgentType)
	}
}
