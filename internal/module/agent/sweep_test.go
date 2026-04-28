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

// decodeSweepBroadcast unwraps a raw subscriber message into the
// inner agentpkg.NormalizedEvent. The broadcaster sends each event
// as core.HostEvent JSON; HostEvent.Value is itself a JSON-encoded
// NormalizedEvent string. Strict equality assertions on Status /
// RawEventName / AgentType need both layers parsed.
func decodeSweepBroadcast(t *testing.T, raw []byte) agentpkg.NormalizedEvent {
	t.Helper()
	var hostEvent core.HostEvent
	if err := json.Unmarshal(raw, &hostEvent); err != nil {
		t.Fatalf("unmarshal HostEvent: %v (raw=%s)", err, raw)
	}
	var normalized agentpkg.NormalizedEvent
	if err := json.Unmarshal([]byte(hostEvent.Value), &normalized); err != nil {
		t.Fatalf("unmarshal NormalizedEvent: %v (value=%s)", err, hostEvent.Value)
	}
	return normalized
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
	m.prober.Watch("work:", probe.WatchOptions{}, func(probe.ScreenChangeEvent) {})
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
	m.prober.Watch("work:", probe.WatchOptions{}, func(probe.ScreenChangeEvent) {})
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

// ---------------------------------------------------------------------------
// PR-3.5b §3.1 — sweep canonicalizePane (defense-in-depth backstop for
// hot-path canonicalization paths that left a partial state).
//
// IT10/IT10b-IT10p: integration tests driven through m.sweepOnce(). When
// they're committed (commit 5 of the PR), canonicalizePane does not yet
// exist; tests are therefore guarded with t.Skip("PR-3.5b commit 7: ...")
// so the test file compiles and the suite stays green. Skips are removed
// in commits 7 and 8 once canonicalizePane and broadcast are implemented.
//
// RC6-RC11: findCanonicalAncestor unit tests are added in commit 6 along
// with the helper itself, so they don't need t.Skip — they target a
// freshly-introduced symbol.
// ---------------------------------------------------------------------------

// installSweepCanonicalSeams sets up the standard seam overrides for
// IT10 tests: every PID in the alivePIDs/startTimes maps is alive and
// identity-verified, every other PID is dead. nowFn is pinned close to
// the seeded LastSeenAt so the idle_timeout rule does not fire on
// fixture frames. PPID for any PID not in ppids is 1 (init) — caller
// passes only the PIDs that need a non-trivial chain.
//
// Returns a cleanup that restores all seams; caller wires it through
// t.Cleanup so each test gets isolated state.
func installSweepCanonicalSeams(t *testing.T, alivePIDs map[int]bool, startTimes map[int]string, ppids map[int]int) {
	t.Helper()
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origInfo := readProcessInfoFn
	origNow := nowFn

	isPidAliveFn = func(pid int) bool {
		return alivePIDs[pid]
	}
	processStartTimeFn = func(pid int) (string, error) {
		st, ok := startTimes[pid]
		if !ok {
			return "", errStub("ps unknown pid")
		}
		return st, nil
	}
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		ppid := 1
		if v, ok := ppids[pid]; ok {
			ppid = v
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: ppid}, nil
	}
	nowFn = func() time.Time { return time.Unix(0, 100) }

	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		readProcessInfoFn = origInfo
		nowFn = origNow
	})
}

// IT10 — sweep canonicalizes a standalone live cross-type descendant
// into its ancestor's proxy ref. cc parent + codex standalone (PPID
// chain reaches cc) → after sweep: cc.Subagents has codex IsProxy ref;
// codex frame deleted; MetricSweepCanonicalized +1.
func TestSweep_CanonicalizesStandaloneDescendantIntoAncestor(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex standalone: %v", err)
	}

	installSweepCanonicalSeams(t,
		map[int]bool{100: true, 200: true},
		map[int]string{100: "t100", 200: "t200"},
		map[int]int{200: 100, 100: 1},
	)

	startMetric := agentpkg.MetricSweepCanonicalized.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "cc" {
		t.Fatalf("frames = %+v, want only cc remaining (codex folded into proxy ref)", frames)
	}
	if len(frames[0].Subagents) != 1 {
		t.Fatalf("cc.Subagents = %+v, want 1 codex IsProxy ref", frames[0].Subagents)
	}
	ref := frames[0].Subagents[0]
	if !ref.IsProxy || ref.SourcePID != 200 || ref.Type != "codex" {
		t.Fatalf("ref = %+v, want codex IsProxy SourcePID=200", ref)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startMetric; delta != 1 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 1", delta)
	}
}

// IT10b — candidate identity gate: PID-reused candidate → skip; cc
// untouched; pid_reused pass cleans the codex frame.
func TestSweep_CanonicalizeSkipsPidReuseCandidate(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200-stale", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex stale: %v", err)
	}

	// PID 200 alive but identity changed → pid_reused pass clears it.
	installSweepCanonicalSeams(t,
		map[int]bool{100: true, 200: true},
		map[int]string{100: "t100", 200: "t200-fresh"},
		map[int]int{200: 100},
	)

	startMetric := agentpkg.MetricSweepCanonicalized.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "cc" {
		t.Fatalf("frames = %+v, want only cc (codex pid_reused-cleared, never canonicalized)", frames)
	}
	if len(frames[0].Subagents) != 0 {
		t.Fatalf("cc.Subagents = %+v, want empty (pid-reused candidate must not be canonicalized)", frames[0].Subagents)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startMetric; delta != 0 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 0 (gated)", delta)
	}
}

// IT10c — candidate identity gate: dead PID candidate → skip; pid_dead
// pass clears it.
func TestSweep_CanonicalizeSkipsDeadCandidate(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex dead: %v", err)
	}

	// PID 200 dead → pid_dead pass clears it.
	installSweepCanonicalSeams(t,
		map[int]bool{100: true, 200: false},
		map[int]string{100: "t100", 200: "t200"},
		map[int]int{200: 100},
	)

	startMetric := agentpkg.MetricSweepCanonicalized.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "cc" {
		t.Fatalf("frames = %+v, want only cc remaining (codex pid_dead-cleared)", frames)
	}
	if len(frames[0].Subagents) != 0 {
		t.Fatalf("cc.Subagents = %+v, want empty (dead candidate must not be canonicalized)", frames[0].Subagents)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startMetric; delta != 0 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 0 (gated)", delta)
	}
}

// IT10d — ancestor identity gate: candidate live but ancestor (cc) PID
// is dead → findCanonicalAncestor identity gate skips → no fold; cc is
// cleared by pid_dead pass.
func TestSweep_CanonicalizeSkipsWhenAncestorDead(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex: %v", err)
	}

	// cc (PID 100) dead, codex (200) alive.
	installSweepCanonicalSeams(t,
		map[int]bool{100: false, 200: true},
		map[int]string{100: "t100", 200: "t200"},
		map[int]int{200: 100},
	)

	startMetric := agentpkg.MetricSweepCanonicalized.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	// cc cleared by pid_dead. Codex remains standalone (no ancestor to
	// fold into; ancestor identity gate kept candidate from being
	// attached to a dead frame about to be removed).
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "codex" {
		t.Fatalf("frames = %+v, want only codex (cc pid_dead-cleared, codex left for next tick)", frames)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startMetric; delta != 0 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 0 (ancestor dead gated)", delta)
	}
}

// IT10e — same-type ancestor short-circuit: two cc standalone frames
// where the second's PPID chain reaches the first → findCanonicalAncestor
// returns false (cc → cc is not a proxy relationship); both frames stay.
func TestSweep_CanonicalizeSkipsSameTypeAncestor(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc-1: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 110, PPID: 100,
		ProcessStartTime: "t110", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc-2: %v", err)
	}

	installSweepCanonicalSeams(t,
		map[int]bool{100: true, 110: true},
		map[int]string{100: "t100", 110: "t110"},
		map[int]int{110: 100},
	)

	startMetric := agentpkg.MetricSweepCanonicalized.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frames = %+v, want 2 (same-type ancestor not a proxy relationship)", frames)
	}
	for _, f := range frames {
		if len(f.Subagents) != 0 {
			t.Fatalf("frame %s Subagents = %+v, want empty (same-type fold suppressed)", f.FrameID, f.Subagents)
		}
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startMetric; delta != 0 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 0 (same-type)", delta)
	}
}

// IT10f — no ancestor frame in the pane: PPID chain walks to init
// without finding a frame; candidate left alone.
func TestSweep_CanonicalizeSkipsWhenNoAncestorInPane(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 999,
		ProcessStartTime: "t200", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex: %v", err)
	}

	// PPID chain: 200 → 999 → 1 (init). Neither 999 nor 1 has a frame
	// in the pane.
	installSweepCanonicalSeams(t,
		map[int]bool{200: true, 999: true},
		map[int]string{200: "t200", 999: "t999"},
		map[int]int{200: 999, 999: 1},
	)

	startMetric := agentpkg.MetricSweepCanonicalized.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "codex" {
		t.Fatalf("frames = %+v, want codex preserved (no ancestor)", frames)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startMetric; delta != 0 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 0", delta)
	}
}

// IT10g — partial: DeleteIfUnchanged fails (concurrent refresh bumps
// LastSeenAt baseline). Attach succeeds → cc.Subagents has codex IsProxy
// ref; codex standalone remains; metric NOT incremented (per plan: success
// = attach + delete both).
//
// Race trick: bump candidate LastSeenAt via Upsert during the
// processStartTimeFn callback for PID 200 (called by candidate identity
// gate) so when sweep later issues DeleteIfUnchanged with the original
// LastSeenAt it returns false.
func TestSweep_CanonicalizePartialWhenDeleteUnchangedFails(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc: %v", err)
	}
	codex, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	})
	if err != nil {
		t.Fatalf("Upsert codex: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origInfo := readProcessInfoFn
	origNow := nowFn
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		readProcessInfoFn = origInfo
		nowFn = origNow
	})

	isPidAliveFn = func(pid int) bool { return pid == 100 || pid == 200 }
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		ppid := 1
		if pid == 200 {
			ppid = 100
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: ppid}, nil
	}
	// processStartTimeFn(200) is called at least twice: once by
	// sweepOnce's main loop (frame identity gate), once by
	// canonicalizePane (candidate identity gate). Race on the SECOND
	// call: bump codex.LastSeenAt so canonicalizePane's later
	// DeleteIfUnchanged sees a stale baseline and returns false.
	pid200Calls := 0
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			pid200Calls++
			if pid200Calls == 2 {
				// Concurrent refresh — bump codex.LastSeenAt before
				// canonicalizePane's DeleteIfUnchanged runs.
				_, _ = m.frames.Upsert(store.Frame{
					FrameID:          codex.FrameID,
					PaneID:           "%5",
					AgentType:        "codex",
					PID:              200,
					PPID:             100,
					ProcessStartTime: "t200",
					Status:           agentpkg.StatusRunning,
					StartedAt:        50,
					LastSeenAt:       80, // bumped
					Verified:         true,
				})
			}
			return "t200", nil
		}
		return "", errStub("ps unknown pid")
	}
	nowFn = func() time.Time { return time.Unix(0, 100) }

	startMetric := agentpkg.MetricSweepCanonicalized.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frames = %+v, want 2 (partial: attach success, delete failure)", frames)
	}
	var ccRow *store.Frame
	for i := range frames {
		if frames[i].AgentType == "cc" {
			ccRow = &frames[i]
		}
	}
	if ccRow == nil || len(ccRow.Subagents) != 1 || !ccRow.Subagents[0].IsProxy {
		t.Fatalf("cc row = %+v, want exactly 1 IsProxy ref attached", ccRow)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startMetric; delta != 0 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 0 (partial → not counted)", delta)
	}
}

// IT10h — partial: attach fails because ancestor vanishes mid-attach
// (concurrent SessionEnd / sweep). codex frame remains; cc deleted; no
// fold metric increment.
//
// Race trick: in processStartTimeFn for ancestor (PID 100) — called by
// findCanonicalAncestor's identity gate — delete cc row before
// attachProxyRefWithRetry's first UpsertIfUnchanged. The reload via
// GetByIdentity returns nil → attached=false.
func TestSweep_CanonicalizePartialWhenAttachFails(t *testing.T) {
	m := newSweepTestModule(t)
	cc, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	})
	if err != nil {
		t.Fatalf("Upsert cc: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origInfo := readProcessInfoFn
	origNow := nowFn
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		readProcessInfoFn = origInfo
		nowFn = origNow
	})

	isPidAliveFn = func(pid int) bool { return pid == 100 || pid == 200 }
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		ppid := 1
		if pid == 200 {
			ppid = 100
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: ppid}, nil
	}
	// On the second processStartTimeFn(100) call (findCanonicalAncestor
	// identity gate, after candidate's own gate already ran on PID 200),
	// delete cc row so attachProxyRefWithRetry's UpsertIfUnchanged
	// detects a missing row and returns attached=false.
	pid100Calls := 0
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			pid100Calls++
			if pid100Calls == 1 {
				// findCanonicalAncestor's identity gate. Race: delete
				// cc row before attachProxyRefWithRetry can run.
				_ = m.frames.Delete(cc.FrameID)
			}
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "", errStub("ps unknown pid")
	}
	nowFn = func() time.Time { return time.Unix(0, 100) }

	startMetric := agentpkg.MetricSweepCanonicalized.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "codex" {
		t.Fatalf("frames = %+v, want only codex (cc deleted mid-attach; partial → no rollback)", frames)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startMetric; delta != 0 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 0 (attach failed)", delta)
	}
}

// IT10q (review F1 — round 2 attacker) — ancestor dies between
// findCanonicalAncestor's identity gate and DeleteIfUnchanged. Without
// the post-attach revalidation, sweep would attach codex IsProxy on
// cc (succeeds), then delete the live codex standalone row, leaving
// the live codex process with NO DB representation until its next
// hook event (the dead cc row is cleared on the next sweep tick along
// with its Subagents, severing the only link).
//
// Race trick: count isPidAliveFn(100) calls. Without F1 fix, exactly
// three calls happen — sweepOnce survivor (1), candidate=cc gate (2),
// findCanonicalAncestor when iterating codex (3). With F1 fix, the
// post-attach revalidation adds a 4th call. Returning false on the
// 4th call simulates cc dying after attach succeeded but before the
// candidate row was deleted; sweep must skip delete and leave the
// codex row standing.
func TestSweep_CanonicalizeSkipsDeleteWhenAncestorDiesPostAttach(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc: %v", err)
	}
	codex, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200", Status: agentpkg.StatusRunning,
		StartedAt: 51, LastSeenAt: 51, Verified: true,
	})
	if err != nil {
		t.Fatalf("Upsert codex: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origInfo := readProcessInfoFn
	origNow := nowFn
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		readProcessInfoFn = origInfo
		nowFn = origNow
	})

	pid100AliveCalls := 0
	isPidAliveFn = func(pid int) bool {
		switch pid {
		case 100:
			pid100AliveCalls++
			// First three calls (survivor pass + cc-as-candidate gate +
			// findCanonicalAncestor for codex) say alive. The 4th call
			// — only reachable with the F1 post-attach revalidation —
			// reports cc as dead.
			return pid100AliveCalls < 4
		case 200:
			return true
		}
		return false
	}
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "", errStub("ps unknown pid")
	}
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		ppid := 1
		if pid == 200 {
			ppid = 100
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: ppid}, nil
	}
	nowFn = func() time.Time { return time.Unix(0, 100) }

	startSweep := agentpkg.MetricSweepCanonicalized.Value()
	startPartial := agentpkg.MetricPartialCanonicalizationCreated.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	var ccRow, codexRow *store.Frame
	for i := range frames {
		switch frames[i].AgentType {
		case "cc":
			ccRow = &frames[i]
		case "codex":
			codexRow = &frames[i]
		}
	}
	if codexRow == nil || codexRow.FrameID != codex.FrameID {
		t.Fatalf("codex row missing or replaced; want preserved live row, got frames = %+v", frames)
	}
	if ccRow == nil || len(ccRow.Subagents) != 1 || !ccRow.Subagents[0].IsProxy || ccRow.Subagents[0].SourcePID != 200 {
		t.Fatalf("cc row = %+v, want exactly 1 IsProxy ref attached pre-revalidation", ccRow)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startSweep; delta != 0 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 0 (delete skipped because ancestor died)", delta)
	}
	if delta := agentpkg.MetricPartialCanonicalizationCreated.Value() - startPartial; delta != 1 {
		t.Fatalf("MetricPartialCanonicalizationCreated delta = %d, want 1 (deliberate partial — ancestor died post-attach)", delta)
	}
}

// IT10s (review F4 — round 3 closure) — already-proxied delete-only
// path: when the parent already has a matching IsProxy ref AND the
// candidate carries no owned state, the F2 reorganization routes
// straight to DeleteIfUnchanged without going through the attach
// branch where the F1 post-attach revalidation lives. Same data-loss
// hazard as F1 reopens through the new path: ancestor can die between
// findCanonicalAncestor and DeleteIfUnchanged, leaving the live
// candidate row deleted into a dead parent that the next sweep tick
// will clear (taking the proxy ref with it).
//
// Race trick mirrors IT10q: count isPidAliveFn(ancestor) calls and
// flip false on the 4th call — the call introduced only by the F4
// hoisted revalidation. Without F4 the 4th call never runs and the
// codex row is incorrectly deleted.
func TestSweep_CanonicalizeSkipsDeleteWhenAncestorDiesPostMatchCheck(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100",
		Subagents: []agentpkg.SubagentRef{{
			ID:        "proxy:codex:200:t200",
			Type:      "codex",
			StartedAt: 30,
			SourcePID: 200, SourceStartTime: "t200",
			IsProxy: true,
		}},
		Status: agentpkg.StatusIdle, StartedAt: 30, LastSeenAt: 80, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc with pre-existing proxy: %v", err)
	}
	codex, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200", Status: agentpkg.StatusRunning,
		StartedAt: 51, LastSeenAt: 51, Verified: true,
	})
	if err != nil {
		t.Fatalf("Upsert codex orphan: %v", err)
	}

	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	origInfo := readProcessInfoFn
	origNow := nowFn
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		readProcessInfoFn = origInfo
		nowFn = origNow
	})

	pid100AliveCalls := 0
	isPidAliveFn = func(pid int) bool {
		switch pid {
		case 100:
			pid100AliveCalls++
			// Calls 1-3: survivor pass + cc-as-candidate gate +
			// findCanonicalAncestor. Call 4 (only reachable via
			// F4 hoisted revalidation) reports cc dead.
			return pid100AliveCalls < 4
		case 200:
			return true
		}
		return false
	}
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "", errStub("ps unknown pid")
	}
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		ppid := 1
		if pid == 200 {
			ppid = 100
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: ppid}, nil
	}
	nowFn = func() time.Time { return time.Unix(0, 100) }

	startSweep := agentpkg.MetricSweepCanonicalized.Value()
	startPartial := agentpkg.MetricPartialCanonicalizationCreated.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	var ccRow, codexRow *store.Frame
	for i := range frames {
		switch frames[i].AgentType {
		case "cc":
			ccRow = &frames[i]
		case "codex":
			codexRow = &frames[i]
		}
	}
	if codexRow == nil || codexRow.FrameID != codex.FrameID {
		t.Fatalf("codex row missing or replaced; want preserved live row, got frames = %+v", frames)
	}
	if ccRow == nil || len(ccRow.Subagents) != 1 || !ccRow.Subagents[0].IsProxy || ccRow.Subagents[0].SourcePID != 200 {
		t.Fatalf("cc row = %+v, want pre-existing IsProxy ref intact", ccRow)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startSweep; delta != 0 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 0 (delete skipped because ancestor died after match-check)", delta)
	}
	if delta := agentpkg.MetricPartialCanonicalizationCreated.Value() - startPartial; delta != 1 {
		t.Fatalf("MetricPartialCanonicalizationCreated delta = %d, want 1 (deliberate partial — F4 hoisted revalidation)", delta)
	}
}

// IT10i — canonicalize → prune ordering in the same tick. cc has a
// stale codex IsProxy ref AND a live opencode descendant standalone.
// Same tick: canonicalize attaches opencode IsProxy → prune detaches
// stale codex IsProxy. Final cc.Subagents = [opencode IsProxy].
func TestSweep_CanonicalizeThenPruneSameTick(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100",
		Subagents: []agentpkg.SubagentRef{{
			ID: "proxy:codex:300:dead", Type: "codex",
			SourcePID: 300, SourceStartTime: "dead-t300", IsProxy: true,
		}},
		Status: agentpkg.StatusIdle, StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc + stale ref: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "opencode", PID: 400, PPID: 100,
		ProcessStartTime: "t400", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert opencode standalone: %v", err)
	}

	// PIDs alive: 100 (cc), 400 (opencode); PID 300 (stale source) DEAD.
	installSweepCanonicalSeams(t,
		map[int]bool{100: true, 400: true, 300: false},
		map[int]string{100: "t100", 400: "t400", 300: "ignored"},
		map[int]int{400: 100},
	)

	startCan := agentpkg.MetricSweepCanonicalized.Value()
	startPrune := agentpkg.MetricSweepPrunedProxy.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "cc" {
		t.Fatalf("frames = %+v, want only cc (opencode folded)", frames)
	}
	if len(frames[0].Subagents) != 1 {
		t.Fatalf("cc.Subagents = %+v, want exactly 1 (stale codex pruned, opencode attached)", frames[0].Subagents)
	}
	ref := frames[0].Subagents[0]
	if ref.SourcePID != 400 || ref.Type != "opencode" {
		t.Fatalf("ref = %+v, want opencode IsProxy SourcePID=400 (stale codex 300 should be pruned)", ref)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startCan; delta != 1 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 1", delta)
	}
	if delta := agentpkg.MetricSweepPrunedProxy.Value() - startPrune; delta != 1 {
		t.Fatalf("MetricSweepPrunedProxy delta = %d, want 1", delta)
	}
}

// IT10j — successful canonicalize emits broadcast with reason=
// sweep:proxy_canonicalized. Verifies broadcast wiring (commit 8).
func TestSweep_CanonicalizeEmitsBroadcastWhenAnySucceeded(t *testing.T) {
	m := newSweepTestModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: m.tmux}
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex: %v", err)
	}

	installSweepCanonicalSeams(t,
		map[int]bool{100: true, 200: true},
		map[int]string{100: "t100", 200: "t200"},
		map[int]int{200: 100},
	)

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	// Look through the broadcast channel for the canonicalized event.
	deadline := time.After(200 * time.Millisecond)
	gotCanonicalized := false
	for !gotCanonicalized {
		select {
		case msg := <-sub.SendCh():
			if strings.Contains(string(msg), "sweep:proxy_canonicalized") {
				gotCanonicalized = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for sweep:proxy_canonicalized broadcast")
		}
	}
}

// IT10k — no successful canonicalize → no broadcast emitted.
func TestSweep_CanonicalizeNoBroadcastWhenNothingSucceeded(t *testing.T) {
	m := newSweepTestModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: m.tmux}
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	// Single isolated codex frame; PPID chain doesn't reach any pane
	// frame → findCanonicalAncestor false → no fold → no broadcast.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 999,
		ProcessStartTime: "t200", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex: %v", err)
	}

	installSweepCanonicalSeams(t,
		map[int]bool{200: true, 999: true},
		map[int]string{200: "t200", 999: "t999"},
		map[int]int{200: 999, 999: 1},
	)

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	// Drain channel briefly; assert no proxy_canonicalized broadcast.
	timeout := time.After(80 * time.Millisecond)
	for {
		select {
		case msg := <-sub.SendCh():
			if strings.Contains(string(msg), "sweep:proxy_canonicalized") {
				t.Fatalf("unexpected broadcast: %s", msg)
			}
		case <-timeout:
			return
		}
	}
}

// IT10l — cross-pane isolation: pane A foldable; pane B not foldable.
// Each pane is canonicalized independently; pane B does not see pane
// A's ancestor and vice-versa.
func TestSweep_CanonicalizeCrossPaneIsolation(t *testing.T) {
	m := newSweepTestModule(t)
	fakeTmux, _ := m.tmux.(*tmux.FakeExecutor)
	fakeTmux.SetPaneSessionName("%6", "work2")
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "work-code", Name: "work"},
		{Code: "work2-code", Name: "work2"},
	}}

	// Pane A: cc + codex foldable.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc paneA: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex paneA: %v", err)
	}
	// Pane B: lone codex; no ancestor frame in pane.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%6", AgentType: "codex", PID: 300, PPID: 999,
		ProcessStartTime: "t300", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex paneB: %v", err)
	}

	installSweepCanonicalSeams(t,
		map[int]bool{100: true, 200: true, 300: true, 999: true},
		map[int]string{100: "t100", 200: "t200", 300: "t300", 999: "t999"},
		map[int]int{200: 100, 300: 999, 999: 1},
	)

	startMetric := agentpkg.MetricSweepCanonicalized.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	paneA, _ := m.frames.ListByPane("%5")
	if len(paneA) != 1 || paneA[0].AgentType != "cc" || len(paneA[0].Subagents) != 1 {
		t.Fatalf("paneA = %+v, want cc with 1 codex IsProxy ref", paneA)
	}
	paneB, _ := m.frames.ListByPane("%6")
	if len(paneB) != 1 || paneB[0].AgentType != "codex" {
		t.Fatalf("paneB = %+v, want codex untouched (no ancestor)", paneB)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startMetric; delta != 1 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 1 (pane A only)", delta)
	}
}

// IT10m — already-proxied historical partial: hot-path attached the
// proxy ref then DeleteIfUnchanged failed long ago. Sweep re-attaches
// idempotently (mutateSubagentsWithRetry replace by match), then deletes
// the standalone row. Final state: cc.Subagents has exactly 1 codex
// IsProxy ref (no duplicate), codex standalone deleted.
func TestSweep_CanonicalizeSkipsAlreadyProxiedCandidate(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100",
		Subagents: []agentpkg.SubagentRef{{
			ID:        "proxy:codex:200:t200",
			Type:      "codex",
			StartedAt: 50,
			SourcePID: 200, SourceStartTime: "t200",
			IsProxy: true,
		}},
		Status: agentpkg.StatusIdle, StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc with pre-existing proxy: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200", Status: agentpkg.StatusRunning,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex orphan: %v", err)
	}

	installSweepCanonicalSeams(t,
		map[int]bool{100: true, 200: true},
		map[int]string{100: "t100", 200: "t200"},
		map[int]int{200: 100},
	)

	startMetric := agentpkg.MetricSweepCanonicalized.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "cc" {
		t.Fatalf("frames = %+v, want only cc remaining (codex re-canonicalized)", frames)
	}
	if len(frames[0].Subagents) != 1 {
		t.Fatalf("cc.Subagents = %+v, want exactly 1 (idempotent re-attach, no duplicate)", frames[0].Subagents)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startMetric; delta != 1 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 1", delta)
	}
}

// IT10n — round 1 high regression guard (v3): sweep must NOT erase a
// child's native subagent state during partial recovery.
//
// Scenario (recreating the original round 1 high data-loss path):
//   (a) cc frame with codex IsProxy already attached (hot-path success)
//   (b) codex standalone row still present (hot-path DeleteIfUnchanged
//       failed in the past)
//   (c) codex.Subagents has a native ref (subsequent SubagentStart on
//       child landed before sweep)
//   (d) codex.LastSeenAt was bumped by that SubagentStart so the
//       hot-path's saved baseline would not match
//
// Without candidateHasOwnedState(), sweep would attach a duplicate codex
// IsProxy ref to cc (or no-op via idempotent retry) and then delete
// the codex row — losing the native ref. With the guard, candidate skips,
// codex row + native ref preserved, no re-attach, metric unchanged.
//
// Projection_dedup (PR-3.5a) continues to merge codex's native into
// the cc projection at read time, hiding the standalone child without
// data loss.
func TestSweep_CanonicalizePreservesChildNativeAfterPartialRecovery(t *testing.T) {
	m := newSweepTestModule(t)
	// (a) cc frame with codex IsProxy already attached.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100",
		Subagents: []agentpkg.SubagentRef{{
			ID:        "proxy:codex:200:t200",
			Type:      "codex",
			StartedAt: 30,
			SourcePID: 200, SourceStartTime: "t200",
			IsProxy: true,
		}},
		Status: agentpkg.StatusIdle, StartedAt: 30, LastSeenAt: 80, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc + existing codex IsProxy: %v", err)
	}
	// (b)+(c)+(d) codex standalone row still present, with native ref,
	// LastSeenAt bumped past hot-path baseline.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200",
		Subagents: []agentpkg.SubagentRef{{
			ID:        "task-1",
			Type:      "codex",
			StartedAt: 70,
			IsProxy:   false, // NATIVE — must be preserved
		}},
		Status: agentpkg.StatusRunning, StartedAt: 50, LastSeenAt: 80, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex + native ref + bumped LastSeenAt: %v", err)
	}

	installSweepCanonicalSeams(t,
		map[int]bool{100: true, 200: true},
		map[int]string{100: "t100", 200: "t200"},
		map[int]int{200: 100},
	)

	startMetric := agentpkg.MetricSweepCanonicalized.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frames = %+v, want 2 (codex row preserved, candidate has owned native state)", frames)
	}
	var cc, codex *store.Frame
	for i := range frames {
		switch frames[i].AgentType {
		case "cc":
			cc = &frames[i]
		case "codex":
			codex = &frames[i]
		}
	}
	if cc == nil || codex == nil {
		t.Fatalf("frames = %+v, missing cc or codex", frames)
	}
	if len(cc.Subagents) != 1 {
		t.Fatalf("cc.Subagents = %+v, want exactly 1 codex IsProxy (no duplicate from re-attach)", cc.Subagents)
	}
	if !cc.Subagents[0].IsProxy || cc.Subagents[0].SourcePID != 200 {
		t.Fatalf("cc.Subagents[0] = %+v, want codex IsProxy SourcePID=200", cc.Subagents[0])
	}
	if len(codex.Subagents) != 1 || codex.Subagents[0].IsProxy || codex.Subagents[0].ID != "task-1" {
		t.Fatalf("codex.Subagents = %+v, want native task-1 preserved (round 1 high guard)", codex.Subagents)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startMetric; delta != 0 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 0 (gated case must not count as progress)", delta)
	}
}

// IT10r (review F2 — round 2 defender) — owned-state candidate with NO
// existing parent proxy ref. The hot path failed to attach (e.g.
// readProcessInfoFn transient error during PPID walk) and the child
// then accumulated owned native state. Without this recovery path,
// candidateHasOwnedState would early-skip and the partial would
// persist forever: parent has no proxy ref to claim the child, so
// projection_dedup cannot hide it; the child would render as a
// separate top-frame indefinitely.
//
// Recovery: idempotently attach proxy ref on ancestor (enables
// projection_dedup at next read) but skip delete (preserves owned
// native state). MetricPartialCanonicalizationCreated +1 marks the
// deliberate partial; MetricSweepCanonicalized stays unchanged
// (semantics: only fully canonicalized rows count).
func TestSweep_CanonicalizeAttachesAncestorProxyForOwnedStateCandidate(t *testing.T) {
	m := newSweepTestModule(t)
	// cc frame with EMPTY Subagents — hot path failed to attach.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100",
		Status:           agentpkg.StatusIdle, StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc: %v", err)
	}
	// codex standalone, owned native subagent state.
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200",
		Subagents: []agentpkg.SubagentRef{{
			ID:        "task-defender-1",
			Type:      "codex",
			StartedAt: 70,
			IsProxy:   false, // NATIVE — must be preserved
		}},
		Status: agentpkg.StatusRunning, StartedAt: 51, LastSeenAt: 51, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex + native ref: %v", err)
	}

	installSweepCanonicalSeams(t,
		map[int]bool{100: true, 200: true},
		map[int]string{100: "t100", 200: "t200"},
		map[int]int{200: 100},
	)

	startSweep := agentpkg.MetricSweepCanonicalized.Value()
	startPartial := agentpkg.MetricPartialCanonicalizationCreated.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frames = %+v, want 2 (codex row preserved with owned state)", frames)
	}
	var cc, codex *store.Frame
	for i := range frames {
		switch frames[i].AgentType {
		case "cc":
			cc = &frames[i]
		case "codex":
			codex = &frames[i]
		}
	}
	if cc == nil || codex == nil {
		t.Fatalf("frames = %+v, missing cc or codex", frames)
	}
	if len(cc.Subagents) != 1 || !cc.Subagents[0].IsProxy || cc.Subagents[0].SourcePID != 200 {
		t.Fatalf("cc.Subagents = %+v, want exactly 1 codex IsProxy (newly attached by F2)", cc.Subagents)
	}
	if len(codex.Subagents) != 1 || codex.Subagents[0].IsProxy || codex.Subagents[0].ID != "task-defender-1" {
		t.Fatalf("codex.Subagents = %+v, want native task-defender-1 preserved (owned state)", codex.Subagents)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startSweep; delta != 0 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 0 (delete deliberately skipped)", delta)
	}
	if delta := agentpkg.MetricPartialCanonicalizationCreated.Value() - startPartial; delta != 1 {
		t.Fatalf("MetricPartialCanonicalizationCreated delta = %d, want 1 (deliberate partial: attach without delete)", delta)
	}
}

// IT10o — candidate carrying ONLY stale (dead) IsProxy refs is folded
// (stale ref dropped along with the row; equivalent to sweep prune
// detaching it then sweep folding next tick).
func TestSweep_CanonicalizeFoldsCandidateWithOnlyStaleProxy(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200",
		Subagents: []agentpkg.SubagentRef{{
			ID: "proxy:opencode:888:dead", Type: "opencode",
			SourcePID: 888, SourceStartTime: "dead", IsProxy: true,
		}},
		Status: agentpkg.StatusRunning, StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex with only-stale proxy: %v", err)
	}

	installSweepCanonicalSeams(t,
		map[int]bool{100: true, 200: true, 888: false},
		map[int]string{100: "t100", 200: "t200", 888: "ignored"},
		map[int]int{200: 100},
	)

	startMetric := agentpkg.MetricSweepCanonicalized.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "cc" {
		t.Fatalf("frames = %+v, want only cc (codex folded)", frames)
	}
	if len(frames[0].Subagents) != 1 || frames[0].Subagents[0].SourcePID != 200 {
		t.Fatalf("cc.Subagents = %+v, want codex IsProxy SourcePID=200", frames[0].Subagents)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startMetric; delta != 1 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 1", delta)
	}
}

// ---------------------------------------------------------------------------
// PR-3.5b §3.2 — findCanonicalAncestor unit tests (RC6-RC11).
//
// Pure helper — no DB I/O. Each test seeds a framesByPID map and
// exercises one return path: cross-type match, same-type hard-stop,
// dead/PID-reused identity gate fail-safe, depth exhaustion, self-loop
// detection, and readProcessInfoFn transient error.
// ---------------------------------------------------------------------------

func newCanonicalAncestorTestModule(t *testing.T) *Module {
	t.Helper()
	return newSweepTestModule(t)
}

// RC6 — happy path: descendant walks one PPID hop, finds cross-type
// ancestor frame in pane, ancestor passes identity gate → returns (frame, true).
func TestFindCanonicalAncestor_WalksToCrossTypeMatch(t *testing.T) {
	m := newCanonicalAncestorTestModule(t)
	cc := store.Frame{
		FrameID: "cc-1", PaneID: "%5", AgentType: "cc",
		PID: 100, PPID: 1, ProcessStartTime: "t100",
	}
	candidate := store.Frame{
		FrameID: "codex-1", PaneID: "%5", AgentType: "codex",
		PID: 200, PPID: 100, ProcessStartTime: "t200",
	}
	framesByPID := map[int]store.Frame{100: cc, 200: candidate}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "t100", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	ancestor, ok := m.findCanonicalAncestor(candidate, framesByPID)
	if !ok || ancestor.PID != 100 || ancestor.AgentType != "cc" {
		t.Fatalf("findCanonicalAncestor = (%+v, %v), want cc PID=100", ancestor, ok)
	}
}

// RC7 — same-type immediate match → returns false (not a proxy
// relationship; mirrors findProxyParent hard-stop).
func TestFindCanonicalAncestor_ReturnsFalseOnSameTypeImmediateMatch(t *testing.T) {
	m := newCanonicalAncestorTestModule(t)
	parent := store.Frame{
		FrameID: "cc-1", PaneID: "%5", AgentType: "cc",
		PID: 100, PPID: 1, ProcessStartTime: "t100",
	}
	candidate := store.Frame{
		FrameID: "cc-2", PaneID: "%5", AgentType: "cc",
		PID: 200, PPID: 100, ProcessStartTime: "t200",
	}
	framesByPID := map[int]store.Frame{100: parent, 200: candidate}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "t100", nil }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	if _, ok := m.findCanonicalAncestor(candidate, framesByPID); ok {
		t.Fatal("findCanonicalAncestor = true, want false (same-type ancestor must hard-stop)")
	}
}

// RC8 — ancestor PID dead → identity gate fail-safe; helper continues
// walking (next ppid hop). When chain exhausts without finding a live
// match, returns false.
func TestFindCanonicalAncestor_ReturnsFalseOnDeadAncestorIdentityGate(t *testing.T) {
	m := newCanonicalAncestorTestModule(t)
	cc := store.Frame{
		FrameID: "cc-1", PaneID: "%5", AgentType: "cc",
		PID: 100, PPID: 1, ProcessStartTime: "t100",
	}
	candidate := store.Frame{
		FrameID: "codex-1", PaneID: "%5", AgentType: "codex",
		PID: 200, PPID: 100, ProcessStartTime: "t200",
	}
	framesByPID := map[int]store.Frame{100: cc, 200: candidate}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 200:
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		case 100:
			return agentpkg.ProcessInfo{PID: 100, PPID: 1}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	// PID 100 (cc) is DEAD.
	isPidAliveFn = func(pid int) bool { return pid != 100 }
	processStartTimeFn = func(int) (string, error) { return "t100", nil }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	if _, ok := m.findCanonicalAncestor(candidate, framesByPID); ok {
		t.Fatal("findCanonicalAncestor = true, want false (dead ancestor identity gate must skip)")
	}
}

// RC9 — depth exhaustion: long PPID chain with no frame match within
// proxyMaxDepth → false.
func TestFindCanonicalAncestor_ReturnsFalseOnDepthExhaustion(t *testing.T) {
	m := newCanonicalAncestorTestModule(t)
	candidate := store.Frame{
		FrameID: "codex-1", PaneID: "%5", AgentType: "codex",
		PID: 200, PPID: 1000, ProcessStartTime: "t200",
	}
	// PPID chain: 200 → 1000 → 1001 → 1002 → 1003 → 1004 → 1005 (still
	// no frame in pane; depth cap stops after 5 hops).
	framesByPID := map[int]store.Frame{200: candidate}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 200:
			return agentpkg.ProcessInfo{PID: 200, PPID: 1000}, nil
		case 1000:
			return agentpkg.ProcessInfo{PID: 1000, PPID: 1001}, nil
		case 1001:
			return agentpkg.ProcessInfo{PID: 1001, PPID: 1002}, nil
		case 1002:
			return agentpkg.ProcessInfo{PID: 1002, PPID: 1003}, nil
		case 1003:
			return agentpkg.ProcessInfo{PID: 1003, PPID: 1004}, nil
		case 1004:
			return agentpkg.ProcessInfo{PID: 1004, PPID: 1005}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: pid + 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "irrelevant", nil }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	if _, ok := m.findCanonicalAncestor(candidate, framesByPID); ok {
		t.Fatal("findCanonicalAncestor = true, want false (depth exhausted)")
	}
}

// RC10 — self-loop: ancestor's PPID equals its own PID → false.
func TestFindCanonicalAncestor_ReturnsFalseOnLoopDetectionPpidEqPid(t *testing.T) {
	m := newCanonicalAncestorTestModule(t)
	candidate := store.Frame{
		FrameID: "codex-1", PaneID: "%5", AgentType: "codex",
		PID: 200, PPID: 1000, ProcessStartTime: "t200",
	}
	framesByPID := map[int]store.Frame{200: candidate}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 200:
			return agentpkg.ProcessInfo{PID: 200, PPID: 1000}, nil
		case 1000:
			// Self-loop: PPID == PID.
			return agentpkg.ProcessInfo{PID: 1000, PPID: 1000}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "irrelevant", nil }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	if _, ok := m.findCanonicalAncestor(candidate, framesByPID); ok {
		t.Fatal("findCanonicalAncestor = true, want false (self-loop must abort walk)")
	}
}

// RC11 — readProcessInfoFn returns transient error → false (caller
// retries next sweep tick).
func TestFindCanonicalAncestor_ReturnsFalseOnReadProcessInfoTransientError(t *testing.T) {
	m := newCanonicalAncestorTestModule(t)
	candidate := store.Frame{
		FrameID: "codex-1", PaneID: "%5", AgentType: "codex",
		PID: 200, PPID: 100, ProcessStartTime: "t200",
	}
	framesByPID := map[int]store.Frame{200: candidate}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{}, errStub("proc transient")
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "irrelevant", nil }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	if _, ok := m.findCanonicalAncestor(candidate, framesByPID); ok {
		t.Fatal("findCanonicalAncestor = true, want false (transient read error must abort)")
	}
}

// IT10p — candidate carrying a LIVE identity-verified IsProxy ref is
// preserved as a frame (the candidate itself is acting as ancestor for
// some other sub-tree; folding its row would lose that role) BUT the
// canonical proxy ref is still attached on the higher ancestor so the
// pane projection consolidates the whole tree under one top frame.
//
// Round 2 defender (review F2) reorganization: owned-state guard no
// longer early-skips before findCanonicalAncestor. Instead, when a
// canonical ancestor exists AND the candidate carries owned state,
// sweep idempotently attaches the proxy ref on the ancestor (so
// projection_dedup can hide + merge the candidate at read time) and
// skips delete (so the candidate's owned state is preserved). Net
// effect for IT10p: cc gains a codex IsProxy ref; codex row stays put
// with its opencode IsProxy ref intact; projection sees a single cc
// top frame with [codex IsProxy + opencode IsProxy] merged refs.
func TestSweep_CanonicalizeSkipsCandidateWithLiveProxy(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1,
		ProcessStartTime: "t100", Status: agentpkg.StatusIdle,
		StartedAt: 50, LastSeenAt: 50, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert cc: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100,
		ProcessStartTime: "t200",
		Subagents: []agentpkg.SubagentRef{{
			ID: "proxy:opencode:300:t300", Type: "opencode",
			SourcePID: 300, SourceStartTime: "t300", IsProxy: true,
		}},
		Status: agentpkg.StatusRunning, StartedAt: 51, LastSeenAt: 51, Verified: true,
	}); err != nil {
		t.Fatalf("Upsert codex with live proxy: %v", err)
	}

	installSweepCanonicalSeams(t,
		map[int]bool{100: true, 200: true, 300: true},
		map[int]string{100: "t100", 200: "t200", 300: "t300"},
		map[int]int{200: 100},
	)

	startSweep := agentpkg.MetricSweepCanonicalized.Value()
	startPartial := agentpkg.MetricPartialCanonicalizationCreated.Value()
	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frames = %+v, want 2 (codex preserved as sub-ancestor)", frames)
	}
	var cc, codex *store.Frame
	for i := range frames {
		switch frames[i].AgentType {
		case "cc":
			cc = &frames[i]
		case "codex":
			codex = &frames[i]
		}
	}
	if cc == nil || codex == nil {
		t.Fatalf("frames = %+v, missing cc or codex", frames)
	}
	if len(cc.Subagents) != 1 || !cc.Subagents[0].IsProxy || cc.Subagents[0].SourcePID != 200 {
		t.Fatalf("cc.Subagents = %+v, want exactly 1 codex IsProxy attached by F2 attach-only recovery", cc.Subagents)
	}
	if len(codex.Subagents) != 1 || !codex.Subagents[0].IsProxy || codex.Subagents[0].SourcePID != 300 {
		t.Fatalf("codex.Subagents = %+v, want preserved opencode IsProxy (sub-tree role intact)", codex.Subagents)
	}
	if delta := agentpkg.MetricSweepCanonicalized.Value() - startSweep; delta != 0 {
		t.Fatalf("MetricSweepCanonicalized delta = %d, want 0 (delete deliberately skipped)", delta)
	}
	if delta := agentpkg.MetricPartialCanonicalizationCreated.Value() - startPartial; delta != 1 {
		t.Fatalf("MetricPartialCanonicalizationCreated delta = %d, want 1 (deliberate partial: attach without delete)", delta)
	}
}

// #717 — sweep:pid_dead broadcast must carry status=clear when the
// dead frame was the only frame in its tmux session. SPA's
// useAgentStore strict-equality tests (`event.status === 'clear'`)
// drop empty status as a no-op, leaving the agent indicator stuck
// at its last value (the user-visible #717 symptom).
func TestSweep_PidDead_BroadcastsClearWhenSessionEmpties(t *testing.T) {
	m := newSweepTestModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: m.tmux}
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "opencode",
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
	origStart := processStartTimeFn
	origNow := nowFn
	isPidAliveFn = func(int) bool { return false }
	processStartTimeFn = func(int) (string, error) { return "dead", nil }
	nowFn = func() time.Time { return time.Unix(0, 20) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	select {
	case msg := <-sub.SendCh():
		got := decodeSweepBroadcast(t, msg)
		if got.RawEventName != "sweep:pid_dead" {
			t.Fatalf("RawEventName = %q, want %q", got.RawEventName, "sweep:pid_dead")
		}
		if got.Status != string(agentpkg.StatusClear) {
			t.Fatalf("Status = %q, want %q (#717: empty status leaks through projection==nil branch)", got.Status, agentpkg.StatusClear)
		}
		if got.AgentType != "opencode" {
			t.Fatalf("AgentType = %q, want %q (fallback should preserve dead frame's type)", got.AgentType, "opencode")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for sweep:pid_dead broadcast")
	}
}

// #717 — when a sibling frame survives the sweep in the same tmux
// session, the broadcast must surface the surviving TopFrame's status
// (branch [C] of buildProjectionNormalized), NOT clear. This guards
// against an over-eager fix that would always force clear regardless
// of remaining projection state.
func TestSweep_PidDead_PreservesSiblingStatus(t *testing.T) {
	m := newSweepTestModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: m.tmux}
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	// cc parent (alive, idle, StartedAt=10) and codex child (dead,
	// running, StartedAt=20). Explicit non-equal StartedAt eliminates
	// the projectionSortGreater FrameID-uuid tiebreak (codex plan
	// review P2 0.94: would otherwise be flaky on tie).
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
		t.Fatalf("Upsert cc parent: %v", err)
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
		t.Fatalf("Upsert codex child: %v", err)
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
	nowFn = func() time.Time { return time.Unix(0, 30) }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		nowFn = origNow
	})

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}

	select {
	case msg := <-sub.SendCh():
		got := decodeSweepBroadcast(t, msg)
		if got.RawEventName != "sweep:pid_dead" {
			t.Fatalf("RawEventName = %q, want %q", got.RawEventName, "sweep:pid_dead")
		}
		if got.Status != string(agentpkg.StatusIdle) {
			t.Fatalf("Status = %q, want %q (TopFrame=cc StatusIdle should dominate via branch [C])", got.Status, agentpkg.StatusIdle)
		}
		if got.AgentType != "cc" {
			t.Fatalf("AgentType = %q, want %q (TopFrame override expected)", got.AgentType, "cc")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for sweep:pid_dead broadcast")
	}
}
