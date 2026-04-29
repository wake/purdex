package agent

import (
	"sync/atomic"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
)

// W6-3 P1-T2: direct unit tests for the extracted applyProbeGuards free
// function. The seven cases cover each layered guard independently
// (StaleCheck fast-path, graceWindow, Mapping, ErrorGuard, transition gate,
// final critical-section re-check) plus a happy-path mutate+broadcast
// assertion. ScreenChange path regression coverage lives in
// TestScreenChangePath_RegressionMatrix below; existing OR* / FX* / CC*
// tests still hit the path via the orchestrator callback so behaviour parity
// is double-checked.
//
// Helpers --------------------------------------------------------------

// staleAlways / staleNever are convenience StaleCheck strategies for tests
// where the fast-path / re-check semantics are independent from module state.
func staleAlways(*Module) bool { return true }
func staleNever(*Module) bool  { return false }

// mappingTo returns a Mapping callback that always emits the given status.
// nil status maps to "" which the dispatcher treats as drop.
func mappingTo(status agentpkg.Status) func(agentpkg.Signal) agentpkg.Status {
	return func(agentpkg.Signal) agentpkg.Status { return status }
}

// applyGuardsTestModule wires a Module ready for direct applyProbeGuards
// invocation. Mirrors orchTestModule but does not require activeWatchers /
// the prober fake — the StaleCheck closure provides whatever stale semantics
// the test wants, decoupling these tests from activeWatchers state.
func applyGuardsTestModule(t *testing.T) *Module {
	t.Helper()
	m, _, _ := orchTestModule(t)
	return m
}

// TestApplyProbeGuards_StaleCheckGuard_FastPath — StaleCheck closure returns
// false on the early fast-path; applyProbeGuards must drop the signal,
// return applied=false, and not broadcast.
func TestApplyProbeGuards_StaleCheckGuard_FastPath(t *testing.T) {
	m := applyGuardsTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusIdle, 700)
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()

	before := snapshotMetrics()
	applied := applyProbeGuards(m, probeGuardArgs{
		Session:    "work",
		AgentType:  "cc",
		Reason:     "probe:activity",
		Mapping:    mappingTo(agentpkg.StatusRunning),
		StaleCheck: staleNever,
	})
	after := snapshotMetrics()

	if applied {
		t.Fatalf("applied = true, want false (StaleCheck rejected)")
	}
	if got := after.screenEvent - before.screenEvent; got != 0 {
		t.Fatalf("screenEvent delta = %d, want 0", got)
	}
	m.mu.Lock()
	status := m.currentStatus["work"]
	m.mu.Unlock()
	if status != agentpkg.StatusIdle {
		t.Fatalf("currentStatus[work] = %q, want StatusIdle (untouched)", status)
	}
}

// TestApplyProbeGuards_GraceWindow — recordHookAt within probeGraceWindow
// suppresses the signal; counter increments; applied=false.
func TestApplyProbeGuards_GraceWindow(t *testing.T) {
	m := applyGuardsTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusWaiting, 701)
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusWaiting
	m.mu.Unlock()
	m.probeOrch.recordHookAt("work")

	before := snapshotMetrics()
	applied := applyProbeGuards(m, probeGuardArgs{
		Session:    "work",
		AgentType:  "cc",
		Reason:     "probe:activity",
		Mapping:    mappingTo(agentpkg.StatusRunning),
		StaleCheck: staleAlways,
	})
	after := snapshotMetrics()

	if applied {
		t.Fatalf("applied = true, want false (graceWindow active)")
	}
	if got := after.graceWindowSup - before.graceWindowSup; got != 1 {
		t.Fatalf("graceWindowSup delta = %d, want 1", got)
	}
	if got := after.screenEvent - before.screenEvent; got != 0 {
		t.Fatalf("screenEvent delta = %d, want 0 (suppressed)", got)
	}
	m.mu.Lock()
	status := m.currentStatus["work"]
	m.mu.Unlock()
	if status != agentpkg.StatusWaiting {
		t.Fatalf("currentStatus[work] = %q, want StatusWaiting (untouched)", status)
	}
}

// TestApplyProbeGuards_Mapping_NewStatusEmpty_Drops — Mapping returns "" →
// dispatcher treats as drop; no mutate, no broadcast.
func TestApplyProbeGuards_Mapping_NewStatusEmpty_Drops(t *testing.T) {
	m := applyGuardsTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusRunning, 702)
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusRunning
	m.mu.Unlock()

	before := snapshotMetrics()
	applied := applyProbeGuards(m, probeGuardArgs{
		Session:    "work",
		AgentType:  "cc",
		Reason:     "probe-intent:test_drop",
		Mapping:    mappingTo(""), // empty → drop signal
		StaleCheck: staleAlways,
	})
	after := snapshotMetrics()

	if applied {
		t.Fatalf("applied = true, want false (empty mapping → drop)")
	}
	if got := after.screenEvent - before.screenEvent; got != 0 {
		t.Fatalf("screenEvent delta = %d, want 0", got)
	}
	m.mu.Lock()
	status := m.currentStatus["work"]
	m.mu.Unlock()
	if status != agentpkg.StatusRunning {
		t.Fatalf("currentStatus[work] = %q, want StatusRunning (untouched)", status)
	}
}

// TestApplyProbeGuards_ErrorGuard — currentStatus = StatusError; probe must
// never overwrite (recovery-only contract). applied=false; no broadcast.
func TestApplyProbeGuards_ErrorGuard(t *testing.T) {
	m := applyGuardsTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusError, 703)
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusError
	m.mu.Unlock()

	before := snapshotMetrics()
	applied := applyProbeGuards(m, probeGuardArgs{
		Session:    "work",
		AgentType:  "cc",
		Reason:     "probe:activity",
		Mapping:    mappingTo(agentpkg.StatusIdle),
		StaleCheck: staleAlways,
	})
	after := snapshotMetrics()

	if applied {
		t.Fatalf("applied = true, want false (ErrorGuard blocks)")
	}
	if got := after.screenEvent - before.screenEvent; got != 0 {
		t.Fatalf("screenEvent delta = %d, want 0", got)
	}
	m.mu.Lock()
	status := m.currentStatus["work"]
	m.mu.Unlock()
	if status != agentpkg.StatusError {
		t.Fatalf("currentStatus[work] = %q, want StatusError preserved", status)
	}
}

// TestApplyProbeGuards_TransitionGate — currentStatus already equals new
// mapped status; transition gate drops the duplicate; applied=false.
func TestApplyProbeGuards_TransitionGate(t *testing.T) {
	m := applyGuardsTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusRunning, 704)
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusRunning
	m.mu.Unlock()

	before := snapshotMetrics()
	applied := applyProbeGuards(m, probeGuardArgs{
		Session:    "work",
		AgentType:  "cc",
		Reason:     "probe:activity",
		Mapping:    mappingTo(agentpkg.StatusRunning),
		StaleCheck: staleAlways,
	})
	after := snapshotMetrics()

	if applied {
		t.Fatalf("applied = true, want false (transition gate dedup)")
	}
	if got := after.screenEvent - before.screenEvent; got != 0 {
		t.Fatalf("screenEvent delta = %d, want 0", got)
	}
}

// TestApplyProbeGuards_HappyPath_MutatesAndBroadcasts — every guard passes;
// currentStatus mutates, broadcast fires, MetricProbeScreenEvent +1, return
// applied=true.
func TestApplyProbeGuards_HappyPath_MutatesAndBroadcasts(t *testing.T) {
	m := applyGuardsTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusIdle, 705)
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	before := snapshotMetrics()
	applied := applyProbeGuards(m, probeGuardArgs{
		Session:    "work",
		AgentType:  "cc",
		Reason:     "probe:activity",
		Mapping:    mappingTo(agentpkg.StatusRunning),
		StaleCheck: staleAlways,
	})
	after := snapshotMetrics()

	if !applied {
		t.Fatalf("applied = false, want true (happy path)")
	}
	if got := after.screenEvent - before.screenEvent; got != 1 {
		t.Fatalf("screenEvent delta = %d, want 1", got)
	}
	m.mu.Lock()
	status := m.currentStatus["work"]
	m.mu.Unlock()
	if status != agentpkg.StatusRunning {
		t.Fatalf("currentStatus[work] = %q, want StatusRunning", status)
	}
	select {
	case <-sub.SendCh():
		// ok
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for broadcast")
	}
}

// TestApplyProbeGuards_FinalCriticalSectionReCheck — StaleCheck strategy
// returns true on the early fast-path but flips to false by the time the
// final critical-section re-check runs (simulated via a side-effecting
// closure fired by interruptBeforeFinalLockFn-equivalent — here we use the
// counter inside the StaleCheck closure itself). applied=false; no broadcast.
func TestApplyProbeGuards_FinalCriticalSectionReCheck(t *testing.T) {
	m := applyGuardsTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusIdle, 706)
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()

	// First call: pass (early fast-path); subsequent calls: fail (final
	// critical-section re-check). applyProbeGuards must consult StaleCheck
	// twice — once in step 1 (early fast-path) and again in step 4 (atomic
	// re-check), per spec §5.2.
	var calls int32
	stale := func(*Module) bool {
		n := atomic.AddInt32(&calls, 1)
		return n == 1
	}

	before := snapshotMetrics()
	applied := applyProbeGuards(m, probeGuardArgs{
		Session:    "work",
		AgentType:  "cc",
		Reason:     "probe:activity",
		Mapping:    mappingTo(agentpkg.StatusRunning),
		StaleCheck: stale,
	})
	after := snapshotMetrics()

	if applied {
		t.Fatalf("applied = true, want false (final re-check failed)")
	}
	if got := after.screenEvent - before.screenEvent; got != 0 {
		t.Fatalf("screenEvent delta = %d, want 0 (no broadcast on re-check fail)", got)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("StaleCheck call count = %d, want 2 (early fast-path + final re-check)", got)
	}
	m.mu.Lock()
	status := m.currentStatus["work"]
	m.mu.Unlock()
	if status != agentpkg.StatusIdle {
		t.Fatalf("currentStatus[work] = %q, want StatusIdle (no transition)", status)
	}
}

// TestScreenChangePath_RegressionMatrix — table-driven regression for the
// ScreenChange caller adaptor (interpretScreenEvent). Each case asserts
// observable output (broadcast / mutation / metric delta) matches the
// pre-extraction baseline. Mirrors the existing OR* / FX* test invariants
// in a single table form per plan P1-T2.
func TestScreenChangePath_RegressionMatrix(t *testing.T) {
	type setupFn func(t *testing.T, m *Module, fake *fakeTmuxCapture)

	cases := []struct {
		name            string
		setup           setupFn
		event           probe.ScreenChangeEvent
		expectMutation  bool
		expectBroadcast bool
		// expectScreenEventDelta is the delta on MetricProbeScreenEvent.
		// expectGraceWindowDelta is the delta on MetricProbeGraceWindowSuppressed.
		expectScreenEventDelta int64
		expectGraceWindowDelta int64
		// finalStatus is the expected currentStatus[work] after the call.
		finalStatus agentpkg.Status
	}{
		{
			name: "stale_active_watchers_drop",
			setup: func(t *testing.T, m *Module, _ *fakeTmuxCapture) {
				seedOrchFrame(t, m, agentpkg.StatusIdle, 800)
				// activeWatchers absent — stale-callback fast-path drops.
				m.mu.Lock()
				m.currentStatus["work"] = agentpkg.StatusIdle
				m.mu.Unlock()
			},
			event:                  probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()},
			expectMutation:         false,
			expectBroadcast:        false,
			expectScreenEventDelta: 0,
			expectGraceWindowDelta: 0,
			finalStatus:            agentpkg.StatusIdle,
		},
		{
			name: "grace_window_drop",
			setup: func(t *testing.T, m *Module, _ *fakeTmuxCapture) {
				seedOrchFrame(t, m, agentpkg.StatusWaiting, 801)
				m.mu.Lock()
				m.activeWatchers["work"] = "cc"
				m.currentStatus["work"] = agentpkg.StatusWaiting
				m.mu.Unlock()
				m.probeOrch.recordHookAt("work")
			},
			event:                  probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()},
			expectMutation:         false,
			expectBroadcast:        false,
			expectScreenEventDelta: 0,
			expectGraceWindowDelta: 1,
			finalStatus:            agentpkg.StatusWaiting,
		},
		{
			name: "error_guard_drop",
			setup: func(t *testing.T, m *Module, fake *fakeTmuxCapture) {
				seedOrchFrame(t, m, agentpkg.StatusError, 802)
				m.mu.Lock()
				m.activeWatchers["work"] = "cc"
				m.currentStatus["work"] = agentpkg.StatusError
				m.mu.Unlock()
				// ScreenStable would map to Idle; ErrorGuard blocks.
				fake.SetPaneContent("work:", "user typed something normal")
			},
			event:                  probe.ScreenChangeEvent{Kind: probe.ScreenStable, Target: "work:", OccurredAt: time.Now()},
			expectMutation:         false,
			expectBroadcast:        false,
			expectScreenEventDelta: 0,
			expectGraceWindowDelta: 0,
			finalStatus:            agentpkg.StatusError,
		},
		{
			name: "transition_gate_drop",
			setup: func(t *testing.T, m *Module, _ *fakeTmuxCapture) {
				// currentStatus already Running, ScreenChanged maps to Running.
				seedOrchFrame(t, m, agentpkg.StatusRunning, 803)
				m.mu.Lock()
				m.activeWatchers["work"] = "cc"
				m.currentStatus["work"] = agentpkg.StatusRunning
				m.mu.Unlock()
			},
			event:                  probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()},
			expectMutation:         false,
			expectBroadcast:        false,
			expectScreenEventDelta: 0,
			expectGraceWindowDelta: 0,
			finalStatus:            agentpkg.StatusRunning,
		},
		{
			name: "happy_path_apply",
			setup: func(t *testing.T, m *Module, _ *fakeTmuxCapture) {
				seedOrchFrame(t, m, agentpkg.StatusIdle, 804)
				m.mu.Lock()
				m.activeWatchers["work"] = "cc"
				m.currentStatus["work"] = agentpkg.StatusIdle
				m.mu.Unlock()
			},
			event:                  probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()},
			expectMutation:         true,
			expectBroadcast:        true,
			expectScreenEventDelta: 1,
			expectGraceWindowDelta: 0,
			finalStatus:            agentpkg.StatusRunning,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, fake := orchTestModule(t)
			tc.setup(t, m, fake)

			sub := m.core.Events.AddTestSubscriber()
			defer m.core.Events.RemoveTestSubscriber(sub)

			before := snapshotMetrics()
			cb := m.probeOrch.makeCallback("work", "cc")
			cb(tc.event)
			after := snapshotMetrics()

			if got := after.screenEvent - before.screenEvent; got != tc.expectScreenEventDelta {
				t.Fatalf("screenEvent delta = %d, want %d", got, tc.expectScreenEventDelta)
			}
			if got := after.graceWindowSup - before.graceWindowSup; got != tc.expectGraceWindowDelta {
				t.Fatalf("graceWindowSup delta = %d, want %d", got, tc.expectGraceWindowDelta)
			}

			m.mu.Lock()
			status := m.currentStatus["work"]
			m.mu.Unlock()
			if status != tc.finalStatus {
				t.Fatalf("currentStatus[work] = %q, want %q", status, tc.finalStatus)
			}

			// Broadcast assertion is best-effort: drain any pending message
			// non-blockingly. Subscriber channel buffer is sized for tests.
			gotBroadcast := false
			select {
			case <-sub.SendCh():
				gotBroadcast = true
			case <-time.After(50 * time.Millisecond):
			}
			if gotBroadcast != tc.expectBroadcast {
				t.Fatalf("broadcast observed = %v, want %v", gotBroadcast, tc.expectBroadcast)
			}
		})
	}
}
