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

// TestConsumeSignals_GraceWindowDrop_RearmsAfterTeardown pins F1 +
// round-3 follow-up: when a one-shot detector emits its sole Signal
// during an active graceWindow, applyProbeGuards drops the signal
// (applied=false), the channel closes, and consumeSignals' post-loop
// path must (1) remove the stranded active entry (F1) AND (2) re-arm
// a fresh generation if currentStatus still gates the intent — otherwise
// a codex that dies within graceWindow is permanently undetected
// because there's no future hook to trigger lifecycle.
//
// The detector emits exactly once; subsequent arms block on ctx so the
// rearm cycle stays bounded (production graceWindow expiry breaks the
// cycle within 2-3 detector polls).
func TestConsumeSignals_GraceWindowDrop_RearmsAfterTeardown(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}

	var emitCount atomic.Int64
	m.probeIntentDisp.startDetector = func(ctx context.Context, _ *Module, _ agentpkg.ProbeIntentKind, _ string, _ int, out chan<- agentpkg.Signal) {
		// Only the first arm emits — subsequent rearms block so the test
		// can settle on a single rearm without runaway loops.
		if emitCount.Add(1) == 1 {
			select {
			case out <- agentpkg.Signal{
				Kind:      agentpkg.ProbeIntentKindProcessDead,
				PaneAlive: true,
				PaneID:    "%5",
				SenderPID: 4242,
			}:
			case <-ctx.Done():
			}
			return
		}
		<-ctx.Done()
	}
	t.Cleanup(func() { m.probeIntentDisp.stopAll() })

	seedRunningFrame(t, m, "work", "%5", "codex", 4242)
	// Open graceWindow: probe signal in the next probeGraceWindow is
	// suppressed. The F1 fix tears down + the round-3 follow-up rearms.
	m.probeOrch.recordHookAt("work")

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	// Capture gen 1 (first arm) before the teardown+rearm cycle completes.
	var gen1 uint64
	waitFor(t, time.Second, func() bool {
		cur, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		if !ok {
			return false
		}
		gen1 = cur.generation
		return gen1 > 0
	}, "gen 1 active entry observed before teardown")

	// Wait for the rearm: emitCount becomes 2 (second arm) AND active
	// entry's generation has advanced past gen 1. With the round-3 fix
	// the post-loop teardown calls applyStatus → applyIntentLifecycle
	// arms a fresh gen.
	waitFor(t, 2*time.Second, func() bool {
		cur, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		if !ok {
			return false
		}
		return cur.generation > gen1 && emitCount.Load() >= 2
	}, "rearm with new generation after graceWindow drop (F1 round-3 follow-up)")
}

// TestConsumeSignals_ZeroEmitDetectorExit_TearsDownAndRearms pins the
// generic dispatcher hygiene: any detector that returns WITHOUT
// emitting any Signal — for example a one-shot detector that observes
// no qualifying event before its ctx cancel, or a guard-drop path
// that bails before any Signal is produced — must trigger
// consumeSignals' zero-emit teardown (`!appliedAny` branch), which
// removes the active intent so a subsequent applyStatus on the same
// OnEntryStatus can rearm a fresh detector.
//
// Note (W6-6 R5 F7): the W6-6 R4 F6 "ScreenChange watchLoop
// baseline-fail observed via wh.Done()" case that originally
// motivated this test was reverted by F7. Baseline capture failure
// is now retried inside the probe layer rather than surfacing as a
// detector lifecycle event, so the screen-change detector no longer
// returns zero-emit on transient capture failures. This pin still
// matters as a Kind-agnostic dispatcher hygiene guard: it prevents
// any future detector from silently stranding its active intent if
// it returns without emitting and without lifecycle ctx cancel.
//
// Without this end-to-end pin, regressing the dispatcher's existing
// `if !appliedAny` teardown (or removing the rearm via applyStatus)
// would silently strand a watcherless intent: the active entry would
// persist, case-4 lifecycle target match would short-circuit every
// subsequent Waiting hook, and qualifying events would be missed
// indefinitely until an unrelated lifecycle cancellation.
//
// Test scaffolding:
//   - detector stub returns immediately (zero emits) on first arm,
//     blocks on ctx for subsequent arms — bounding the rearm cycle.
//   - applyStatus(Waiting) arms gen 1.
//   - wrap goroutine close(out) → consumeSignals exits the for-range
//     loop with appliedAny=false → teardown branch fires.
//   - currentStatus still in OnEntryStatus → applyStatus rearms a
//     fresh gen 2.
//
// Test asserts:
//   - gen 2 active entry observed (rearm fired)
//   - second arm's startCount fired
//   - MetricProbeIntentStopped advanced (via the
//     emitStopObservability path on the teardown branch)
func TestConsumeSignals_ZeroEmitDetectorExit_TearsDownAndRearms(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}

	// Detector counter — first arm returns immediately (zero emit),
	// subsequent arms block on ctx so the rearm cycle settles.
	var armCount atomic.Int64
	m.probeIntentDisp.startDetector = func(ctx context.Context, _ *Module, _ agentpkg.ProbeIntentKind, _ string, _ int, _ chan<- agentpkg.Signal) {
		if armCount.Add(1) == 1 {
			// First arm: return immediately without emitting → wrap
			// goroutine close(out) → consumeSignals !appliedAny path.
			return
		}
		// Second+ arm: block on ctx so the test can observe the rearm
		// without runaway loops.
		<-ctx.Done()
	}
	t.Cleanup(func() { m.probeIntentDisp.stopAll() })

	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	stoppedBefore := metricInt("purdex_probe_intent_stopped_total")

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	// Capture gen 1 (first arm) BEFORE the teardown+rearm cycle
	// completes. Note: the first-arm goroutine returns immediately, so
	// gen 1 may already be torn down by the time we look — accept either
	// state but require gen2 strictly greater than gen1.
	var gen1 uint64
	waitFor(t, time.Second, func() bool {
		// First arm started — count >= 1
		return armCount.Load() >= 1
	}, "first detector arm observed")

	// Wait for rearm: armCount becomes 2 (second arm) AND active entry's
	// generation is greater than 0 (rearm installed gen 2).
	waitFor(t, 2*time.Second, func() bool {
		if armCount.Load() < 2 {
			return false
		}
		cur, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		if !ok {
			return false
		}
		gen1 = cur.generation
		return gen1 > 0
	}, "rearm with new generation after zero-emit detector exit (R4 F6 acceptance)")

	// Stopped metric must have advanced (teardown emitted observability).
	stoppedAfter := metricInt("purdex_probe_intent_stopped_total")
	if delta := stoppedAfter - stoppedBefore; delta < 1 {
		t.Errorf("MetricProbeIntentStopped delta = %d, want >= 1 (teardown must emit)", delta)
	}
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

// TestApplyIntentLifecycle_NilTraceSink_DoesNotPanic pins F9: Module.New
// treats trace-store init failure as non-fatal and leaves traceSink nil.
// All ProbeIntent observability sites (start / stop / signal /
// unsupported-kind / drop) MUST guard the nil — otherwise any codex
// running/waiting transition in degraded mode panics the agent module.
//
// This test exercises the start path specifically because it was the
// only site missing the guard before round-5; the others were already
// covered by the existing observability tests with non-nil traceSink.
func TestApplyIntentLifecycle_NilTraceSink_DoesNotPanic(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)
	// Simulate degraded-trace mode (Module.New non-fatal trace init failure).
	m.traceSink = nil

	// Arming exercises the start observability emit which previously
	// dereferenced traceSink unconditionally. Pre-fix: panics here.
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	<-rec.started
	if rec.startCount() != 1 {
		t.Fatalf("detector started count = %d, want 1 (arm must succeed under nil traceSink)", rec.startCount())
	}
}

// -----------------------------------------------------------------------------
// J3 P1-T4: pre-grace bidirectional graceWindow table-driven tests
// -----------------------------------------------------------------------------

// preGraceMetricSnapshot reads the J3 pre-grace counters plus the
// supporting (signalEmitted / applied / droppedGrace / graceWindowSup)
// counters used by the table assertions. expvar globals persist across
// tests — only deltas are stable.
type preGraceMetricSnapshot struct {
	signalEmitted    int64
	preGraceHeld     int64
	droppedPreGrace  int64
	preGraceCanceled int64
	applied          int64
	droppedGrace     int64
	graceWindowSup   int64
}

func snapshotPreGraceMetrics() preGraceMetricSnapshot {
	return preGraceMetricSnapshot{
		signalEmitted:    metricInt("purdex_probe_intent_signal_emitted_total"),
		preGraceHeld:     metricInt("purdex_probe_intent_pre_grace_held_total"),
		droppedPreGrace:  metricInt("purdex_probe_intent_dropped_pre_grace_total"),
		preGraceCanceled: metricInt("purdex_probe_intent_pre_grace_canceled_total"),
		applied:          metricInt("purdex_probe_intent_applied_total"),
		droppedGrace:     metricInt("purdex_probe_intent_dropped_grace_total"),
		graceWindowSup:   metricInt("purdex_probe_grace_window_suppressed_total"),
	}
}

// installPreGraceEmitOnceDetector installs a detector that emits exactly
// one Signal on first arm via emit, then waits for ctx.Done. Subsequent
// arms (e.g. post-applied teardown + rearm in case 1) block on ctx.
//
// Note: this helper does NOT expose a "detector sent" gate. PR round-2
// review (job 019de4d9 high + 019de4db high) flagged that gate-on-send
// + Sleep is unreliable: a buffered-channel send returning success only
// proves the Signal landed in the buffer, not that consumeSignals has
// read it, captured signalAt, and entered the 300ms timer. Tests must
// instead wait for `MetricProbeIntentPreGraceHeld` to advance past
// baseline (see waitForConsumerInHold) — that increment fires
// immediately after signalAt is captured, providing a deterministic
// happens-before edge without any production seam.
func installPreGraceEmitOnceDetector(
	t *testing.T,
	m *Module,
	sig agentpkg.Signal,
) {
	t.Helper()
	var emitted atomic.Int32
	m.probeIntentDisp.startDetector = func(ctx context.Context, _ *Module, _ agentpkg.ProbeIntentKind, _ string, _ int, out chan<- agentpkg.Signal) {
		if emitted.Add(1) == 1 {
			select {
			case out <- sig:
			case <-ctx.Done():
				return
			}
		}
		<-ctx.Done()
	}
	t.Cleanup(func() { m.probeIntentDisp.stopAll() })
}

// waitForConsumerInHold blocks until MetricProbeIntentPreGraceHeld
// advances past baseline, proving that consumeSignals has read the
// next Signal, captured signalAt via orchNowFn, and entered the
// 300ms timer/select block. Subsequent recordHookAt / stopAll calls
// are then deterministic w.r.t. the pre-grace decision boundary.
func waitForConsumerInHold(t *testing.T, baseline int64) {
	t.Helper()
	waitFor(t, time.Second, func() bool {
		return metricInt("purdex_probe_intent_pre_grace_held_total") > baseline
	}, "consumeSignals captured signalAt and entered pre-grace hold")
}

// TestConsumeSignals_PreGrace_Table covers J3 spec §4.1 P1-T4 cases 1-5
// (R13 boundary race / case 6 is acceptable as known limitation per spec
// §2.3 R13; mlab A9 quantified threshold gates production漏網率).
//
// Common assertions per case (per plan §P1-T4):
//   - signalEmitted +1 (existing dispatcher metric preserved)
//   - preGraceHeld +1 (every Signal entering the hold)
//
// Case-specific deltas validate the drop reason routing — since
// probeIntentOnDropForSession is a package-level closure factory with
// no production seam (per v7 trim), reason is asserted via the
// exclusive metric ↔ reason mapping rather than by intercepting the
// callback (per plan-review P2 fix).
func TestConsumeSignals_PreGrace_Table(t *testing.T) {
	t.Run("case_1_no_hook_proceeds_to_apply", func(t *testing.T) {
		m := newDispatcherTestModule(t)
		m.sessions = &fakeSessionProvider{}
		// Detector emits PaneAlive=true on first arm → OnSignal returns
		// Error → applyProbeGuards step 4 broadcasts → applied=true.
		installPreGraceEmitOnceDetector(t, m, agentpkg.Signal{
			Kind:      agentpkg.ProbeIntentKindProcessDead,
			PaneAlive: true,
			PaneID:    "%5",
			SenderPID: 4242,
		})
		seedRunningFrame(t, m, "work", "%5", "codex", 4242)

		before := snapshotPreGraceMetrics()
		m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

		// applyProbeGuards step 4 mutates currentStatus = Error → case 3
		// teardown removes the active entry. waitFor must wait long
		// enough to clear the 300ms pre-grace hold.
		waitFor(t, 2*time.Second, func() bool {
			_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
			return !ok
		}, "active entry torn down after applied=true (case 1: no hook → apply)")

		after := snapshotPreGraceMetrics()
		if got := after.signalEmitted - before.signalEmitted; got != 1 {
			t.Errorf("signalEmitted delta = %d, want 1", got)
		}
		if got := after.preGraceHeld - before.preGraceHeld; got != 1 {
			t.Errorf("preGraceHeld delta = %d, want 1", got)
		}
		if got := after.droppedPreGrace - before.droppedPreGrace; got != 0 {
			t.Errorf("droppedPreGrace delta = %d, want 0", got)
		}
		if got := after.preGraceCanceled - before.preGraceCanceled; got != 0 {
			t.Errorf("preGraceCanceled delta = %d, want 0", got)
		}
		if got := after.applied - before.applied; got != 1 {
			t.Errorf("applied delta = %d, want 1 (signal must reach apply)", got)
		}

		m.mu.Lock()
		gotStatus := m.currentStatus["work"]
		m.mu.Unlock()
		if gotStatus != agentpkg.StatusError {
			t.Fatalf("currentStatus = %q, want error after pre-grace pass + apply", gotStatus)
		}
	})

	t.Run("case_2_hook_during_hold_drops_pre_grace", func(t *testing.T) {
		m := newDispatcherTestModule(t)
		m.sessions = &fakeSessionProvider{}
		installPreGraceEmitOnceDetector(t, m, agentpkg.Signal{
			Kind:      agentpkg.ProbeIntentKindProcessDead,
			PaneAlive: true,
			PaneID:    "%5",
			SenderPID: 4242,
		})
		seedRunningFrame(t, m, "work", "%5", "codex", 4242)

		before := snapshotPreGraceMetrics()
		m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

		// Wait for consumer to capture signalAt + enter the 300ms timer
		// (deterministic via metric handshake — no scheduler-dependent
		// Sleep). Then recordHookAt is guaranteed lastHookAt > signalAt
		// because monotonic time advances between the metric increment
		// and this call → pre-grace drop branch fires at timer expiry.
		waitForConsumerInHold(t, before.preGraceHeld)
		m.probeOrch.recordHookAt("work")

		// Timer expires at ~300ms → pre-grace check sees lastHookAt >
		// signalAt → drop. Active entry stays armed (no apply) until
		// detector ctx cancel. Verify by waiting just past hold + a
		// scheduler grace.
		waitFor(t, 2*time.Second, func() bool {
			now := snapshotPreGraceMetrics()
			return (now.droppedPreGrace - before.droppedPreGrace) >= 1
		}, "droppedPreGrace +1 after timer expires with hook present")

		after := snapshotPreGraceMetrics()
		if got := after.signalEmitted - before.signalEmitted; got != 1 {
			t.Errorf("signalEmitted delta = %d, want 1", got)
		}
		if got := after.preGraceHeld - before.preGraceHeld; got != 1 {
			t.Errorf("preGraceHeld delta = %d, want 1", got)
		}
		if got := after.droppedPreGrace - before.droppedPreGrace; got != 1 {
			t.Errorf("droppedPreGrace delta = %d, want 1", got)
		}
		if got := after.preGraceCanceled - before.preGraceCanceled; got != 0 {
			t.Errorf("preGraceCanceled delta = %d, want 0", got)
		}
		if got := after.applied - before.applied; got != 0 {
			t.Errorf("applied delta = %d, want 0 (signal must NOT reach apply)", got)
		}
		// status untouched by probe — stays Running (set by seedRunningFrame).
		m.mu.Lock()
		gotStatus := m.currentStatus["work"]
		m.mu.Unlock()
		if gotStatus != agentpkg.StatusRunning {
			t.Fatalf("currentStatus = %q, want running (probe must not flip)", gotStatus)
		}
	})

	t.Run("case_3_ctx_cancel_during_hold_drops_pre_cancel", func(t *testing.T) {
		m := newDispatcherTestModule(t)
		m.sessions = &fakeSessionProvider{}
		installPreGraceEmitOnceDetector(t, m, agentpkg.Signal{
			Kind:      agentpkg.ProbeIntentKindProcessDead,
			PaneAlive: true,
			PaneID:    "%5",
			SenderPID: 4242,
		})
		seedRunningFrame(t, m, "work", "%5", "codex", 4242)

		before := snapshotPreGraceMetrics()
		m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

		// Wait for consumer in hold, then cancel ctx without recording
		// any hook — ctx.Done branch sees lastHookAt absent and routes
		// to MetricProbeIntentPreGraceCanceled (genuine lifecycle
		// cancel, not hook race). Tests the no-hook leg of the new
		// classifyAsHookRace logic.
		waitForConsumerInHold(t, before.preGraceHeld)
		m.probeIntentDisp.stopAll()

		waitFor(t, 2*time.Second, func() bool {
			now := snapshotPreGraceMetrics()
			return (now.preGraceCanceled - before.preGraceCanceled) >= 1
		}, "preGraceCanceled +1 after ctx cancel during hold")

		after := snapshotPreGraceMetrics()
		if got := after.signalEmitted - before.signalEmitted; got != 1 {
			t.Errorf("signalEmitted delta = %d, want 1", got)
		}
		if got := after.preGraceHeld - before.preGraceHeld; got != 1 {
			t.Errorf("preGraceHeld delta = %d, want 1", got)
		}
		if got := after.preGraceCanceled - before.preGraceCanceled; got != 1 {
			t.Errorf("preGraceCanceled delta = %d, want 1", got)
		}
		if got := after.droppedPreGrace - before.droppedPreGrace; got != 0 {
			t.Errorf("droppedPreGrace delta = %d, want 0", got)
		}
		if got := after.applied - before.applied; got != 0 {
			t.Errorf("applied delta = %d, want 0 (signal must NOT reach apply)", got)
		}
	})

	t.Run("case_4_hook_immediately_after_hold_start_drops_pre_grace", func(t *testing.T) {
		// Same metric outcome as case 2; pins the no-tail-delay variant
		// (recordHookAt fires the moment consumer enters hold). With
		// monotonic clock, lastHookAt > signalAt holds even at the
		// minimum delta the scheduler can produce → pre-grace drop.
		m := newDispatcherTestModule(t)
		m.sessions = &fakeSessionProvider{}
		installPreGraceEmitOnceDetector(t, m, agentpkg.Signal{
			Kind:      agentpkg.ProbeIntentKindProcessDead,
			PaneAlive: true,
			PaneID:    "%5",
			SenderPID: 4242,
		})
		seedRunningFrame(t, m, "work", "%5", "codex", 4242)

		before := snapshotPreGraceMetrics()
		m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

		waitForConsumerInHold(t, before.preGraceHeld)
		// recordHookAt fires immediately — no Sleep — yet lastHookAt is
		// strictly > signalAt because monotonic time has already
		// advanced between the metric increment and this call.
		m.probeOrch.recordHookAt("work")

		waitFor(t, 2*time.Second, func() bool {
			now := snapshotPreGraceMetrics()
			return (now.droppedPreGrace - before.droppedPreGrace) >= 1
		}, "droppedPreGrace +1 (early hook race)")

		after := snapshotPreGraceMetrics()
		if got := after.signalEmitted - before.signalEmitted; got != 1 {
			t.Errorf("signalEmitted delta = %d, want 1", got)
		}
		if got := after.preGraceHeld - before.preGraceHeld; got != 1 {
			t.Errorf("preGraceHeld delta = %d, want 1", got)
		}
		if got := after.droppedPreGrace - before.droppedPreGrace; got != 1 {
			t.Errorf("droppedPreGrace delta = %d, want 1", got)
		}
		if got := after.applied - before.applied; got != 0 {
			t.Errorf("applied delta = %d, want 0", got)
		}
	})

	t.Run("case_5_pre_grace_pass_post_grace_window_catches", func(t *testing.T) {
		// Pre-existing hook (lastHookAt < signalAt) → pre-grace check
		// returns false (`last.After(signalAt) == false`) → signal
		// proceeds into applyProbeGuards → step 2's existing post-
		// direction graceWindow sees `now - last < 2s` → drop with
		// reason="grace" → MetricProbeGraceWindowSuppressed +1 AND
		// MetricProbeIntentDroppedGrace +1 (dual-counter pattern per
		// applyProbeGuards step 2 + probeIntentOnDropForSession switch).
		m := newDispatcherTestModule(t)
		m.sessions = &fakeSessionProvider{}
		installPreGraceEmitOnceDetector(t, m, agentpkg.Signal{
			Kind:      agentpkg.ProbeIntentKindProcessDead,
			PaneAlive: true,
			PaneID:    "%5",
			SenderPID: 4242,
		})
		seedRunningFrame(t, m, "work", "%5", "codex", 4242)

		// Hook BEFORE the signal — emulate the standard post-direction
		// race that the legacy 2s graceWindow already handled. The
		// dispatcher's new pre-hold must not regress this case.
		m.probeOrch.recordHookAt("work")

		before := snapshotPreGraceMetrics()
		m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

		// Wait for both pre-grace pass-through (300ms hold) and the
		// applyProbeGuards step 2 drop.
		waitFor(t, 2*time.Second, func() bool {
			now := snapshotPreGraceMetrics()
			return (now.droppedGrace - before.droppedGrace) >= 1
		}, "droppedGrace +1 via post graceWindow after pre-grace pass-through")

		after := snapshotPreGraceMetrics()
		if got := after.signalEmitted - before.signalEmitted; got != 1 {
			t.Errorf("signalEmitted delta = %d, want 1", got)
		}
		if got := after.preGraceHeld - before.preGraceHeld; got != 1 {
			t.Errorf("preGraceHeld delta = %d, want 1", got)
		}
		if got := after.droppedPreGrace - before.droppedPreGrace; got != 0 {
			t.Errorf("droppedPreGrace delta = %d, want 0 (pre-grace must pass-through)", got)
		}
		if got := after.preGraceCanceled - before.preGraceCanceled; got != 0 {
			t.Errorf("preGraceCanceled delta = %d, want 0", got)
		}
		// Existing dual-count behavior at step 2: legacy
		// MetricProbeGraceWindowSuppressed AND ProbeIntent-side
		// MetricProbeIntentDroppedGrace both increment.
		if got := after.graceWindowSup - before.graceWindowSup; got != 1 {
			t.Errorf("graceWindowSup delta = %d, want 1 (post graceWindow drop)", got)
		}
		if got := after.droppedGrace - before.droppedGrace; got != 1 {
			t.Errorf("droppedGrace delta = %d, want 1 (probe-intent reason routing)", got)
		}
		if got := after.applied - before.applied; got != 0 {
			t.Errorf("applied delta = %d, want 0 (post graceWindow blocks apply)", got)
		}
	})

	t.Run("case_6_ctx_cancel_after_hook_classifies_as_pre_grace", func(t *testing.T) {
		// PR round-2 體質 finding: real reject path goes
		//   handler.recordHookAt → applyStatus → reconcileSessionActive
		//   → ctx cancel
		// in a single goroutine, so when ctx.Done wins inside
		// consumeSignals, lastHookAt is ALREADY set with a value >
		// signalAt. Pre-fix logic always recorded
		// MetricProbeIntentPreGraceCanceled, conflating hook race
		// with genuine lifecycle cancel. Post-fix: ctx.Done branch
		// calls classifyAsHookRace and routes to
		// MetricProbeIntentDroppedPreGrace when a same-session hook
		// arrived before the cancel.
		m := newDispatcherTestModule(t)
		m.sessions = &fakeSessionProvider{}
		installPreGraceEmitOnceDetector(t, m, agentpkg.Signal{
			Kind:      agentpkg.ProbeIntentKindProcessDead,
			PaneAlive: true,
			PaneID:    "%5",
			SenderPID: 4242,
		})
		seedRunningFrame(t, m, "work", "%5", "codex", 4242)

		before := snapshotPreGraceMetrics()
		m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

		// Consumer in hold, then simulate handler ordering:
		//   recordHookAt happens-before ctx cancel (handler.go:380-413)
		waitForConsumerInHold(t, before.preGraceHeld)
		m.probeOrch.recordHookAt("work")
		m.probeIntentDisp.stopAll()

		waitFor(t, 2*time.Second, func() bool {
			now := snapshotPreGraceMetrics()
			return (now.droppedPreGrace - before.droppedPreGrace) >= 1
		}, "droppedPreGrace +1 via ctx.Done branch hook-race classification")

		after := snapshotPreGraceMetrics()
		if got := after.signalEmitted - before.signalEmitted; got != 1 {
			t.Errorf("signalEmitted delta = %d, want 1", got)
		}
		if got := after.preGraceHeld - before.preGraceHeld; got != 1 {
			t.Errorf("preGraceHeld delta = %d, want 1", got)
		}
		if got := after.droppedPreGrace - before.droppedPreGrace; got != 1 {
			t.Errorf("droppedPreGrace delta = %d, want 1 (ctx.Done + hook race)", got)
		}
		if got := after.preGraceCanceled - before.preGraceCanceled; got != 0 {
			t.Errorf("preGraceCanceled delta = %d, want 0 (must NOT be classified as canceled when hook arrived)", got)
		}
		if got := after.applied - before.applied; got != 0 {
			t.Errorf("applied delta = %d, want 0", got)
		}
	})
}

// TestConsumeSignals_PreGraceDrop_RearmsAfterTeardown pins J3 P2-T1
// regression: when pre-grace drops a Signal (hook arrived during the
// 300ms hold) and the one-shot detector returns immediately after
// emitting, the consumer's `range in` loop exits with appliedAny=false,
// triggering the existing post-loop teardown + rearm cycle (F1
// round-3 follow-up). The new generation must arm under live status
// (currentStatus still gates the intent), proving pre-grace drop is
// behaviorally equivalent to the post graceWindow drop case for the
// rearm path.
//
// Without the rearm, a codex that had a single probe signal cancelled
// by an unrelated hook would leave the active entry stranded with no
// live detector — exactly the F1 strand failure mode.
func TestConsumeSignals_PreGraceDrop_RearmsAfterTeardown(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}

	var emitCount atomic.Int64
	// Detector emits exactly once on first arm then returns. The
	// goroutine wrapper in applyIntentLifecycle calls close(out) on
	// return, so the consumer's `range in` loop exits → !appliedAny
	// post-loop branch fires.
	m.probeIntentDisp.startDetector = func(ctx context.Context, _ *Module, _ agentpkg.ProbeIntentKind, _ string, _ int, out chan<- agentpkg.Signal) {
		if emitCount.Add(1) == 1 {
			select {
			case out <- agentpkg.Signal{
				Kind:      agentpkg.ProbeIntentKindProcessDead,
				PaneAlive: true,
				PaneID:    "%5",
				SenderPID: 4242,
			}:
			case <-ctx.Done():
			}
			return
		}
		<-ctx.Done()
	}
	t.Cleanup(func() { m.probeIntentDisp.stopAll() })

	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	before := snapshotPreGraceMetrics()
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	// Capture gen 1 before teardown completes.
	var gen1 uint64
	waitFor(t, time.Second, func() bool {
		cur, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		if !ok {
			return false
		}
		gen1 = cur.generation
		return gen1 > 0
	}, "gen 1 active entry observed before pre-grace teardown")

	// Inject hook DURING the 300ms hold so pre-grace drops the signal.
	// Use deterministic metric handshake (PR round-4 P2 finding) — wait
	// for MetricProbeIntentPreGraceHeld to advance past baseline,
	// proving consumer captured signalAt + entered timer; then
	// recordHookAt's monotonic timestamp is strictly > signalAt → falls
	// into pre-grace post-check branch, not post-grace pre-check.
	waitForConsumerInHold(t, before.preGraceHeld)
	m.probeOrch.recordHookAt("work")

	// Wait for: pre-grace drop +1 AND emitCount becomes 2 (rearm fired)
	// AND active entry's generation has advanced past gen1.
	waitFor(t, 3*time.Second, func() bool {
		now := snapshotPreGraceMetrics()
		if (now.droppedPreGrace - before.droppedPreGrace) < 1 {
			return false
		}
		cur, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		if !ok {
			return false
		}
		return cur.generation > gen1 && emitCount.Load() >= 2
	}, "pre-grace drop teardown + rearm with new generation (J3 P2-T1)")

	after := snapshotPreGraceMetrics()
	if got := after.droppedPreGrace - before.droppedPreGrace; got < 1 {
		t.Errorf("droppedPreGrace delta = %d, want >= 1", got)
	}
	// Note: signalEmitted / preGraceHeld may exceed 1 because the rearm
	// cycle re-runs (subsequent emits block via the emitCount guard,
	// but the second arm still increments preGraceHeld once if the
	// recordHookAt grace is still active and consumer enters another
	// hold). Lower-bound assertions only.
	if got := after.signalEmitted - before.signalEmitted; got < 1 {
		t.Errorf("signalEmitted delta = %d, want >= 1", got)
	}
	if got := after.preGraceHeld - before.preGraceHeld; got < 1 {
		t.Errorf("preGraceHeld delta = %d, want >= 1", got)
	}
}

// TestReconcileSessionActive_DuringPreGraceHold_CancelsAndCleans pins
// J3 P2-T3: cross-provider switch (reconcileSessionActive) cancels an
// active ProbeIntent entry whose detector emission is currently inside
// the 300ms pre-grace hold. The hold's ctx.Done branch fires →
// preGraceCanceled +1. The active entry under the old provider is
// removed (cross-provider cleanup), and the new provider's lifecycle
// runs without contamination.
func TestReconcileSessionActive_DuringPreGraceHold_CancelsAndCleans(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}
	installPreGraceEmitOnceDetector(t, m, agentpkg.Signal{
		Kind:      agentpkg.ProbeIntentKindProcessDead,
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 4242,
	})
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	before := snapshotPreGraceMetrics()
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	// Wait for consumer to capture signalAt + enter hold.
	waitForConsumerInHold(t, before.preGraceHeld)
	// Switch top frame to a different agent (cc-noprobes has no
	// ProbeIntents), then call applyStatus → reconcileSessionActive
	// drops the codex entry (cross-provider stale) BEFORE the hold timer
	// fires (300ms is plenty of head-room for the reconcile path to
	// run synchronously from the test goroutine).
	m.registry.Register(&fakeAgentProvider{typeName: "cc-noprobes"})
	m.probeIntentDisp.applyStatus("work", "cc-noprobes", agentpkg.StatusRunning)

	// preGraceCanceled +1 from the cancelled detector's hold goroutine,
	// and the codex active entry is gone.
	waitFor(t, 2*time.Second, func() bool {
		now := snapshotPreGraceMetrics()
		if (now.preGraceCanceled - before.preGraceCanceled) < 1 {
			return false
		}
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		return !ok
	}, "pre-grace cancel + active entry cleared after cross-provider switch")

	after := snapshotPreGraceMetrics()
	if got := after.preGraceCanceled - before.preGraceCanceled; got != 1 {
		t.Errorf("preGraceCanceled delta = %d, want 1", got)
	}
	if got := after.droppedPreGrace - before.droppedPreGrace; got != 0 {
		t.Errorf("droppedPreGrace delta = %d, want 0 (must drop pre-cancel, not pre-grace)", got)
	}
	if got := after.applied - before.applied; got != 0 {
		t.Errorf("applied delta = %d, want 0 (signal must NOT reach apply)", got)
	}
}

// TestReplayStatus_TriggersProbeIntent_PreGraceConsistent pins J3 P2-T4
// (spec §1.3 A7): daemon-restart replayStatus → ProbeIntent re-arm
// runs through the same pre-grace 300ms hold as a fresh applyStatus
// arm. With no concurrent hook, the replayed Signal proceeds to
// applyProbeGuards normally; ProcessDead detector latency is bounded
// by the hold + 1Hz poll, well under the W6-3 ≤2s target. The test
// stub-emits immediately to keep timing deterministic — production
// codex polling tail is exercised in P3 mlab live verify.
func TestReplayStatus_TriggersProbeIntent_PreGraceConsistent(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}
	if fake, ok := m.tmux.(*tmux.FakeExecutor); ok {
		fake.SetPaneSessionName("%5", "work")
	}
	installPreGraceEmitOnceDetector(t, m, agentpkg.Signal{
		Kind:      agentpkg.ProbeIntentKindProcessDead,
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 4242,
	})
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	before := snapshotPreGraceMetrics()
	// replayStatus snapshots currentStatus and re-runs applyStatus per
	// session — same path as a live hook would take.
	m.probeIntentDisp.replayStatus()

	// Detector emits → consumer enters hold → no concurrent hook → pre-
	// grace pass-through → applyProbeGuards step 4 broadcasts → applied
	// → consumeSignals re-runs applyStatus(error) → case 3 teardown.
	waitFor(t, 2*time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		return !ok
	}, "active entry torn down after replay-driven pre-grace pass + apply")

	after := snapshotPreGraceMetrics()
	if got := after.signalEmitted - before.signalEmitted; got != 1 {
		t.Errorf("signalEmitted delta = %d, want 1", got)
	}
	if got := after.preGraceHeld - before.preGraceHeld; got != 1 {
		t.Errorf("preGraceHeld delta = %d, want 1 (replay must enter pre-grace)", got)
	}
	if got := after.droppedPreGrace - before.droppedPreGrace; got != 0 {
		t.Errorf("droppedPreGrace delta = %d, want 0 (no concurrent hook)", got)
	}
	if got := after.preGraceCanceled - before.preGraceCanceled; got != 0 {
		t.Errorf("preGraceCanceled delta = %d, want 0", got)
	}
	if got := after.applied - before.applied; got != 1 {
		t.Errorf("applied delta = %d, want 1 (replay path must reach apply)", got)
	}
	m.mu.Lock()
	gotStatus := m.currentStatus["work"]
	m.mu.Unlock()
	if gotStatus != agentpkg.StatusError {
		t.Fatalf("currentStatus = %q, want error after replay-driven apply", gotStatus)
	}
}

// TestConsumeSignals_ProcessDead_PostGraceStillSuppresses pins R3 F5
// regression: the by-Kind post-grace specialization (probeIntentPostGrace
// Window) MUST keep the legacy 2s hook-authority semantics for
// ProcessDead. Without explicit coverage, a future drift that mistakenly
// sets ProcessDead's post-grace to 0 (or removes the default branch
// entirely) would silently regress the W6-3 race-competitor protection
// — ProcessDead detector emits "PaneAlive=false" on its own polling
// schedule, which CAN race a same-session SessionEnd hook; the 2s
// graceWindow is the original hook-authority guard for that race.
//
// This test mirrors TestConsumeSignals_PreGrace_Table case_5 but stays
// as a permanent stand-alone regression so the F5 by-Kind asymmetry
// is explicit (ScreenChange = 0 bypass / ProcessDead = 2s legacy).
func TestConsumeSignals_ProcessDead_PostGraceStillSuppresses(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}
	installPreGraceEmitOnceDetector(t, m, agentpkg.Signal{
		Kind:      agentpkg.ProbeIntentKindProcessDead,
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 4242,
	})
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	// Hook BEFORE the signal — emulates the standard post-direction race
	// where the dispatcher's post-grace window must drop the probe.
	m.probeOrch.recordHookAt("work")

	before := snapshotPreGraceMetrics()
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	// Wait for the dispatcher's post-grace early check to drop
	// (consumeSignals.go: `signalAt.Sub(last) < probeGraceWindow`). For
	// ProcessDead the window stays 2s, so this drop fires immediately
	// when consumeSignals reads the Signal.
	waitFor(t, 2*time.Second, func() bool {
		now := snapshotPreGraceMetrics()
		return (now.droppedGrace - before.droppedGrace) >= 1
	}, "ProcessDead Signal dropped via post graceWindow (legacy 2s)")

	after := snapshotPreGraceMetrics()
	// Must increment BOTH the legacy global counter AND the per-reason
	// ProbeIntent counter (dual-count pattern at consumeSignals' early
	// post-grace check; mirrors applyProbeGuards step 2 dual-count).
	if got := after.graceWindowSup - before.graceWindowSup; got != 1 {
		t.Errorf("MetricProbeGraceWindowSuppressed delta = %d, want 1 (ProcessDead must NOT bypass post-grace)", got)
	}
	if got := after.droppedGrace - before.droppedGrace; got != 1 {
		t.Errorf("MetricProbeIntentDroppedGrace delta = %d, want 1 (ProcessDead must NOT bypass post-grace)", got)
	}
	if got := after.applied - before.applied; got != 0 {
		t.Errorf("MetricProbeIntentApplied delta = %d, want 0 (post graceWindow must block apply)", got)
	}
}
