package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
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
	origNow := nowFn
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "live", nil }
	// Pin "now" close to LastSeenAt so the idle_timeout rule does not fire
	// on fixture frames using a simple sentinel LastSeenAt value.
	nowFn = func() time.Time { return time.Unix(0, 10) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
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
	origNow := nowFn
	isPidAliveFn = func(pid int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) { return "live", nil }
	nowFn = func() time.Time { return time.Unix(0, 10) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
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

// ---------------------------------------------------------------------------
// Idle sweep (Phase 2 PR-2b, plan §1.6 + §1.8 + §2.7)
// ---------------------------------------------------------------------------

// IS1 — frame idle past threshold is cleared with broadcast reason=sweep:idle_timeout.
func TestSweep_ClearsIdleFramesByLastSeen(t *testing.T) {
	m := newSweepTestModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: m.tmux}
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	// Seed frame with LastSeenAt=0; fake nowFn returns 2×threshold so the
	// elapsed delta > threshold triggers the idle_timeout rule.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "live",
		Status:           agentpkg.StatusIdle,
		StartedAt:        0,
		LastSeenAt:       0,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "live", nil }
	nowFn = func() time.Time { return time.Unix(0, int64(2*frameIdleThreshold)) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0 (idle frame should be cleared)", len(frames))
	}

	// Drain broadcast with reason=sweep:idle_timeout (inside escaped payload).
	select {
	case msg := <-sub.SendCh():
		if !strings.Contains(string(msg), `sweep:idle_timeout`) {
			t.Fatalf("broadcast = %s, want reason sweep:idle_timeout", msg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for idle_timeout broadcast")
	}
}

// IS2 — fresh frame (LastSeenAt within threshold) is preserved.
func TestSweep_PreservesFreshFrames(t *testing.T) {
	m := newSweepTestModule(t)

	// Pretend "now" is 1h — fresh frame LastSeenAt at now-1min (delta < threshold).
	fakeNow := int64(60 * time.Minute)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "live",
		Status:           agentpkg.StatusIdle,
		StartedAt:        fakeNow - int64(time.Minute),
		LastSeenAt:       fakeNow - int64(time.Minute),
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "live", nil }
	nowFn = func() time.Time { return time.Unix(0, fakeNow) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (fresh frame preserved)", len(frames))
	}
}

// IS3 — idle sweep also stops the orphan activity watcher.
func TestSweep_IdleClearStopsOrphanWatcher(t *testing.T) {
	m := newSweepTestModule(t)
	m.prober = probe.New(m.tmux)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "live",
		Status:           agentpkg.StatusIdle,
		StartedAt:        0,
		LastSeenAt:       0,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}

	// Register an active watcher for this session (simulating an in-flight
	// Activity probe left over from a running/waiting status).
	m.activeWatchers["work"] = "cc"
	m.prober.StartWatch("work:", func(string, probe.ActivitySignal) {})
	t.Cleanup(func() { m.prober.StopAllWatches() })

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "live", nil }
	nowFn = func() time.Time { return time.Unix(0, int64(2*frameIdleThreshold)) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	if _, ok := m.activeWatchers["work"]; ok {
		t.Fatal("activeWatchers[work] should be cleared after idle_timeout sweep")
	}
	if m.prober.HasWatcher("work:") {
		t.Fatal("prober watcher for work: should be stopped after idle_timeout sweep")
	}
}

// IS4 — pid_dead path also stops the orphan watcher (regression for the
// pre-PR-2b bug that only deleted the map entry).
func TestSweep_DeadPidAlsoStopsOrphanWatcher(t *testing.T) {
	m := newSweepTestModule(t)
	m.prober = probe.New(m.tmux)
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

	m.activeWatchers["work"] = "cc"
	m.prober.StartWatch("work:", func(string, probe.ActivitySignal) {})
	t.Cleanup(func() { m.prober.StopAllWatches() })

	origAlive := isPidAliveFn
	isPidAliveFn = func(int) bool { return false } // dead
	t.Cleanup(func() { isPidAliveFn = origAlive })

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	if _, ok := m.activeWatchers["work"]; ok {
		t.Fatal("activeWatchers[work] should be cleared after pid_dead sweep")
	}
	if m.prober.HasWatcher("work:") {
		t.Fatal("prober watcher should be stopped after pid_dead sweep (StopWatch leak fix)")
	}
}

// IS5 — concurrent refresh between ListAll and DeleteIfUnchanged causes the
// optimistic sweep to skip the frame rather than clobber the refreshed row.
func TestSweep_IdleConditionalDeleteSkipsOnConcurrentRefresh(t *testing.T) {
	m := newSweepTestModule(t)

	// The frame starts idle (LastSeenAt=0). sweepOnce() will read the list
	// first, then iterate. We simulate a concurrent hook refresh by bumping
	// LastSeenAt between ListAll and the DELETE — achieved by swapping nowFn
	// to run an Upsert on first call.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "live",
		Status:           agentpkg.StatusIdle,
		StartedAt:        0,
		LastSeenAt:       0,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "live", nil }
	// First call (isPidAliveFn before idle check) reads the baseline now; we
	// piggyback on nowFn to force a concurrent Upsert before the DELETE runs.
	var raced bool
	nowFn = func() time.Time {
		if !raced {
			raced = true
			// Simulate concurrent hook refresh before our sweep DELETE.
			_, _ = m.frames.Upsert(store.Frame{
				PaneID:           "%5",
				AgentType:        "cc",
				PID:              200,
				PPID:             1,
				ProcessStartTime: "live",
				Status:           agentpkg.StatusIdle,
				StartedAt:        0,
				LastSeenAt:       int64(30 * time.Minute),
				Verified:         true,
			})
		}
		return time.Unix(0, int64(2*frameIdleThreshold))
	}
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (DeleteIfUnchanged should skip on refresh)", len(frames))
	}
	if frames[0].LastSeenAt != int64(30*time.Minute) {
		t.Fatalf("LastSeenAt = %d, want refreshed value (concurrent Upsert preserved)", frames[0].LastSeenAt)
	}
}

// IS6 — probe-driven status transitions (setProjectionTopStatus) refresh
// LastSeenAt so the idle rule does not mis-classify a live agent at a shell
// prompt as idle. R1 regression: v6 plan assumed hook traffic was the only
// LastSeenAt source, but probe activity is the normal signal for
// waiting/running/idle while hooks may be silent for > 1h.
func TestSweep_PreservesLiveFrameAfterProbeActivity(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "live",
		Status:           agentpkg.StatusRunning,
		StartedAt:        0,
		LastSeenAt:       0, // stale baseline — > 1h in the past from sweep's POV
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}

	probeTime := time.Now().UnixNano()
	if _, err := m.setProjectionTopStatus("work", agentpkg.StatusIdle); err != nil {
		t.Fatalf("setProjectionTopStatus: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "live", nil }
	// Sweep 30 min after probe activity — well within the 1h idle threshold.
	nowFn = func() time.Time { return time.Unix(0, probeTime+int64(30*time.Minute)) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (probe-bumped LastSeenAt keeps frame alive)", len(frames))
	}
	if frames[0].LastSeenAt < probeTime {
		t.Fatalf("LastSeenAt = %d, want >= %d (setProjectionTopStatus should have bumped it)", frames[0].LastSeenAt, probeTime)
	}
}

// IS7 — R7 regression: setProjectionTopStatus does narrow column update
// (status + last_seen_at only), so probe-driven status transitions do not
// clobber concurrent Subagents mutations on the same row. Before the narrow
// update fix, status updates round-tripped a stale Subagents baseline and
// could overwrite refs added by concurrent proxy/native attaches.
func TestSetProjectionTopStatus_DoesNotClobberConcurrentSubagents(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "live",
		Subagents:        []agentpkg.SubagentRef{{ID: "a", Type: "cc", StartedAt: 10}},
		Status:           agentpkg.StatusRunning,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert baseline: %v", err)
	}

	// Simulate a concurrent subagents mutation landing on the row after the
	// probe callback has already read the projection but before its status
	// write. Under the old whole-frame Upsert path, the probe callback's
	// stale Subagents baseline would clobber this write.
	racerBaseline, _ := m.frames.GetByIdentity("%5", 200, "live")
	racerBaseline.Subagents = append(racerBaseline.Subagents,
		agentpkg.SubagentRef{ID: "b", Type: "cc", StartedAt: 20},
		agentpkg.SubagentRef{ID: "c", Type: "cc", StartedAt: 30})
	racerBaseline.LastSeenAt = 30
	if _, err := m.frames.Upsert(*racerBaseline); err != nil {
		t.Fatalf("racer Upsert: %v", err)
	}

	// Probe callback fires. setProjectionTopStatus reads the (now-stale)
	// projection, but under the narrow-update fix it only writes status +
	// last_seen_at — the subagents_json column is untouched.
	if _, err := m.setProjectionTopStatus("work", agentpkg.StatusIdle); err != nil {
		t.Fatalf("setProjectionTopStatus: %v", err)
	}

	final, err := m.frames.GetByIdentity("%5", 200, "live")
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	if final == nil {
		t.Fatal("frame vanished")
	}
	if final.Status != agentpkg.StatusIdle {
		t.Fatalf("Status = %q, want Idle", final.Status)
	}
	if len(final.Subagents) != 3 {
		t.Fatalf("Subagents len = %d, want 3 (concurrent writer's refs preserved); got %+v", len(final.Subagents), final.Subagents)
	}
	ids := make(map[string]bool)
	for _, ref := range final.Subagents {
		ids[ref.ID] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !ids[want] {
			t.Fatalf("Subagents missing ID=%q; subagents_json was clobbered by status write", want)
		}
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
	origNow := nowFn
	isPidAliveFn = func(pid int) bool { return pid != 300 }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 {
			return "A", nil
		}
		return "B", nil
	}
	nowFn = func() time.Time { return time.Unix(0, 20) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
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

// IT13 — sweep pruneDeadProxyRefs detaches a proxy ref whose source
// process is dead. Plan §4.3 (lifted from PR-3.5b into PR-3.5a per v8 L1
// fix). Hot-path SessionEnd is now detach-first + propagate, but a
// daemon crash between the detach and Delete — or a removeProxyRef
// retry exhaustion that the caller logged and continued past — would
// still leave a stale proxy ref permanently lit on the parent without
// this sweep pass.
func TestSweep_PruneDeadProxyRefs_DetachesDeadSource(t *testing.T) {
	m := newSweepTestModule(t)
	// cc parent alive at PID 200 with a codex IsProxy ref whose source
	// (PID 300) is dead. No standalone codex frame — projection_dedup
	// can't help.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "A",
		Subagents: []agentpkg.SubagentRef{{
			ID:              "proxy:codex:300:dead-t300",
			Type:            "codex",
			SourcePID:       300,
			SourceStartTime: "dead-t300",
			IsProxy:         true,
		}},
		Status:     agentpkg.StatusIdle,
		StartedAt:  10,
		LastSeenAt: 10,
		Verified:   true,
	}); err != nil {
		t.Fatalf("Upsert cc + dead proxy ref: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	isPidAliveFn = func(pid int) bool { return pid == 200 }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 {
			return "A", nil
		}
		return "", nil
	}
	nowFn = func() time.Time { return time.Unix(0, 20) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	startMetric := agentpkg.MetricSweepPrunedProxy.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "cc" {
		t.Fatalf("frames = %+v, want cc parent preserved", frames)
	}
	if len(frames[0].Subagents) != 0 {
		t.Fatalf("cc.Subagents = %+v, want empty (dead proxy detached)", frames[0].Subagents)
	}
	if delta := agentpkg.MetricSweepPrunedProxy.Value() - startMetric; delta != 1 {
		t.Fatalf("MetricSweepPrunedProxy delta = %d, want 1", delta)
	}
}

// IT13b — sweep pruneDeadProxyRefs preserves a proxy ref whose source is
// alive and identity-verified. Negative case for the prune pass.
func TestSweep_PruneDeadProxyRefs_KeepsLiveProxy(t *testing.T) {
	m := newSweepTestModule(t)
	// cc parent at PID 200 with codex IsProxy whose source (PID 300) is
	// alive + start_time matches.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "A",
		Subagents: []agentpkg.SubagentRef{{
			ID:              "proxy:codex:300:t300",
			Type:            "codex",
			SourcePID:       300,
			SourceStartTime: "t300",
			IsProxy:         true,
		}},
		Status:     agentpkg.StatusIdle,
		StartedAt:  10,
		LastSeenAt: 10,
		Verified:   true,
	}); err != nil {
		t.Fatalf("Upsert cc + live proxy ref: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 200:
			return "A", nil
		case 300:
			return "t300", nil
		}
		return "", nil
	}
	nowFn = func() time.Time { return time.Unix(0, 20) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	startMetric := agentpkg.MetricSweepPrunedProxy.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
	if len(frames[0].Subagents) != 1 || frames[0].Subagents[0].SourcePID != 300 {
		t.Fatalf("cc.Subagents = %+v, want live codex proxy preserved", frames[0].Subagents)
	}
	if delta := agentpkg.MetricSweepPrunedProxy.Value() - startMetric; delta != 0 {
		t.Fatalf("MetricSweepPrunedProxy delta = %d, want 0 (live proxy kept)", delta)
	}
}

// IT13c — codex round 2 #P1 fix: sweep pruneDeadProxyRefs broadcasts a
// projection update after detaching a stale proxy ref. Previously the
// detach was visible in storage but m.subagents/m.currentStatus and the
// SPA stayed stale until an unrelated hook fired. Sweep prune now emits
// "hook" payload with reason=sweep:proxy_pruned (matching the existing
// idle_timeout / pid_dead broadcast pattern in afterFrameCleared).
func TestSweep_PruneDeadProxyRefs_BroadcastsProjectionAfterDetach(t *testing.T) {
	m := newSweepTestModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: m.tmux}
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "A",
		Subagents: []agentpkg.SubagentRef{{
			ID:              "proxy:codex:300:dead-t300",
			Type:            "codex",
			SourcePID:       300,
			SourceStartTime: "dead-t300",
			IsProxy:         true,
		}},
		Status:     agentpkg.StatusIdle,
		StartedAt:  10,
		LastSeenAt: 10,
		Verified:   true,
	}); err != nil {
		t.Fatalf("Upsert cc + dead proxy ref: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	isPidAliveFn = func(pid int) bool { return pid == 200 }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 {
			return "A", nil
		}
		return "", nil
	}
	nowFn = func() time.Time { return time.Unix(0, 20) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	// Verify storage: proxy ref detached.
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || len(frames[0].Subagents) != 0 {
		t.Fatalf("frames = %+v, want cc with empty subagents (detach storage step)", frames)
	}

	// Verify broadcast: payload contains reason=sweep:proxy_pruned.
	select {
	case msg := <-sub.SendCh():
		if !strings.Contains(string(msg), "sweep:proxy_pruned") {
			t.Fatalf("broadcast payload = %s, want reason sweep:proxy_pruned (P1 broadcast missing)", msg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for sweep:proxy_pruned broadcast — P1 fix missing")
	}
}

// IT14b — codex round 2 #O3 fix: sweep pruneDeadProxyRefs preserves a
// proxy ref when processStartTimeFn returns an error (transient /proc
// read failure / platform probe issue). Previously the fall-through
// after a `sterr != nil` test detached the ref — fail-destructive,
// could falsely reap a live proxy whose start_time read transiently
// failed. Fail-safe: only detach on CONFIRMED dead source or CONFIRMED
// identity mismatch. Read error → keep, retry next sweep.
func TestSweep_PruneDeadProxyRefs_KeepsProxyOnStartTimeError(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "A",
		Subagents: []agentpkg.SubagentRef{{
			ID:              "proxy:codex:300:t300",
			Type:            "codex",
			SourcePID:       300,
			SourceStartTime: "t300",
			IsProxy:         true,
		}},
		Status:     agentpkg.StatusIdle,
		StartedAt:  10,
		LastSeenAt: 10,
		Verified:   true,
	}); err != nil {
		t.Fatalf("Upsert cc + proxy ref: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	// PID 300 alive, but processStartTimeFn returns transient error.
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 {
			return "A", nil
		}
		// PID 300 (the proxy's source): transient read error.
		return "", errStub("ps transient failure")
	}
	nowFn = func() time.Time { return time.Unix(0, 20) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	startMetric := agentpkg.MetricSweepPrunedProxy.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
	if len(frames[0].Subagents) != 1 || frames[0].Subagents[0].SourcePID != 300 {
		t.Fatalf("cc.Subagents = %+v, want proxy preserved on read error (fail-safe)", frames[0].Subagents)
	}
	if delta := agentpkg.MetricSweepPrunedProxy.Value() - startMetric; delta != 0 {
		t.Fatalf("MetricSweepPrunedProxy delta = %d, want 0 (read error must not detach)", delta)
	}
}

// IT14 — sweep pruneDeadProxyRefs detaches a proxy ref whose source PID
// has been reused (alive but stored start_time mismatches actualStart).
// Plan §4.3 PID-reuse case.
func TestSweep_PruneDeadProxyRefs_DetachesPidReused(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "A",
		Subagents: []agentpkg.SubagentRef{{
			ID:              "proxy:codex:300:stale-t300",
			Type:            "codex",
			SourcePID:       300,
			SourceStartTime: "stale-t300",
			IsProxy:         true,
		}},
		Status:     agentpkg.StatusIdle,
		StartedAt:  10,
		LastSeenAt: 10,
		Verified:   true,
	}); err != nil {
		t.Fatalf("Upsert cc + pid-reused proxy ref: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	isPidAliveFn = func(int) bool { return true }
	// PID 300 alive but identity changed.
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 200:
			return "A", nil
		case 300:
			return "fresh-t300", nil
		}
		return "", nil
	}
	nowFn = func() time.Time { return time.Unix(0, 20) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	startMetric := agentpkg.MetricSweepPrunedProxy.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
	if len(frames[0].Subagents) != 0 {
		t.Fatalf("cc.Subagents = %+v, want empty (pid-reused proxy detached)", frames[0].Subagents)
	}
	if delta := agentpkg.MetricSweepPrunedProxy.Value() - startMetric; delta != 1 {
		t.Fatalf("MetricSweepPrunedProxy delta = %d, want 1", delta)
	}
}

// IT13d — codex round 3 #R2 fix: when sweepOnce's owner-identity read
// (processStartTimeFn for the FRAME's own PID) returns an error, the
// frame must still flow into the survivors list so its pane reaches
// pruneDeadProxyRefs. Previously a bare `continue` skipped the entire
// pane's prune pass, leaving any stale proxy refs lit until a sweep
// tick happened to land while the owner's /proc read succeeded.
//
// Scenario: cc owner at PID 200 with a stale codex proxy ref pointing
// to PID 300 (confirmed dead source). processStartTimeFn(200) returns
// a transient error; processStartTimeFn(300) is irrelevant because the
// per-ref fail-safe (#O3) already gates on isPidAliveFn(300) first.
// Expected: pane still enters prune; stale codex ref detached.
func TestSweep_PruneRunsWhenOwnerStartTimeReadErrors(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             1,
		ProcessStartTime: "A",
		Subagents: []agentpkg.SubagentRef{{
			ID:              "proxy:codex:300:dead-t300",
			Type:            "codex",
			SourcePID:       300,
			SourceStartTime: "dead-t300",
			IsProxy:         true,
		}},
		Status:     agentpkg.StatusIdle,
		StartedAt:  10,
		LastSeenAt: 10,
		Verified:   true,
	}); err != nil {
		t.Fatalf("Upsert cc + dead proxy ref: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	// Owner (PID 200) alive; proxy source (PID 300) confirmed dead.
	isPidAliveFn = func(pid int) bool { return pid == 200 }
	// Owner identity read errors transiently; proxy source read is
	// short-circuited by the alive check in pruneDeadProxyRefs.
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 {
			return "", errStub("owner /proc transient")
		}
		return "", nil
	}
	nowFn = func() time.Time { return time.Unix(0, 20) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	startMetric := agentpkg.MetricSweepPrunedProxy.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (owner preserved on read error)", len(frames))
	}
	if len(frames[0].Subagents) != 0 {
		t.Fatalf("cc.Subagents = %+v, want empty — R2 regression: prune skipped because owner read errored", frames[0].Subagents)
	}
	if delta := agentpkg.MetricSweepPrunedProxy.Value() - startMetric; delta != 1 {
		t.Fatalf("MetricSweepPrunedProxy delta = %d, want 1 (prune ran despite owner read error)", delta)
	}
}

// IT14c — codex round 3 #R2 boundary: owner read error must not
// destructively delete the owner frame as a "pid_reused" cleanup. A
// transient /proc failure for the owner's PID returns ("", err), and
// the previous bare-continue path silently skipped the frame this round
// without any cleanup — equally important is that the new survivor
// path also doesn't trigger pid_reused (which compares startTime to
// frame.ProcessStartTime). Verifies the owner row is intact after the
// sweep.
func TestSweep_OwnerReadErrorPreservesFrame(t *testing.T) {
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
		t.Fatalf("Upsert cc: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origNow := nowFn
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) {
		return "", errStub("owner /proc transient")
	}
	nowFn = func() time.Time { return time.Unix(0, 20) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (owner read error must not delete)", len(frames))
	}
	if frames[0].ProcessStartTime != "A" {
		t.Fatalf("ProcessStartTime = %q, want A (frame untouched)", frames[0].ProcessStartTime)
	}
}
