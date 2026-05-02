// internal/module/agent/probe_intent_dispatcher_codex_wire_test.go
//
// W6-3 P2-T4: integration test for the dispatcher → codex ProcessDead
// detector wiring set up in Module.New(). The other dispatcher_test.go
// tests swap startDetector to a recording stub; this test deliberately
// leaves the production wiring in place and drives the codex detector
// via its public test seams (codex.SetIsPidAliveFnForTest +
// codex.SetProcessDeadPollIntervalForTest) so the codex package's
// detector goroutine actually runs end-to-end.
//
// W6-6 P2-T3: extends with a ScreenChange wire test that drives
// codex.StartScreenChangeDetector via a fake screenWatcher. The
// production switch resolves prober.FirstAliveAgentInTree which
// requires a live *probe.Prober + tmux backend; instead the wire
// test swaps probeIntentDisp.startDetector to a closure that calls
// the real codex.StartScreenChangeDetector with a fake watcher and a
// scripted isCodexAlive — so the dispatcher pipeline (lifecycle,
// supportedKinds gate, applyProbeGuards, consumeSignals teardown)
// runs end-to-end against the real detector.
package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/store"
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

// fakeScreenWatcher implements the codex package's screenWatcher
// contract (Watch + StopWatchOwned) for the W6-6 ScreenChange wire
// test. Watch records the registered callback so the test can fire
// ScreenChangeEvent values synchronously; StopWatchOwned records the
// teardown call so the test can verify the active entry is properly
// torn down.
//
// Lives in the dispatcher test package because the codex package's
// screenWatcher interface is unexported — Go's structural typing lets
// any matching method-set satisfy it from outside the package.
//
// W6-6 R2 F1: Watch returns probe.WatchHandle (zero value — the fake
// cannot fabricate non-zero handles since fields are unexported); the
// detector code round-trips the handle to StopWatchOwned, so identity
// preservation across the round trip suffices for wire testing.
type fakeScreenWatcher struct {
	mu        sync.Mutex
	cb        probe.ScreenChangeCallback
	watchedTo string
	opts      probe.WatchOptions
	stopCalls int
}

func (f *fakeScreenWatcher) Watch(target string, opts probe.WatchOptions, cb probe.ScreenChangeCallback) probe.WatchHandle {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cb = cb
	f.watchedTo = target
	f.opts = opts
	return probe.WatchHandle{}
}

func (f *fakeScreenWatcher) StopWatchOwned(_ probe.WatchHandle) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls++
	return true
}

func (f *fakeScreenWatcher) fire(ev probe.ScreenChangeEvent) {
	f.mu.Lock()
	cb := f.cb
	f.mu.Unlock()
	if cb == nil {
		return
	}
	cb(ev)
}

func (f *fakeScreenWatcher) hasCallback() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cb != nil
}

func (f *fakeScreenWatcher) stopCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCalls
}

// screenChangeIntent is the canonical W6-6 ProbeIntent declaration
// used in dispatcher tests. OnEntryStatus = {Waiting}; OnSignal mirrors
// the production codex onScreenChange mapper (Kind=ScreenChange →
// Running).
func screenChangeIntent() agentpkg.ProbeIntent {
	return agentpkg.ProbeIntent{
		Kind:          agentpkg.ProbeIntentKindScreenChange,
		OnEntryStatus: []agentpkg.Status{agentpkg.StatusWaiting},
		OnSignal: func(sig agentpkg.Signal) agentpkg.Status {
			if sig.Kind != agentpkg.ProbeIntentKindScreenChange {
				return ""
			}
			return agentpkg.StatusRunning
		},
	}
}

// installScreenChangeWiring swaps probeIntentDisp.startDetector to a
// closure that routes ScreenChange to codex.StartScreenChangeDetector
// using the supplied fakeScreenWatcher + isCodexAlive. ProcessDead
// continues to route to the standard codex detector (using m.tmux) so
// existing dispatcher invariants stay covered.
//
// supportedKinds is set to {ProcessDead, ScreenChange} so the
// lifecycle's audit-F6 fail-closed branch does NOT trip when the
// dispatcher arms a ScreenChange entry.
func installScreenChangeWiring(t *testing.T, m *Module, fw *fakeScreenWatcher, isCodexAlive func() bool) {
	t.Helper()
	m.probeIntentDisp.startDetector = func(ctx context.Context, mod *Module, kind agentpkg.ProbeIntentKind, paneID string, senderPID int, out chan<- agentpkg.Signal) {
		switch kind {
		case agentpkg.ProbeIntentKindProcessDead:
			codex.StartProcessDeadDetector(ctx, mod.tmux, paneID, senderPID, out)
		case agentpkg.ProbeIntentKindScreenChange:
			codex.StartScreenChangeDetector(ctx, fw, isCodexAlive, paneID, senderPID, out)
		default:
			defaultStartProbeIntentDetector(ctx, mod, kind, paneID, senderPID, out)
		}
	}
	m.probeIntentDisp.supportedKinds = map[agentpkg.ProbeIntentKind]struct{}{
		agentpkg.ProbeIntentKindProcessDead:  {},
		agentpkg.ProbeIntentKindScreenChange: {},
	}
	t.Cleanup(func() { m.probeIntentDisp.stopAll() })
}

// TestDispatcher_CodexScreenChangeWire_WaitingToRunning drives the W6-6
// ScreenChange path end-to-end through the dispatcher pipeline:
//
//  1. Register a fake codex provider that declares both ProcessDead +
//     ScreenChange intents.
//  2. Install custom startDetector wiring that routes ScreenChange to
//     codex.StartScreenChangeDetector with a fakeScreenWatcher.
//  3. applyStatus(Waiting) — dispatcher arms BOTH intents (ProcessDead
//     gates on {Running, Waiting}; ScreenChange gates on {Waiting}).
//  4. Assert fakeScreenWatcher has registered a callback (detector
//     entered Phase A).
//  5. Fire ScreenStable then ScreenChanged — detector emits Signal.
//  6. consumeSignals applies the signal, status flips to Running,
//     re-runs applyStatus(Running) which tears down the ScreenChange
//     entry (case 3: Running ∉ {Waiting}).
//  7. ScreenChange detector ctx is cancelled → StopWatch called.
func TestDispatcher_CodexScreenChangeWire_WaitingToRunning(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}

	// Replace the default fakeProbeIntentAgentProvider (registered by
	// newDispatcherTestModule with only ProcessDead) so the codex test
	// provider declares BOTH intents.
	m.registry = agentpkg.NewRegistry()
	m.registry.Register(&fakeProbeIntentAgentProvider{
		fakeAgentProvider: fakeAgentProvider{typeName: "codex"},
		intents:           []agentpkg.ProbeIntent{processDeadIntent(), screenChangeIntent()},
	})

	// Avoid the ProcessDead detector emitting (would race with our
	// ScreenChange test by flipping currentStatus to Error).
	t.Cleanup(codex.SetProcessDeadPollIntervalForTest(50 * time.Millisecond))
	t.Cleanup(codex.SetIsPidAliveFnForTest(func(int) bool { return true }))

	if fake, ok := getFakeTmux(t, m); ok {
		fake.SetPanes([]string{"%5"})
	}

	fw := &fakeScreenWatcher{}
	installScreenChangeWiring(t, m, fw, func() bool { return true })

	// Seed Waiting state — applyStatus(Waiting) is the natural entry
	// for ScreenChange (per spec §4.1: OnEntryStatus = {Waiting}).
	seedWaitingFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusWaiting)

	// Wait for the ScreenChange detector to call Watch (registers cb).
	waitFor(t, time.Second, fw.hasCallback, "fake screenWatcher received Watch")

	// Verify the active entry exists for ScreenChange.
	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindScreenChange); !ok {
		t.Fatalf("ScreenChange active entry not present after applyStatus(Waiting)")
	}

	// Phase A → B: ScreenStable arms the detector.
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})
	// Phase B emit: ScreenChanged + isCodexAlive=true → emit Signal.
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	// applyProbeGuards routes the Signal through the J3 pre-grace
	// (300ms) — wait long enough for the consumeSignals path to
	// complete.
	waitFor(t, 2*time.Second, func() bool {
		m.mu.Lock()
		st := m.currentStatus["work"]
		m.mu.Unlock()
		return st == agentpkg.StatusRunning
	}, "currentStatus flipped to Running after ScreenChange Signal")

	// 5-case lifecycle: status=Running → ScreenChange OnEntryStatus
	// no longer matches → entry teardown via consumeSignals re-runs
	// applyStatus(Running).
	waitFor(t, 2*time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindScreenChange)
		return !ok
	}, "ScreenChange active entry torn down after Running")

	// StopWatch called as part of detector return + dispatcher
	// reconcile.
	waitFor(t, 2*time.Second, func() bool {
		return fw.stopCallCount() >= 1
	}, "fake screenWatcher.StopWatch called")
}

// seedWaitingFrame mirrors seedRunningFrame but sets Status =
// StatusWaiting on both the frame projection and currentStatus. Used
// by the ScreenChange wire test (W6-6 OnEntryStatus = {Waiting}).
func seedWaitingFrame(t *testing.T, m *Module, session, paneID, agentType string, pid int) {
	t.Helper()
	if _, err := m.frames.Upsert(store.Frame{
		FrameID:          "frame-" + session + "-" + agentType,
		PaneID:           paneID,
		AgentType:        agentType,
		PID:              pid,
		PPID:             1,
		ProcessStartTime: "Sun Apr 20 01:30:00 2026",
		Status:           agentpkg.StatusWaiting,
		StartedAt:        100,
		LastSeenAt:       120,
		Verified:         true,
	}); err != nil {
		t.Fatalf("seed waiting frame: %v", err)
	}
	m.mu.Lock()
	m.currentStatus[session] = agentpkg.StatusWaiting
	m.mu.Unlock()
}
