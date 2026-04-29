// internal/module/agent/probe_intent_dispatcher_codex_wire_test.go
//
// W6-3 P2-T4: integration test for the dispatcher → codex ProcessDead
// detector wiring set up in Module.New(). The other dispatcher_test.go
// tests swap startDetector to a recording stub; this test deliberately
// leaves the production wiring in place and drives the codex detector
// via its public test seams (codex.SetIsPidAliveFnForTest +
// codex.SetProcessDeadPollIntervalForTest) so the codex package's
// detector goroutine actually runs end-to-end.
package agent

import (
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
)

// installCodexDetectorSeams installs the codex test seams (fast poll +
// alive-fn override) and arranges stopAll to cancel any armed detectors
// at test end. Cleanup ordering is LIFO: stopAll runs first → detector
// ctx cancel propagates → seam restores run last.
//
// The seams use atomic primitives so a still-polling detector reading
// the active fn concurrently with the cleanup write does not race; the
// stopAll call here is a leak-prevention belt rather than a race-fix
// (without it, the goroutine would keep polling until the test process
// exits).
func installCodexDetectorSeams(t *testing.T, m *Module, alive func(int) bool) {
	t.Helper()
	t.Cleanup(codex.SetProcessDeadPollIntervalForTest(time.Millisecond))
	t.Cleanup(codex.SetIsPidAliveFnForTest(alive))
	t.Cleanup(func() { m.probeIntentDisp.stopAll() })
}

// TestApplyStatus_RealCodexDetector_ArmsAndStops drives the full P2-T4
// wiring: applyStatus(running) on a session with a ProcessDead-declaring
// provider arms the dispatcher, the codex detector polls (override:
// pid=dead, pane=alive → emit Signal{PaneAlive=true}), guards run, status
// flips to error, and consumeSignals re-runs applyStatus(error) which
// tears the active entry down (case 3).
//
// This test does NOT swap dispatcher.startDetector; it exercises the
// production closure installed in Module.New(). The codex package's
// isPidAliveFn is controlled via the cross-package test seam.
func TestApplyStatus_RealCodexDetector_ArmsAndStops(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}

	// Force the first poll tick to observe pid=dead. Seam helper orders
	// stopAll BEFORE seam restoration to keep -race clean.
	installCodexDetectorSeams(t, m, func(int) bool { return false })

	// Register %5 in the fake tmux pane list so HasPane returns true →
	// PaneAlive=true → OnSignal returns Error (W6-3 path).
	if fake, ok := getFakeTmux(t, m); ok {
		fake.SetPanes([]string{"%5"})
	}
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	// Sanity: the production dispatcher closure (not a stub) is in place.
	if m.probeIntentDisp.startDetector == nil {
		t.Fatalf("dispatcher.startDetector unset — Module.New() did not wire the codex routing closure")
	}

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	// After detector emits PaneAlive=true → OnSignal returns Error → guards
	// pass → applied=true → consumeSignals invokes applyStatus(error) →
	// case 3 (active && !shouldActive) tears down the active entry.
	waitFor(t, 2*time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		return !ok
	}, "active entry torn down after real codex detector emits")

	m.mu.Lock()
	got := m.currentStatus["work"]
	m.mu.Unlock()
	if got != agentpkg.StatusError {
		t.Fatalf("currentStatus = %q, want error after real codex detector emit", got)
	}
}

// TestApplyStatus_RealCodexDetector_PaneGone_ClearPath drives the W6-4
// path through the same production wiring: pid dead + pane gone →
// PaneAlive=false → OnSignal returns Clear → status flips to clear,
// active entry torn down.
func TestApplyStatus_RealCodexDetector_PaneGone_ClearPath(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}

	installCodexDetectorSeams(t, m, func(int) bool { return false })

	// Pane list intentionally OMITS %5 — HasPane(%5)=false → PaneAlive=false.
	if fake, ok := getFakeTmux(t, m); ok {
		fake.SetPanes([]string{"%9"})
	}
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	waitFor(t, 2*time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		return !ok
	}, "active entry torn down after real codex detector emits clear")

	m.mu.Lock()
	got := m.currentStatus["work"]
	m.mu.Unlock()
	if got != agentpkg.StatusClear {
		t.Fatalf("currentStatus = %q, want clear after real codex detector emit (pane gone)", got)
	}
}

// TestApplyStatus_RealCodexDetector_BothAlive_NoEmit confirms the
// detector keeps polling without emitting when both pid and pane are
// alive: the active entry persists, currentStatus stays running.
//
// This is the negative case that complements the two emit-path tests
// above — the codex routing closure is exercised but the inner detector
// does not produce a signal.
func TestApplyStatus_RealCodexDetector_BothAlive_NoEmit(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}

	installCodexDetectorSeams(t, m, func(int) bool { return true })

	if fake, ok := getFakeTmux(t, m); ok {
		fake.SetPanes([]string{"%5"})
	}
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	// Detector polls but never emits. Wait long enough that several ticks
	// would have happened (poll=1ms × ~50ms gives ≥40 ticks) — entry must
	// remain armed and currentStatus untouched.
	time.Sleep(50 * time.Millisecond)

	cur, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
	if !ok {
		t.Fatalf("active entry torn down despite both alive — should remain armed")
	}
	if cur.agentType != "codex" || cur.paneID != "%5" || cur.senderPID != 4242 {
		t.Fatalf("active entry mutated unexpectedly: %+v", cur)
	}
	m.mu.Lock()
	got := m.currentStatus["work"]
	m.mu.Unlock()
	if got != agentpkg.StatusRunning {
		t.Fatalf("currentStatus = %q, want running (no signal applied)", got)
	}
	// stopAll runs in installCodexDetectorSeams' Cleanup before the seam
	// restorations.
}

// getFakeTmux returns the FakeExecutor backing m.tmux, or false when the
// test module didn't install one. Helper extracted so the wire tests
// don't repeat the type assertion + nil-check pattern.
func getFakeTmux(t *testing.T, m *Module) (interface {
	SetPanes(paneIDs []string)
}, bool) {
	t.Helper()
	type setter interface {
		SetPanes(paneIDs []string)
	}
	if m.tmux == nil {
		return nil, false
	}
	s, ok := m.tmux.(setter)
	return s, ok
}
