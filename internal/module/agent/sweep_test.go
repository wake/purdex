package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/agent/arbitrator"
	"github.com/wake/purdex/internal/module/agent/arbmode"
	"github.com/wake/purdex/internal/module/agent/observation"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

func newSweepTestModule(t *testing.T) *Module {
	t.Helper()
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "work-code", Name: "work"}}}
	return m
}

// attachTestArbitrator attaches a lightweight Arbitrator + minter so sweep
// wiring tests (PR-1b-1c T8) can observe SubmitObservation output without
// running the full apply pipeline goroutine. inChCap controls the input
// channel capacity; tests may read from InChForTesting() to snoop the
// observation, or pre-fill the channel to force a submit drop.
func attachTestArbitrator(t *testing.T, m *Module, inChCap int) {
	t.Helper()
	arbitrator.ResetForTesting()
	minter, _ := observation.NewTraceIDRegistry()
	m.traceMinter = minter
	m.arbmodeMgr = arbmode.NewManager("", "")
	m.arbitrator = arbitrator.NewArbitrator(arbitrator.Options{
		Minter:      minter,
		Arbmode:     m.arbmodeMgr,
		TraceWriter: &sweepNopTraceSubmitter{},
		InChCap:     inChCap,
	})
}

// sweepNopTraceSubmitter satisfies arbitrator.TraceSubmitter for sweep tests.
type sweepNopTraceSubmitter struct{}

func (*sweepNopTraceSubmitter) Submit(arbitrator.TraceRecord) {}
func (*sweepNopTraceSubmitter) Shutdown(_ context.Context) error {
	return nil
}

// sweepEvidenceValue returns the Value for the first evidence entry matching
// key, or nil if none. Named with a sweep prefix so it doesn't collide with
// T6's evidenceValue(obs, key) (any, bool) helper in handler_observation_test.go.
func sweepEvidenceValue(obs observation.Observation, key string) interface{} {
	for _, e := range obs.Evidence {
		if e.Key == key {
			return e.Value
		}
	}
	return nil
}

func TestSweep_ClearsDeadFramesByPid(t *testing.T) {
	m := newSweepTestModule(t)
	if err := m.events.Set("work", "Stop", json.RawMessage(`{}`), "cc", 1); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              99999,
		PPID:             1,
		ProcessStartTime: "dead",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	origAlive := isPidAliveFn
	isPidAliveFn = func(pid int) bool { return false }
	t.Cleanup(func() { isPidAliveFn = origAlive })

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0", len(frames))
	}
	ev, err := m.events.Get("work")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev != nil {
		t.Fatalf("legacy event row should be cleared, got %+v", ev)
	}
}

func TestSweep_PreservesLiveFrames(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "live",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "live", nil }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
}

func TestSweep_DetectsPidReuse(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "B", nil }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0", len(frames))
	}
}

func TestSweep_StopWaitsForInFlight(t *testing.T) {
	m := newSweepTestModule(t)
	origInterval := sweepInterval
	origOnce := sweepOnceFn
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	sweepInterval = 10 * time.Millisecond
	sweepOnceFn = func(*Module) {
		started <- struct{}{}
		<-release
	}
	t.Cleanup(func() {
		sweepInterval = origInterval
		sweepOnceFn = origOnce
	})

	if err := m.Start(nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started
	done := make(chan struct{})
	go func() {
		_ = m.Stop(nil)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Stop returned before in-flight sweep completed")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Stop did not wait for sweep goroutine")
	}
}

func TestSweep_ContextCancellationPropagates(t *testing.T) {
	m := newSweepTestModule(t)
	origInterval := sweepInterval
	origOnce := sweepOnceFn
	sweepInterval = 10 * time.Millisecond
	sweepOnceFn = func(*Module) {}
	t.Cleanup(func() {
		sweepInterval = origInterval
		sweepOnceFn = origOnce
	})

	if err := m.Start(nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(nil); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestSweep_DoesNotMassDeleteOnTmuxOutage(t *testing.T) {
	m := newTestModule(t)
	m.tmux = nil
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "live",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "live", nil }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
}

func TestSweep_ClearingFramePreservesSiblings(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame cc: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "codex",
		PID:              300,
		PPID:             200,
		ProcessStartTime: "B",
		Status:           agentpkg.StatusRunning,
		StartedAt:        20,
		LastSeenAt:       20,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame codex: %v", err)
	}
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	isPidAliveFn = func(pid int) bool { return pid != 300 }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 {
			return "A", nil
		}
		return "B", nil
	}
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 1 || frames[0].AgentType != "cc" {
		t.Fatalf("frames = %+v, want only cc sibling preserved", frames)
	}
}

// ----------------------------------------------------------------------
// PR-1b-1c T8: sweep path observation wiring
// ----------------------------------------------------------------------

// readSweepObservation drains one observation from the arbitrator's input
// channel, waiting up to 500ms. Returns the observation or t.Fatal on
// timeout.
func readSweepObservation(t *testing.T, m *Module) observation.Observation {
	t.Helper()
	select {
	case obs := <-m.arbitrator.InChForTesting():
		return obs
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("no observation enqueued on arbitrator InCh within 500ms")
		return observation.Observation{}
	}
}

func TestSweep_PidDead_SubmitsObservation_BeforeClearFrame_IdentityTriplePresent_RoleResolved(t *testing.T) {
	m := newSweepTestModule(t)
	attachTestArbitrator(t, m, 8)

	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              12345,
		PPID:             1,
		ProcessStartTime: "Sun Apr 20 01:30:00 2026",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	origAlive := isPidAliveFn
	isPidAliveFn = func(pid int) bool { return false }
	t.Cleanup(func() { isPidAliveFn = origAlive })

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	obs := readSweepObservation(t, m)

	// SourceKind + phase + action
	if obs.SourceKind != observation.SourceSweep {
		t.Errorf("SourceKind = %q, want %q", obs.SourceKind, observation.SourceSweep)
	}
	if obs.Phase != observation.PhaseProposed {
		t.Errorf("Phase = %q, want %q", obs.Phase, observation.PhaseProposed)
	}
	if obs.Action != "sweep.frame_cleared" {
		t.Errorf("Action = %q, want sweep.frame_cleared", obs.Action)
	}

	// Proposal carries EndLifecycle + reason = pid_dead
	if !obs.Proposal.EndLifecycle {
		t.Errorf("Proposal.EndLifecycle = false, want true")
	}
	if obs.Proposal.EndReason != "pid_dead" {
		t.Errorf("Proposal.EndReason = %q, want pid_dead", obs.Proposal.EndReason)
	}

	// Identity triple
	if got := sweepEvidenceValue(obs, "pid"); got != int64(12345) {
		t.Errorf("evidence[pid] = %v (%T), want int64(12345)", got, got)
	}
	if got := sweepEvidenceValue(obs, "pane_id"); got != "%5" {
		t.Errorf("evidence[pane_id] = %v, want %%5", got)
	}
	if got := sweepEvidenceValue(obs, "start_time"); got != "Sun Apr 20 01:30:00 2026" {
		t.Errorf("evidence[start_time] = %v, want Sun Apr 20 01:30:00 2026", got)
	}
	if got := sweepEvidenceValue(obs, "reason"); got != "pid_dead" {
		t.Errorf("evidence[reason] = %v, want pid_dead", got)
	}

	// Role resolution = RoleResolved (identity triple all present)
	if got := sweepEvidenceValue(obs, observation.EvidenceKeyRoleResolution); got != observation.RoleResolved {
		t.Errorf("evidence[role_resolution] = %v, want RoleResolved", got)
	}

	// clearFrame actually ran — frame removed from store
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0 (clearFrame must have run)", len(frames))
	}
}

func TestSweep_PidReused_SubmitsObservationWithReusedReason(t *testing.T) {
	m := newSweepTestModule(t)
	attachTestArbitrator(t, m, 8)

	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "B", nil }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	obs := readSweepObservation(t, m)

	if obs.Proposal.EndReason != "pid_reused" {
		t.Errorf("Proposal.EndReason = %q, want pid_reused", obs.Proposal.EndReason)
	}
	if got := sweepEvidenceValue(obs, "reason"); got != "pid_reused" {
		t.Errorf("evidence[reason] = %v, want pid_reused", got)
	}
	if got := sweepEvidenceValue(obs, observation.EvidenceKeyRoleResolution); got != observation.RoleResolved {
		t.Errorf("evidence[role_resolution] = %v, want RoleResolved", got)
	}

	// clearFrame ran
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0", len(frames))
	}
}

func TestSweep_FrameIdentityMissing_RoleTerminalUnresolved(t *testing.T) {
	// A frame with empty ProcessStartTime reaches clearFrame (e.g. via
	// dedicated paths that don't require the sweep identity check). We call
	// clearFrame directly with reason="pid_dead" — the observation must be
	// tagged RoleTerminalUnresolved by the T4 builder because start_time is
	// empty.
	m := newSweepTestModule(t)
	attachTestArbitrator(t, m, 8)

	frame := store.Frame{
		FrameID:          "frame-identity-missing",
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              99999,
		PPID:             1,
		ProcessStartTime: "", // identity missing
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}
	if _, err := m.frames.Upsert(frame); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}

	if err := m.clearFrame(frame, "pid_dead"); err != nil {
		t.Fatalf("clearFrame: %v", err)
	}

	obs := readSweepObservation(t, m)

	if got := sweepEvidenceValue(obs, observation.EvidenceKeyRoleResolution); got != observation.RoleTerminalUnresolved {
		t.Errorf("evidence[role_resolution] = %v, want RoleTerminalUnresolved (empty start_time)", got)
	}
}

func TestSweep_SubmitFails_ClearFrameStillRuns(t *testing.T) {
	// Saturate the arbitrator input channel so a SubmitObservation call for
	// a PhaseProposed (sweep) observation drops on the fast-path. clearFrame
	// must still run and delete the frame — D9 fail-open contract.
	m := newSweepTestModule(t)
	attachTestArbitrator(t, m, 1)

	// Fill the channel so proposed (sweep) observations drop immediately.
	ch := m.arbitrator.InChForTesting()
	ch <- observation.Observation{SessionID: "filler", Phase: observation.PhaseProposed}

	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              12345,
		PPID:             1,
		ProcessStartTime: "live",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	origAlive := isPidAliveFn
	isPidAliveFn = func(pid int) bool { return false }
	t.Cleanup(func() { isPidAliveFn = origAlive })

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	// clearFrame still ran — frame gone.
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0 (clearFrame must survive submit drop)", len(frames))
	}

	// And the drop metric incremented — observation never reached Run.
	if got := arbitrator.Value("lights_arb_in_dropped", "priority=proposed"); got == 0 {
		t.Fatalf("lights_arb_in_dropped{priority=proposed} = 0, want >=1 (submit must have dropped)")
	}
}

func TestSweep_ObservationHasSessionCode(t *testing.T) {
	// Frame has no SessionCode field; T8 wiring must populate
	// Observation.SessionID by resolving pane -> session name -> session code
	// via resolvePaneSession.
	m := newSweepTestModule(t)
	attachTestArbitrator(t, m, 8)

	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              12345,
		PPID:             1,
		ProcessStartTime: "alive",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	origAlive := isPidAliveFn
	isPidAliveFn = func(pid int) bool { return false }
	t.Cleanup(func() { isPidAliveFn = origAlive })

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	obs := readSweepObservation(t, m)
	if obs.SessionID != "work-code" {
		t.Fatalf("Observation.SessionID = %q, want %q (resolved from pane %%5 -> session 'work' -> code 'work-code')",
			obs.SessionID, "work-code")
	}
}

func TestSweep_ObservedGenerationIsCurrentGen_NotAdvanced(t *testing.T) {
	// Sweep never advances generation. The builder must emit
	// observedGeneration == arbitrator.CurrentGeneration(sessionCode).
	// For a session that never had a SessionStart, CurrentGeneration == 0.
	m := newSweepTestModule(t)
	attachTestArbitrator(t, m, 8)

	if got := m.arbitrator.CurrentGeneration("work-code"); got != 0 {
		t.Fatalf("precondition: CurrentGeneration(work-code) = %d, want 0", got)
	}

	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              12345,
		PPID:             1,
		ProcessStartTime: "alive",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	origAlive := isPidAliveFn
	isPidAliveFn = func(pid int) bool { return false }
	t.Cleanup(func() { isPidAliveFn = origAlive })

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	obs := readSweepObservation(t, m)
	if obs.ObservedGeneration != 0 {
		t.Fatalf("ObservedGeneration = %d, want 0 (sweep does not advance gen)", obs.ObservedGeneration)
	}

	// CurrentGeneration must not have moved either.
	if got := m.arbitrator.CurrentGeneration("work-code"); got != 0 {
		t.Fatalf("post: CurrentGeneration(work-code) = %d, want 0 (sweep must not advance gen)", got)
	}
}
