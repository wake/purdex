package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// processDeadIntent is the canonical W6-3 ProbeIntent declaration used in
// dispatcher tests. OnEntryStatus = {Running, Waiting}; OnSignal mirrors the
// production codex mapping (PaneAlive=true → error, =false → clear).
func processDeadIntent() agentpkg.ProbeIntent {
	return agentpkg.ProbeIntent{
		Kind:          agentpkg.ProbeIntentKindProcessDead,
		OnEntryStatus: []agentpkg.Status{agentpkg.StatusRunning, agentpkg.StatusWaiting},
		OnSignal: func(sig agentpkg.Signal) agentpkg.Status {
			if sig.PaneAlive {
				return agentpkg.StatusError
			}
			return agentpkg.StatusClear
		},
	}
}

// newDispatcherTestModule wires a Module with a fake codex provider that
// declares the ProcessDead intent, plus a fake tmux executor that resolves
// "%5" → "work". Returns a t.Cleanup-bound module ready for dispatcher
// lifecycle tests. The default detector stub honors ctx and never emits;
// individual tests swap startProbeIntentDetectorFn when they need control.
func newDispatcherTestModule(t *testing.T) *Module {
	t.Helper()
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.registry.Register(&fakeProbeIntentAgentProvider{
		fakeAgentProvider: fakeAgentProvider{typeName: "codex"},
		intents:           []agentpkg.ProbeIntent{processDeadIntent()},
	})
	return m
}

// seedRunningFrame inserts a top frame for the (paneID, session, agentType,
// pid) tuple and writes currentStatus = Running. Used by tests that want a
// pre-existing armable target.
func seedRunningFrame(t *testing.T, m *Module, session, paneID, agentType string, pid int) {
	t.Helper()
	if _, err := m.frames.Upsert(store.Frame{
		FrameID:          "frame-" + session + "-" + agentType,
		PaneID:           paneID,
		AgentType:        agentType,
		PID:              pid,
		PPID:             1,
		ProcessStartTime: "Sun Apr 20 01:30:00 2026",
		Status:           agentpkg.StatusRunning,
		StartedAt:        100,
		LastSeenAt:       120,
		Verified:         true,
	}); err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	m.mu.Lock()
	m.currentStatus[session] = agentpkg.StatusRunning
	m.mu.Unlock()
}

// readActiveIntent returns the active entry under m.mu, with ok=false if
// missing. Helper for assertions.
func readActiveIntent(m *Module, session string, kind agentpkg.ProbeIntentKind) (activeIntent, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	per, ok := m.activeProbeIntents[session]
	if !ok {
		return activeIntent{}, false
	}
	cur, ok := per[kind]
	return cur, ok
}

// installRecordingDetector swaps in a stub that records every (paneID,
// senderPID) start, increments started counter, and exits when ctx done
// (incrementing canceled counter). Returns the counters + a started chan
// that closes after the first start (for synchronizing tests).
type recordingDetector struct {
	mu       sync.Mutex
	starts   []recordingDetectorStart
	canceled int32
	started  chan struct{}
}

type recordingDetectorStart struct {
	PaneID    string
	SenderPID int
	Kind      agentpkg.ProbeIntentKind
}

// installRecordingDetector swaps the dispatcher's startDetector to a
// recorder. Per-dispatcher field swap (instead of a package global)
// keeps tests isolated under -race even when subtests run sequentially
// in different Modules. Cleanup also stops the dispatcher so any
// goroutine still reading the field exits before the test returns.
func installRecordingDetector(t *testing.T, m *Module) *recordingDetector {
	t.Helper()
	rec := &recordingDetector{started: make(chan struct{})}
	m.probeIntentDisp.startDetector = func(ctx context.Context, _ *Module, kind agentpkg.ProbeIntentKind, paneID string, senderPID int, _ chan<- agentpkg.Signal) {
		rec.mu.Lock()
		rec.starts = append(rec.starts, recordingDetectorStart{PaneID: paneID, SenderPID: senderPID, Kind: kind})
		first := len(rec.starts) == 1
		rec.mu.Unlock()
		if first {
			close(rec.started)
		}
		<-ctx.Done()
		atomic.AddInt32(&rec.canceled, 1)
	}
	t.Cleanup(func() { m.probeIntentDisp.stopAll() })
	return rec
}

func (r *recordingDetector) startCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.starts)
}

func (r *recordingDetector) cancelCount() int32 {
	return atomic.LoadInt32(&r.canceled)
}

// waitFor polls until pred returns true or timeout. Used to synchronize
// goroutine teardown without sleeps.
func waitFor(t *testing.T, deadline time.Duration, pred func() bool, msg string) {
	t.Helper()
	start := time.Now()
	for time.Since(start) < deadline {
		if pred() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("waitFor timeout: %s", msg)
}

// -----------------------------------------------------------------------------
// Lifecycle 5 case
// -----------------------------------------------------------------------------

// case 1: !shouldActive && !wasActive → noop
func TestApplyIntentLifecycle_Case1_NotActive_NotShouldActive_Noop(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)

	// Status idle, not in OnEntryStatus
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusIdle)

	if rec.startCount() != 0 {
		t.Fatalf("detector started count = %d, want 0", rec.startCount())
	}
	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead); ok {
		t.Fatalf("activeProbeIntents present after idle, want absent")
	}
}

// case 2: shouldActive && !wasActive (frame ok) → record + arm
func TestApplyIntentLifecycle_Case2_NotActive_ShouldActive_Start(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	<-rec.started
	if rec.startCount() != 1 {
		t.Fatalf("detector started count = %d, want 1", rec.startCount())
	}
	cur, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
	if !ok {
		t.Fatalf("activeProbeIntents missing, want set")
	}
	if cur.agentType != "codex" || cur.paneID != "%5" || cur.senderPID != 4242 {
		t.Fatalf("active entry = %+v, want codex/%%5/4242", cur)
	}
	if cur.generation == 0 {
		t.Fatalf("generation = 0, want >0")
	}

	// Cleanup: stopAll cancels the goroutine.
	m.probeIntentDisp.stopAll()
	waitFor(t, time.Second, func() bool { return rec.cancelCount() == 1 }, "detector cancel after stopAll")
}

// case 3: !shouldActive && wasActive → cancel + delete
func TestApplyIntentLifecycle_Case3_Active_NotShouldActive_Stop(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	// Arm
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	<-rec.started

	// Transition to idle (not in OnEntryStatus)
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusIdle)

	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead); ok {
		t.Fatalf("activeProbeIntents present after idle, want absent")
	}
	waitFor(t, time.Second, func() bool { return rec.cancelCount() == 1 }, "detector cancel after stop")
}

// case 4: shouldActive && wasActive && targetMatches → noop (no extra arm)
func TestApplyIntentLifecycle_Case4_Active_ShouldActive_TargetMatch_Noop(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	// First arm
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	<-rec.started
	cur1, _ := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)

	// Re-apply with same status / same target — should be a noop
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	if rec.startCount() != 1 {
		t.Fatalf("detector started count = %d, want 1 (case 4 noop)", rec.startCount())
	}
	cur2, _ := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
	if cur1.generation != cur2.generation {
		t.Fatalf("generation changed across noop apply: %d → %d", cur1.generation, cur2.generation)
	}

	m.probeIntentDisp.stopAll()
}

// case 5: shouldActive && wasActive && target mismatch → cancel old + record new + arm
func TestApplyIntentLifecycle_Case5_Active_ShouldActive_TargetMismatch_CancelAndRearm(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	<-rec.started
	cur1, _ := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)

	// Add a second frame with a different pid (simulates codex restart in the
	// same pane). The new frame has a later StartedAt so projection selects
	// it as TopFrame.
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
		t.Fatalf("Upsert frame: %v", err)
	}

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	// Wait for second start.
	waitFor(t, time.Second, func() bool { return rec.startCount() == 2 }, "second detector start")
	cur2, _ := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
	if cur2.senderPID != 5555 {
		t.Fatalf("rearmed senderPID = %d, want 5555", cur2.senderPID)
	}
	if cur1.generation == cur2.generation {
		t.Fatalf("generation did not advance on re-arm: %d == %d", cur1.generation, cur2.generation)
	}
	// Old detector goroutine should have been cancelled.
	waitFor(t, time.Second, func() bool { return rec.cancelCount() >= 1 }, "old detector cancel")

	m.probeIntentDisp.stopAll()
}

// -----------------------------------------------------------------------------
// Reconcile 4 case
// -----------------------------------------------------------------------------

// case A: unknown agent → drop all entries for the session
func TestReconcileSessionActive_UnknownAgent_DropAll(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	<-rec.started

	// Pretend agentType swap to an unregistered name.
	m.probeIntentDisp.applyStatus("work", "ghost-agent", agentpkg.StatusRunning)

	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead); ok {
		t.Fatalf("activeProbeIntents present after unknown-agent reconcile")
	}
	waitFor(t, time.Second, func() bool { return rec.cancelCount() == 1 }, "old detector cancel after unknown agent")
}

// case B: provider has no ProbeIntents → drop all
func TestReconcileSessionActive_ProviderHasNoIntents_DropAll(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	<-rec.started

	// Register a no-intent provider with a different agent name and switch.
	m.registry.Register(&fakeAgentProvider{typeName: "cc-noprobes"})
	m.probeIntentDisp.applyStatus("work", "cc-noprobes", agentpkg.StatusRunning)

	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead); ok {
		t.Fatalf("activeProbeIntents present after switching to no-probe provider")
	}
	waitFor(t, time.Second, func() bool { return rec.cancelCount() == 1 }, "detector cancel after no-probe switch")
}

// case C: agentType changed → drop old kind even if the new provider declares
// the same Kind. (The new provider's lifecycle then rearms under its own
// agentType.)
func TestReconcileSessionActive_AgentTypeChanged_DropOld(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	<-rec.started
	cur1, _ := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
	if cur1.agentType != "codex" {
		t.Fatalf("initial agentType = %q, want codex", cur1.agentType)
	}

	// Register an alt provider that also declares ProcessDead and switch.
	m.registry.Register(&fakeProbeIntentAgentProvider{
		fakeAgentProvider: fakeAgentProvider{typeName: "alt-codex"},
		intents:           []agentpkg.ProbeIntent{processDeadIntent()},
	})
	// Replace top frame agent type so lifecycle finds (paneID, pid) under the new name.
	if _, err := m.frames.Upsert(store.Frame{
		FrameID:          "frame-work-alt-codex",
		PaneID:           "%5",
		AgentType:        "alt-codex",
		PID:              7777,
		PPID:             1,
		ProcessStartTime: "Sun Apr 20 02:30:00 2026",
		Status:           agentpkg.StatusRunning,
		StartedAt:        300,
		LastSeenAt:       320,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert alt frame: %v", err)
	}
	// Remove the old codex frame to make alt-codex the top frame.
	if err := m.frames.Delete("frame-work-codex"); err != nil {
		t.Fatalf("Delete frame: %v", err)
	}

	m.probeIntentDisp.applyStatus("work", "alt-codex", agentpkg.StatusRunning)
	waitFor(t, time.Second, func() bool {
		cur, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		return ok && cur.agentType == "alt-codex"
	}, "active entry agentType becomes alt-codex")
	waitFor(t, time.Second, func() bool { return rec.cancelCount() >= 1 }, "old codex detector cancel")

	m.probeIntentDisp.stopAll()
}

// case D: kind not in declared → drop the old kind only. Implemented by
// constructing a session with an active ProcessDead entry, then switching to a
// provider that declares NO ProcessDead (empty intents).
func TestReconcileSessionActive_KindNotInDeclared_DropOldKind(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	<-rec.started

	// Register an alt agent that declares NO intents and switch.
	m.registry.Register(&fakeProbeIntentAgentProvider{
		fakeAgentProvider: fakeAgentProvider{typeName: "alt-noprobedead"},
		intents:           nil, // empty declared kinds
	})
	m.probeIntentDisp.applyStatus("work", "alt-noprobedead", agentpkg.StatusRunning)

	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead); ok {
		t.Fatalf("ProcessDead entry present after switching to provider without it")
	}
	waitFor(t, time.Second, func() bool { return rec.cancelCount() == 1 }, "detector cancel after kind drop")
}

// -----------------------------------------------------------------------------
// Probe-applied teardown (round 5 P1)
// -----------------------------------------------------------------------------

// installEmitOnceDetector swaps the dispatcher's startDetector to one that
// emits a single Signal on start, then waits for ctx.Done. Used to verify
// consumeSignals re-runs applyStatus after applied=true. Cleanup stops
// the dispatcher to drain detector goroutines.
func installEmitOnceDetector(t *testing.T, m *Module, sig agentpkg.Signal) {
	t.Helper()
	m.probeIntentDisp.startDetector = func(ctx context.Context, _ *Module, _ agentpkg.ProbeIntentKind, _ string, _ int, out chan<- agentpkg.Signal) {
		select {
		case out <- sig:
		case <-ctx.Done():
			return
		}
		<-ctx.Done()
	}
	t.Cleanup(func() { m.probeIntentDisp.stopAll() })
}

// applied=true → re-runs applyStatus → activeProbeIntents entry deleted via
// case 3 (active && !shouldActive).
func TestConsumeSignals_AppliedTrue_ReRunsApplyStatus(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}
	installEmitOnceDetector(t, m, agentpkg.Signal{
		Kind:      agentpkg.ProbeIntentKindProcessDead,
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 4242,
	})
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	// After detector emits PaneAlive=true → OnSignal returns Error → guards
	// pass → applied=true → consumeSignals invokes applyStatus(error) →
	// case 3 (active, error not in OnEntryStatus) tears down.
	waitFor(t, time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		return !ok
	}, "active entry torn down after applied=true")

	m.mu.Lock()
	got := m.currentStatus["work"]
	m.mu.Unlock()
	if got != agentpkg.StatusError {
		t.Fatalf("currentStatus = %q, want error", got)
	}
}

// applied=false (transition gate trips because currentStatus already error)
// → consumeSignals continues but does NOT tear down. Active entry persists.
func TestConsumeSignals_AppliedFalse_NoTeardown(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}
	installEmitOnceDetector(t, m, agentpkg.Signal{
		Kind:      agentpkg.ProbeIntentKindProcessDead,
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 4242,
	})
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	// Pre-set currentStatus to Error: applyProbeGuards' ErrorGuard rejects
	// the signal → applied=false. We still want the detector arm path to
	// run, so apply with Running first to arm, then flip to Error AFTER arm.
	//
	// Easier path: arm with Running, then between detector start and
	// signal delivery, set Error. But that's racy. Instead: set
	// OnEntryStatus to include Error so arm path runs even with status=Error;
	// detector emits PaneAlive=true → OnSignal returns Error; transition
	// gate (current==new==Error) trips → applied=false.
	//
	// Replace registry intent with one whose OnEntryStatus includes Error:
	m.registry = agentpkg.NewRegistry()
	m.registry.Register(&fakeProbeIntentAgentProvider{
		fakeAgentProvider: fakeAgentProvider{typeName: "codex"},
		intents: []agentpkg.ProbeIntent{
			{
				Kind:          agentpkg.ProbeIntentKindProcessDead,
				OnEntryStatus: []agentpkg.Status{agentpkg.StatusError},
				OnSignal: func(sig agentpkg.Signal) agentpkg.Status {
					return agentpkg.StatusError
				},
			},
		},
	})
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusError
	m.mu.Unlock()

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusError)

	// Active entry should remain (no probe-applied teardown happened).
	// Wait a brief moment for the detector to emit + guard to drop.
	time.Sleep(50 * time.Millisecond)
	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead); !ok {
		t.Fatalf("active entry torn down despite applied=false")
	}

	m.probeIntentDisp.stopAll()
}

// -----------------------------------------------------------------------------
// Stale-callback / generation guard
// -----------------------------------------------------------------------------

func TestMakeStaleCheck_GenerationMismatch_ReturnsFalse(t *testing.T) {
	m := newTestModule(t)
	m.mu.Lock()
	m.activeProbeIntents["work"] = map[agentpkg.ProbeIntentKind]activeIntent{
		agentpkg.ProbeIntentKindProcessDead: {
			agentType:  "codex",
			generation: 5,
		},
	}
	m.mu.Unlock()

	check := makeProbeIntentStaleCheck("work", agentpkg.ProbeIntentKindProcessDead, "codex", 4)
	m.mu.Lock()
	got := check(m)
	m.mu.Unlock()
	if got {
		t.Fatalf("staleCheck = true with generation mismatch (expect 5 vs got 4); want false")
	}
}

func TestMakeStaleCheck_AgentTypeMismatch_ReturnsFalse(t *testing.T) {
	m := newTestModule(t)
	m.mu.Lock()
	m.activeProbeIntents["work"] = map[agentpkg.ProbeIntentKind]activeIntent{
		agentpkg.ProbeIntentKindProcessDead: {
			agentType:  "codex",
			generation: 5,
		},
	}
	m.mu.Unlock()

	check := makeProbeIntentStaleCheck("work", agentpkg.ProbeIntentKindProcessDead, "alt-codex", 5)
	m.mu.Lock()
	got := check(m)
	m.mu.Unlock()
	if got {
		t.Fatalf("staleCheck = true with agentType mismatch; want false")
	}
}

func TestMakeStaleCheck_AllMatch_ReturnsTrue(t *testing.T) {
	m := newTestModule(t)
	m.mu.Lock()
	m.activeProbeIntents["work"] = map[agentpkg.ProbeIntentKind]activeIntent{
		agentpkg.ProbeIntentKindProcessDead: {
			agentType:  "codex",
			generation: 5,
		},
	}
	m.mu.Unlock()

	check := makeProbeIntentStaleCheck("work", agentpkg.ProbeIntentKindProcessDead, "codex", 5)
	m.mu.Lock()
	got := check(m)
	m.mu.Unlock()
	if !got {
		t.Fatalf("staleCheck = false with all-match; want true")
	}
}

// -----------------------------------------------------------------------------
// Replay race (by2z79ouc ATK-2)
// -----------------------------------------------------------------------------

// applyStatus snapshot says Running, but a concurrent hook flipped
// currentStatus to Idle before m.mu was acquired. Lifecycle re-reads under
// lock and refuses to arm.
func TestApplyIntentLifecycle_StatusChangedBetweenSnapshotAndArm_NoArming(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	// Simulate the race: caller snapshot reads Running, but by the time
	// applyIntentLifecycle gets the lock, currentStatus[session] has been
	// flipped to Idle (e.g. a PdxStop hook landed in between).
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	if rec.startCount() != 0 {
		t.Fatalf("detector started despite live currentStatus = Idle; want zero starts")
	}
	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead); ok {
		t.Fatalf("activeProbeIntents present despite live status mismatch")
	}
}

// -----------------------------------------------------------------------------
// stopAll
// -----------------------------------------------------------------------------

func TestStopAll_CancelsAndClearsActiveMap(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	<-rec.started

	m.probeIntentDisp.stopAll()

	m.mu.Lock()
	if len(m.activeProbeIntents) != 0 {
		m.mu.Unlock()
		t.Fatalf("activeProbeIntents non-empty after stopAll: %d entries", len(m.activeProbeIntents))
	}
	m.mu.Unlock()

	waitFor(t, time.Second, func() bool { return rec.cancelCount() == 1 }, "detector cancel after stopAll")
}

// -----------------------------------------------------------------------------
// W6-3 P1-T6: replayStatus daemon-restart recovery (closes #698)
// -----------------------------------------------------------------------------

// TestReplayStatus_RunningSession_DetectorArmed pins the recovery contract:
// after replayFromDB hydrates currentStatus + frame projection, replayStatus
// re-runs applyStatus per session so a session whose top frame matches a
// ProbeIntent provider rearms its detector.
func TestReplayStatus_RunningSession_DetectorArmed(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	if fake, ok := m.tmux.(*tmux.FakeExecutor); ok {
		fake.SetPaneSessionName("%5", "work")
	}
	// Seed a frame + currentStatus the way replayFromDB would after a
	// daemon restart with codex still running on session "work".
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.replayStatus()

	<-rec.started
	if rec.startCount() != 1 {
		t.Fatalf("detector started count = %d, want 1", rec.startCount())
	}
	cur, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
	if !ok {
		t.Fatalf("activeProbeIntents missing after replayStatus")
	}
	if cur.agentType != "codex" || cur.paneID != "%5" || cur.senderPID != 4242 {
		t.Fatalf("active entry = %+v, want codex/%%5/4242", cur)
	}
}

// TestReplayStatus_IdleSession_DetectorNotArmed pins the gating contract:
// a session whose replayed status is Idle (not in OnEntryStatus for the
// ProcessDead intent) does NOT arm a detector.
func TestReplayStatus_IdleSession_DetectorNotArmed(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	if fake, ok := m.tmux.(*tmux.FakeExecutor); ok {
		fake.SetPaneSessionName("%5", "work")
	}
	// Seed a frame but flip currentStatus to Idle.
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()

	m.probeIntentDisp.replayStatus()

	// Give any goroutine that might wrongly start a brief moment to do so.
	time.Sleep(20 * time.Millisecond)
	if rec.startCount() != 0 {
		t.Fatalf("detector started for idle session: count = %d, want 0", rec.startCount())
	}
	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead); ok {
		t.Fatalf("activeProbeIntents present for idle session, want absent")
	}
}

// TestReplayStatus_StaleFrame_DetectorEmitsImmediately exercises the
// canonical issue-#698 fix path: daemon restart inherits a frame whose
// process has already died. The detector (here stub-driven via
// installEmitOnceDetector) emits a Signal on first call; consumeSignals
// runs it through applyProbeGuards which broadcasts error + tears down
// the active entry.
//
// In production the detector polls IsPidAlive at 1Hz; here we simulate
// the immediate emit so the test stays deterministic without timing
// dependencies. Real codex emit path is tested in P2-T7 / mlab.
func TestReplayStatus_StaleFrame_DetectorEmitsImmediately(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}
	if fake, ok := m.tmux.(*tmux.FakeExecutor); ok {
		fake.SetPaneSessionName("%5", "work")
	}
	installEmitOnceDetector(t, m, agentpkg.Signal{
		Kind:      agentpkg.ProbeIntentKindProcessDead,
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 4242,
	})
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.replayStatus()

	// After detector emits PaneAlive=true → OnSignal returns Error → guards
	// pass → applied=true → consumeSignals invokes applyStatus(error) →
	// case 3 tears down the active entry.
	waitFor(t, time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		return !ok
	}, "active entry torn down after stale-frame emit")

	m.mu.Lock()
	got := m.currentStatus["work"]
	m.mu.Unlock()
	if got != agentpkg.StatusError {
		t.Fatalf("currentStatus = %q, want error after stale-frame replay", got)
	}
}

// -----------------------------------------------------------------------------
// Round-2 audit fixes: F1 graceWindow strand / F2 generation-scoped teardown / F6 fail-closed
// -----------------------------------------------------------------------------

// TestConsumeSignals_GraceWindowDrop_TearsDownActiveEntry pins F1: when a
// one-shot detector emits its sole Signal during an active graceWindow,
// applyProbeGuards drops the signal (applied=false), the channel closes,
// and consumeSignals' post-loop teardown must remove the active entry —
// otherwise a future status change observes case-4 (target match) and the
// detector never re-arms, leaving codex permanently undetected.
func TestConsumeSignals_GraceWindowDrop_TearsDownActiveEntry(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}
	// Emit-once-and-exit detector mirrors production codex (returns after
	// emit, channel closes via wrapping goroutine).
	m.probeIntentDisp.startDetector = func(ctx context.Context, _ *Module, _ agentpkg.ProbeIntentKind, _ string, _ int, out chan<- agentpkg.Signal) {
		select {
		case out <- agentpkg.Signal{
			Kind:      agentpkg.ProbeIntentKindProcessDead,
			PaneAlive: true,
			PaneID:    "%5",
			SenderPID: 4242,
		}:
		case <-ctx.Done():
		}
	}
	t.Cleanup(func() { m.probeIntentDisp.stopAll() })

	seedRunningFrame(t, m, "work", "%5", "codex", 4242)
	// Open graceWindow: any probe signal arriving in the next probeGraceWindow
	// is suppressed. With the F1 fix, the active entry must still be torn
	// down so a subsequent crash is not missed.
	m.probeOrch.recordHookAt("work")

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	waitFor(t, 2*time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		return !ok
	}, "active entry torn down post-loop after graceWindow drop (F1)")
}

// TestStopActiveIntentInLock_GenerationMismatch_PreservesEntry pins F2:
// the generation guard must reject mismatched expectations so a concurrent
// rearm (gen N+1) survives the previous detector's applied-true teardown.
// Direct unit test on stopActiveIntentInLock — the F2 fix's contract is
// the helper's behavior under generation mismatch.
func TestStopActiveIntentInLock_GenerationMismatch_PreservesEntry(t *testing.T) {
	m := newDispatcherTestModule(t)

	cancelCalls := int32(0)
	cancel := func() { atomic.AddInt32(&cancelCalls, 1) }
	m.mu.Lock()
	m.activeProbeIntents["work"] = map[agentpkg.ProbeIntentKind]activeIntent{
		agentpkg.ProbeIntentKindProcessDead: {
			agentType:  "codex",
			paneID:     "%5",
			senderPID:  4242,
			generation: 5,
			cancel:     cancel,
		},
	}
	m.mu.Unlock()

	// Caller observed gen 1 → expectGeneration=1 → cur.generation=5 → mismatch.
	m.mu.Lock()
	_, ok := m.probeIntentDisp.stopActiveIntentInLock("work", agentpkg.ProbeIntentKindProcessDead, 1, "test-mismatch")
	m.mu.Unlock()

	if ok {
		t.Fatalf("ok=true with mismatched expectGeneration, want false")
	}
	if atomic.LoadInt32(&cancelCalls) != 0 {
		t.Fatalf("cancel invoked %d times despite mismatch, want 0", atomic.LoadInt32(&cancelCalls))
	}
	// Entry must still be present.
	cur, present := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
	if !present {
		t.Fatalf("entry torn down despite generation mismatch")
	}
	if cur.generation != 5 {
		t.Fatalf("generation = %d, want 5 (preserved)", cur.generation)
	}
}

// TestApplyIntentLifecycle_UnsupportedKind_FailsClosed pins F6: when a
// provider declares a Kind that has no corresponding entry in
// supportedKinds, lifecycle must NOT arm a detector — silent default
// noop would otherwise leave the system "armed" with zero observability.
// Verifies (1) no detector started, (2) no active entry recorded, (3)
// MetricProbeIntentUnsupportedKind +1.
func TestApplyIntentLifecycle_UnsupportedKind_FailsClosed(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	futureKind := agentpkg.ProbeIntentKind("future_kind_unwired")
	futureIntent := agentpkg.ProbeIntent{
		Kind:          futureKind,
		OnEntryStatus: []agentpkg.Status{agentpkg.StatusRunning},
		OnSignal:      func(agentpkg.Signal) agentpkg.Status { return agentpkg.StatusError },
	}

	before := snapshotProbeIntentMetrics()
	m.probeIntentDisp.applyIntentLifecycle("work", "codex", agentpkg.StatusRunning, futureIntent)
	after := snapshotProbeIntentMetrics()

	if rec.startCount() != 0 {
		t.Fatalf("detector started %d time(s) for unsupported kind, want 0", rec.startCount())
	}
	if _, ok := readActiveIntent(m, "work", futureKind); ok {
		t.Fatalf("active entry created for unsupported kind, want none")
	}
	if got := after.unsupportedKind - before.unsupportedKind; got != 1 {
		t.Fatalf("unsupportedKind metric delta = %d, want 1", got)
	}
	if got := after.started - before.started; got != 0 {
		t.Fatalf("started metric delta = %d, want 0 (no arm)", got)
	}
}
