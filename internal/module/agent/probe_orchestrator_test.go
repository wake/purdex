package agent

import (
	"bytes"
	"expvar"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// metricSnapshot reads cumulative counter values for delta-style assertions.
// expvar counters are global to the process so absolute values cannot be
// asserted; tests snapshot before+after and compare deltas instead.
type metricSnapshot struct {
	watchStarted   int64
	watchStopped   int64
	screenEvent    int64
	graceWindowSup int64
}

func snapshotMetrics() metricSnapshot {
	return metricSnapshot{
		watchStarted:   metricInt("purdex_probe_watch_started_total"),
		watchStopped:   metricInt("purdex_probe_watch_stopped_total"),
		screenEvent:    metricInt("purdex_probe_screen_event_total"),
		graceWindowSup: metricInt("purdex_probe_grace_window_suppressed_total"),
	}
}

func metricInt(name string) int64 {
	v := expvar.Get(name)
	if v == nil {
		return 0
	}
	if iv, ok := v.(*expvar.Int); ok {
		return iv.Value()
	}
	return 0
}

// fakeTmuxCapture supports test injection of CapturePaneContent results.
// Embeds a real FakeExecutor and overlays a programmable response for
// (target, lastN) pairs so OR8 can drive shell-prompt classification.
type fakeTmuxCapture struct {
	*tmux.FakeExecutor
	mu          sync.Mutex
	captureFn   func(target string, lastN int) (string, error)
	captureCall []captureArgs
}

type captureArgs struct {
	target string
	lastN  int
}

func newFakeTmuxCapture() *fakeTmuxCapture {
	return &fakeTmuxCapture{FakeExecutor: tmux.NewFakeExecutor()}
}

func (f *fakeTmuxCapture) CapturePaneContent(target string, lastN int) (string, error) {
	f.mu.Lock()
	fn := f.captureFn
	f.captureCall = append(f.captureCall, captureArgs{target: target, lastN: lastN})
	f.mu.Unlock()
	if fn != nil {
		return fn(target, lastN)
	}
	return f.FakeExecutor.CapturePaneContent(target, lastN)
}

func (f *fakeTmuxCapture) setCaptureFn(fn func(target string, lastN int) (string, error)) {
	f.mu.Lock()
	f.captureFn = fn
	f.mu.Unlock()
}

// orchTestModule wires a Module with a recordingProber installed on the
// orchestrator and the minimal seams needed to interpret screen events
// (sessions, core, registry). Returns the Module + the prober for assertions.
func orchTestModule(t *testing.T) (*Module, *recordingProber, *fakeTmuxCapture) {
	t.Helper()
	m := newTestModule(t)
	rec := newRecordingProber()
	m.probeOrch.watcher = rec

	fake := newFakeTmuxCapture()
	m.tmux = fake
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "s1", Name: "work"}},
	}

	provider := &fakeAgentProvider{
		typeName: "cc",
		alive:    true,
	}
	m.registry.Register(provider)
	return m, rec, fake
}

// seedOrchFrame inserts a verified frame for "work" pane "%5" so
// projectionForSession returns a non-nil projection that the orchestrator
// can patch via setProjectionTopStatus / sweepOnce checks.
func seedOrchFrame(t *testing.T, m *Module, status agentpkg.Status, pid int) {
	t.Helper()
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              pid,
		PPID:             1,
		ProcessStartTime: "live",
		Status:           status,
		StartedAt:        time.Now().UnixNano(),
		LastSeenAt:       time.Now().UnixNano(),
		Verified:         true,
	}); err != nil {
		t.Fatalf("seed frame: %v", err)
	}
}

// --- Slice 3 + Slice 7 (Commit 4) tests ---

// OR3 — recordHookAt; injecting a ScreenChanged within the graceWindow is
// suppressed: no broadcast, no status change, only the suppressed counter
// increments.
func TestOrchestrator_GraceWindowSuppressesEventWithinWindow(t *testing.T) {
	m, _, _ := orchTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusWaiting, 200)
	m.mu.Lock()
	m.activeWatchers["work"] = "cc"
	m.currentStatus["work"] = agentpkg.StatusWaiting
	m.mu.Unlock()

	before := snapshotMetrics()
	m.probeOrch.recordHookAt("work")

	cb := m.probeOrch.makeCallback("work", "cc")
	cb(probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()})

	after := snapshotMetrics()
	if got := after.graceWindowSup - before.graceWindowSup; got != 1 {
		t.Fatalf("graceWindowSuppressed delta = %d, want 1", got)
	}
	if got := after.screenEvent - before.screenEvent; got != 0 {
		t.Fatalf("screenEvent delta = %d, want 0 (suppressed events do not count)", got)
	}
	m.mu.Lock()
	status := m.currentStatus["work"]
	m.mu.Unlock()
	if status != agentpkg.StatusWaiting {
		t.Fatalf("currentStatus[work] = %q, want StatusWaiting (no transition)", status)
	}
}

// OR4 — graceWindow expires naturally; subsequent ScreenChanged events
// recover. v2.0: dumb probe + transition gate makes this Just Work without
// any Rearm API. Uses the orchestrator's nowFn seam to fast-forward past
// graceWindow.
func TestOrchestrator_GraceWindowExpiresThenContinuousChangedRecovers(t *testing.T) {
	m, _, _ := orchTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusWaiting, 201)
	m.mu.Lock()
	m.activeWatchers["work"] = "cc"
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()

	base := time.Now()
	origNow := orchNowFn
	orchNowFn = func() time.Time { return base }
	t.Cleanup(func() { orchNowFn = origNow })

	before := snapshotMetrics()
	m.probeOrch.recordHookAt("work")
	cb := m.probeOrch.makeCallback("work", "cc")

	// 5 events within the graceWindow — all suppressed.
	for i := 0; i < 5; i++ {
		orchNowFn = func() time.Time { return base.Add(500 * time.Millisecond) }
		cb(probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()})
	}
	mid := snapshotMetrics()
	if got := mid.graceWindowSup - before.graceWindowSup; got != 5 {
		t.Fatalf("graceWindowSuppressed delta = %d, want 5", got)
	}
	if got := mid.screenEvent - before.screenEvent; got != 0 {
		t.Fatalf("screenEvent delta during suppression = %d, want 0", got)
	}

	// Fast-forward past graceWindow + inject again — this one transitions.
	orchNowFn = func() time.Time { return base.Add(3 * time.Second) }
	cb(probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()})

	after := snapshotMetrics()
	if got := after.screenEvent - before.screenEvent; got != 1 {
		t.Fatalf("screenEvent delta after recovery = %d, want 1", got)
	}
	m.mu.Lock()
	status := m.currentStatus["work"]
	m.mu.Unlock()
	if status != agentpkg.StatusRunning {
		t.Fatalf("currentStatus[work] = %q, want StatusRunning after recovery", status)
	}
}

// OR5 — Error Guard: orchestrator never overwrites StatusError.
func TestOrchestrator_ErrorGuardBlocksProbeOverwrite(t *testing.T) {
	m, _, fake := orchTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusError, 202)
	m.mu.Lock()
	m.activeWatchers["work"] = "cc"
	m.currentStatus["work"] = agentpkg.StatusError
	m.mu.Unlock()

	fake.SetPaneContent("work:", "user typed something normal")

	before := snapshotMetrics()
	cb := m.probeOrch.makeCallback("work", "cc")
	cb(probe.ScreenChangeEvent{Kind: probe.ScreenStable, Target: "work:", OccurredAt: time.Now()})

	after := snapshotMetrics()
	if got := after.screenEvent - before.screenEvent; got != 0 {
		t.Fatalf("screenEvent delta = %d, want 0 (Error Guard blocks)", got)
	}
	m.mu.Lock()
	status := m.currentStatus["work"]
	m.mu.Unlock()
	if status != agentpkg.StatusError {
		t.Fatalf("currentStatus[work] = %q, want StatusError preserved", status)
	}
}

// OR6 — stale-callback guard: callback fired after stopWatch / rename does
// not broadcast or update status (R4 fix regression).
func TestOrchestrator_StaleCallbackGuard(t *testing.T) {
	t.Run("post_stopWatch_no_broadcast", func(t *testing.T) {
		m, _, _ := orchTestModule(t)
		seedOrchFrame(t, m, agentpkg.StatusIdle, 203)
		// activeWatchers absent — simulates post-stop.
		m.mu.Lock()
		delete(m.activeWatchers, "work")
		m.currentStatus["work"] = agentpkg.StatusIdle
		m.mu.Unlock()

		before := snapshotMetrics()
		cb := m.probeOrch.makeCallback("work", "cc")
		cb(probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()})

		after := snapshotMetrics()
		if got := after.screenEvent - before.screenEvent; got != 0 {
			t.Fatalf("screenEvent delta = %d, want 0 (stale watcher)", got)
		}
		m.mu.Lock()
		status := m.currentStatus["work"]
		m.mu.Unlock()
		if status != agentpkg.StatusIdle {
			t.Fatalf("currentStatus[work] = %q, want StatusIdle (no transition)", status)
		}
	})
	t.Run("post_rename_currentAgent_mismatch", func(t *testing.T) {
		m, _, _ := orchTestModule(t)
		seedOrchFrame(t, m, agentpkg.StatusIdle, 204)
		// Closure captures agentType="codex", but activeWatchers now records "cc"
		// (simulating rename / agent swap).
		m.mu.Lock()
		m.activeWatchers["work"] = "cc"
		m.currentStatus["work"] = agentpkg.StatusIdle
		m.mu.Unlock()

		before := snapshotMetrics()
		cb := m.probeOrch.makeCallback("work", "codex")
		cb(probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()})

		after := snapshotMetrics()
		if got := after.screenEvent - before.screenEvent; got != 0 {
			t.Fatalf("screenEvent delta = %d, want 0 (currentAgent mismatch)", got)
		}
		m.mu.Lock()
		status := m.currentStatus["work"]
		m.mu.Unlock()
		if status != agentpkg.StatusIdle {
			t.Fatalf("currentStatus[work] = %q, want StatusIdle (no transition)", status)
		}
	})
}

// OR7 — nil prober no-op (R14 fix #2 regression). Partial-init Module
// fixtures (no Init / no prober wired) must tolerate startWatch / stopWatch
// without panicking, and the metrics counters must not be incremented for
// the dropped operations.
func TestOrchestrator_NilProberStartStopNoOp(t *testing.T) {
	m := newTestModule(t)
	m.prober = nil
	m.probeOrch.watcher = nil

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil-prober path panicked: %v", r)
		}
	}()

	before := snapshotMetrics()
	m.probeOrch.startWatch("sess", "missing", probe.WatchOptions{})
	m.probeOrch.stopWatch("sess")
	after := snapshotMetrics()

	if got := after.watchStarted - before.watchStarted; got != 0 {
		t.Fatalf("watchStarted delta = %d, want 0 (nil-prober no-op)", got)
	}
	if got := after.watchStopped - before.watchStopped; got != 0 {
		t.Fatalf("watchStopped delta = %d, want 0 (nil-prober no-op)", got)
	}
}

// OR8 — ScreenStable independent bottom-capture for shell-prompt classifier
// (R14 fix #1 regression). Two sub-cases:
//   (i) bottomContent is shell prompt + dead PID → sweepOnce called
//   (ii) bottomContent is not shell prompt → status broadcast as Idle, no sweep
func TestOrchestrator_ScreenStableUsesBottomCaptureForShellPrompt(t *testing.T) {
	t.Run("shellprompt_deadPID_triggersSweep", func(t *testing.T) {
		m, _, fake := orchTestModule(t)
		// Pane→session mapping is required so projectionForSession can find
		// the frame for "work" and so sweep's afterFrameCleared can resolve
		// the session for cleanup broadcasts.
		fake.SetPaneSessionName("%5", "work")
		seedOrchFrame(t, m, agentpkg.StatusRunning, 250)
		m.mu.Lock()
		m.activeWatchers["work"] = "cc"
		m.currentStatus["work"] = agentpkg.StatusRunning
		m.mu.Unlock()

		// Pretend ev.Content is top-of-pane (no shell prompt) — orchestrator
		// must do an INDEPENDENT bottom capture.
		fake.setCaptureFn(func(target string, lastN int) (string, error) {
			if lastN == 10 && target == "work:" {
				return "$ ", nil
			}
			return "", nil
		})

		// Force PID dead.
		origAlive := isPidAliveFn
		isPidAliveFn = func(int) bool { return false }
		t.Cleanup(func() { isPidAliveFn = origAlive })

		cb := m.probeOrch.makeCallback("work", "cc")
		// ev.Content has top-of-pane info, irrelevant — orchestrator captures bottom independently.
		cb(probe.ScreenChangeEvent{Kind: probe.ScreenStable, Target: "work:", Content: "TOP HEADER\nbusy\nbusy", OccurredAt: time.Now()})

		// orchestrator calls m.sweepOnce() directly. Verify via DB side-effect:
		// dead-PID frame is removed by sweepOnce.
		frames, err := m.frames.ListByPane("%5")
		if err != nil {
			t.Fatalf("ListByPane: %v", err)
		}
		if len(frames) != 0 {
			t.Fatalf("frame count = %d, want 0 after shell-prompt + dead-PID sweep", len(frames))
		}
	})
	t.Run("noShellPrompt_broadcastsIdle", func(t *testing.T) {
		m, _, fake := orchTestModule(t)
		seedOrchFrame(t, m, agentpkg.StatusRunning, 251)
		m.mu.Lock()
		m.activeWatchers["work"] = "cc"
		m.currentStatus["work"] = agentpkg.StatusRunning
		m.mu.Unlock()
		fake.setCaptureFn(func(target string, lastN int) (string, error) {
			return "user typed text\nmore text", nil
		})

		before := snapshotMetrics()
		cb := m.probeOrch.makeCallback("work", "cc")
		cb(probe.ScreenChangeEvent{Kind: probe.ScreenStable, Target: "work:", Content: "anything", OccurredAt: time.Now()})

		after := snapshotMetrics()
		if got := after.screenEvent - before.screenEvent; got != 1 {
			t.Fatalf("screenEvent delta = %d, want 1 (Idle transition)", got)
		}
		m.mu.Lock()
		status := m.currentStatus["work"]
		m.mu.Unlock()
		if status != agentpkg.StatusIdle {
			t.Fatalf("currentStatus[work] = %q, want StatusIdle", status)
		}
		// Frame survives — sweep should NOT have run.
		frames, _ := m.frames.ListByPane("%5")
		if len(frames) != 1 {
			t.Fatalf("frame count = %d, want 1 (no sweep on non-shellprompt path)", len(frames))
		}
	})
}

// OR9 — repeated ScreenChanged dedups via transition gate. v2.0: probe is
// dumb and fires every tick; orchestrator's currentStatus check ensures
// Idle→Running broadcasts once, all subsequent ScreenChanged returns at
// the gate (no broadcast / no metric).
func TestOrchestrator_RepeatedScreenChangedDedupsToOneBroadcast(t *testing.T) {
	m, _, _ := orchTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusIdle, 260)
	m.mu.Lock()
	m.activeWatchers["work"] = "cc"
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()

	before := snapshotMetrics()
	cb := m.probeOrch.makeCallback("work", "cc")
	for i := 0; i < 5; i++ {
		cb(probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()})
	}

	after := snapshotMetrics()
	if got := after.screenEvent - before.screenEvent; got != 1 {
		t.Fatalf("screenEvent delta = %d, want 1 (transition gate dedups 4 of 5)", got)
	}
	m.mu.Lock()
	status := m.currentStatus["work"]
	m.mu.Unlock()
	if status != agentpkg.StatusRunning {
		t.Fatalf("currentStatus[work] = %q, want StatusRunning", status)
	}
}

// OR10 — repeated ScreenStable dedups via transition gate.
func TestOrchestrator_RepeatedScreenStableDedupsToOneBroadcast(t *testing.T) {
	m, _, fake := orchTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusRunning, 261)
	m.mu.Lock()
	m.activeWatchers["work"] = "cc"
	m.currentStatus["work"] = agentpkg.StatusRunning
	m.mu.Unlock()
	// Bottom capture returns no shell prompt → routes to Idle.
	fake.setCaptureFn(func(string, int) (string, error) { return "user prose", nil })

	before := snapshotMetrics()
	cb := m.probeOrch.makeCallback("work", "cc")
	for i := 0; i < 3; i++ {
		cb(probe.ScreenChangeEvent{Kind: probe.ScreenStable, Target: "work:", OccurredAt: time.Now()})
	}

	after := snapshotMetrics()
	if got := after.screenEvent - before.screenEvent; got != 1 {
		t.Fatalf("screenEvent delta = %d, want 1 (transition gate dedups 2 of 3)", got)
	}
	m.mu.Lock()
	status := m.currentStatus["work"]
	m.mu.Unlock()
	if status != agentpkg.StatusIdle {
		t.Fatalf("currentStatus[work] = %q, want StatusIdle", status)
	}
}

// FX3 — interpretScreenEvent re-validates the stale-callback guard inside
// the final m.mu critical section in addition to the early fast-path check.
// Regression for codex finding #4 (R2 attack HIGH): previously a concurrent
// stop / rename could mutate activeWatchers between the early unlock and
// the final lock — leaving a ghost broadcast for a watcher that no longer
// exists.
//
// Approach: use the interruptBeforeFinalLockFn test seam. Production sets
// it to a no-op; tests set it to delete activeWatchers["work"] (simulating
// stopWatch winning the race). The orchestrator must observe the deletion
// in the re-check inside the final critical section and return without
// broadcasting / mutating currentStatus / incrementing the counter.
func TestInterpretScreenEvent_RevalidatesStaleCallbackAtomically(t *testing.T) {
	m, _, fake := orchTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusRunning, 290)
	m.mu.Lock()
	m.activeWatchers["work"] = "cc"
	m.currentStatus["work"] = agentpkg.StatusRunning
	m.mu.Unlock()
	// Bottom capture returns prose so ScreenStable would otherwise produce
	// an Idle transition — any broadcast would be an actual ghost.
	fake.setCaptureFn(func(string, int) (string, error) { return "user prose", nil })

	origInterrupt := interruptBeforeFinalLockFn
	interruptBeforeFinalLockFn = func(session string) {
		m.mu.Lock()
		delete(m.activeWatchers, session)
		m.mu.Unlock()
	}
	t.Cleanup(func() { interruptBeforeFinalLockFn = origInterrupt })

	before := snapshotMetrics()
	cb := m.probeOrch.makeCallback("work", "cc")
	cb(probe.ScreenChangeEvent{Kind: probe.ScreenStable, Target: "work:", OccurredAt: time.Now()})

	after := snapshotMetrics()
	if got := after.screenEvent - before.screenEvent; got != 0 {
		t.Fatalf("screenEvent delta = %d, want 0 (atomic re-check should drop the event)", got)
	}
	m.mu.Lock()
	status := m.currentStatus["work"]
	m.mu.Unlock()
	if status != agentpkg.StatusRunning {
		t.Fatalf("currentStatus[work] = %q, want StatusRunning preserved (no transition)", status)
	}
}

// FX4 — startWatch validates WatchOptions (TopLines + BottomLines mutually
// exclusive) and returns false on invalid input. Regression for codex
// finding #7 (R2 defense MEDIUM): previously a silent dead watcher was
// possible — stale-callback saw it as active, MetricProbeWatchStarted
// reported "started", but no goroutine ran.
//
// W3 P1-T2: caller now passes WatchOptions directly (orchestrator no longer
// owns a default profile); this test calls startWatch directly to pin the
// validation surface.
func TestStartWatch_InvalidOptionsRollback(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")

	var logBuf bytes.Buffer
	prevWriter := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(prevWriter)

	m := newTestModule(t)
	rec := newRecordingProber()
	m.probeOrch.watcher = rec

	before := snapshotMetrics()
	ok := m.probeOrch.startWatch("sess", "cc", probe.WatchOptions{TopLines: 5, BottomLines: 5})
	after := snapshotMetrics()

	if ok {
		t.Fatalf("startWatch returned true for invalid opts (TopLines+BottomLines both > 0); want false")
	}

	// Metric must NOT increment — it is reserved for actually-registered
	// watchers so /debug/vars stays an honest counter.
	if got := after.watchStarted - before.watchStarted; got != 0 {
		t.Fatalf("MetricProbeWatchStarted delta = %d, want 0 (invalid opts must not bump the counter)", got)
	}

	// And the prober must not have observed a Watch call for this target.
	rec.mu.Lock()
	_, watched := rec.watchOpts["sess:"]
	rec.mu.Unlock()
	if watched {
		t.Fatalf("recordingProber observed Watch on sess: despite invalid opts")
	}

	// Dev-mode log captures the structured invalid-opts surface.
	const wantLog = "[probe] startWatch invalid opts session=sess agent=cc TopLines=5 BottomLines=5"
	if !strings.Contains(logBuf.String(), wantLog) {
		t.Fatalf("dev log missing %q\nfull log:\n%s", wantLog, logBuf.String())
	}
}

// FX5a — ScreenStable on a stable-Idle session with a live top-frame PID
// MUST skip the bottom CapturePaneContent and the transition entirely.
// Regression for codex finding #8 (R2 體質 MEDIUM): continuous-stable Idle
// panes were issuing tmux subprocess calls every N ticks just to be dropped
// at the transition gate.
func TestScreenStable_StableIdleAlivePidSkipsBottomCapture(t *testing.T) {
	m, _, fake := orchTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusIdle, 295)
	m.mu.Lock()
	m.activeWatchers["work"] = "cc"
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()

	// Force PID alive (default already does, but be explicit).
	origAlive := isPidAliveFn
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() { isPidAliveFn = origAlive })

	before := snapshotMetrics()
	cb := m.probeOrch.makeCallback("work", "cc")
	cb(probe.ScreenChangeEvent{Kind: probe.ScreenStable, Target: "work:", OccurredAt: time.Now()})

	after := snapshotMetrics()
	if got := after.screenEvent - before.screenEvent; got != 0 {
		t.Fatalf("screenEvent delta = %d, want 0 (stable-Idle + alive PID is a no-op)", got)
	}
	fake.mu.Lock()
	calls := len(fake.captureCall)
	fake.mu.Unlock()
	if calls != 0 {
		t.Fatalf("CapturePaneContent calls = %d, want 0 (cheap gate must skip the bottom capture)", calls)
	}
}

// FX5b — dead-PID + shell-prompt sweep path is preserved when prev == Idle.
// The cheap gate must only skip when the top-frame PID is alive; a dead PID
// still needs the bottom capture to drive shell-prompt classification +
// sweep cleanup.
func TestScreenStable_StableIdleDeadPidStillTriggersSweep(t *testing.T) {
	m, _, fake := orchTestModule(t)
	fake.SetPaneSessionName("%5", "work")
	seedOrchFrame(t, m, agentpkg.StatusIdle, 296)
	m.mu.Lock()
	m.activeWatchers["work"] = "cc"
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()

	fake.setCaptureFn(func(target string, lastN int) (string, error) {
		if lastN == 10 && target == "work:" {
			return "$ ", nil
		}
		return "", nil
	})

	origAlive := isPidAliveFn
	isPidAliveFn = func(int) bool { return false }
	t.Cleanup(func() { isPidAliveFn = origAlive })

	cb := m.probeOrch.makeCallback("work", "cc")
	cb(probe.ScreenChangeEvent{Kind: probe.ScreenStable, Target: "work:", OccurredAt: time.Now()})

	// Dead-PID + shell-prompt → sweepOnce runs and removes the frame.
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0 after dead-PID + shell-prompt sweep (cheap gate must NOT skip when PID dead)", len(frames))
	}
}

// OB1 — startWatch/stopWatch increment the watch_started/watch_stopped counters.
func TestMetrics_WatchStartedIncrements(t *testing.T) {
	m := newTestModule(t)
	rec := newRecordingProber()
	m.probeOrch.watcher = rec
	provider := &fakeAgentProvider{typeName: "cc"}
	m.registry.Register(provider)

	before := snapshotMetrics()
	m.probeOrch.startWatch("sess", "cc", probe.WatchOptions{})
	m.probeOrch.stopWatch("sess")
	after := snapshotMetrics()

	if got := after.watchStarted - before.watchStarted; got != 1 {
		t.Fatalf("watchStarted delta = %d, want 1", got)
	}
	if got := after.watchStopped - before.watchStopped; got != 1 {
		t.Fatalf("watchStopped delta = %d, want 1", got)
	}
}

// OB2 — accepted-transition ScreenChanged + ScreenStable each increment
// the screen-event counter once.
func TestMetrics_ScreenEventIncrements(t *testing.T) {
	m, _, fake := orchTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusIdle, 270)
	m.mu.Lock()
	m.activeWatchers["work"] = "cc"
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()
	fake.setCaptureFn(func(string, int) (string, error) { return "user prose", nil })

	before := snapshotMetrics()
	cb := m.probeOrch.makeCallback("work", "cc")
	cb(probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()})
	// Now currentStatus = Running; ScreenStable transitions to Idle.
	cb(probe.ScreenChangeEvent{Kind: probe.ScreenStable, Target: "work:", OccurredAt: time.Now()})

	after := snapshotMetrics()
	if got := after.screenEvent - before.screenEvent; got != 2 {
		t.Fatalf("screenEvent delta = %d, want 2", got)
	}
}

// OB3 — graceWindow suppression increments its own counter.
func TestMetrics_GraceWindowSuppressedIncrements(t *testing.T) {
	m, _, _ := orchTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusWaiting, 271)
	m.mu.Lock()
	m.activeWatchers["work"] = "cc"
	m.currentStatus["work"] = agentpkg.StatusWaiting
	m.mu.Unlock()

	before := snapshotMetrics()
	m.probeOrch.recordHookAt("work")
	cb := m.probeOrch.makeCallback("work", "cc")
	cb(probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()})

	after := snapshotMetrics()
	if got := after.graceWindowSup - before.graceWindowSup; got != 1 {
		t.Fatalf("graceWindowSuppressed delta = %d, want 1", got)
	}
}

// OB4 — PDX_DEV_MODE=1 emits [probe] log lines; unset suppresses them.
func TestDevMode_LogsGatedByEnv(t *testing.T) {
	t.Run("env_set_emits_log", func(t *testing.T) {
		t.Setenv("PDX_DEV_MODE", "1")
		m, _, _ := orchTestModule(t)

		var buf bytes.Buffer
		origOut := log.Writer()
		log.SetOutput(&buf)
		t.Cleanup(func() { log.SetOutput(origOut) })

		m.probeOrch.recordHookAt("work")

		if !strings.Contains(buf.String(), "[probe]") {
			t.Fatalf("expected [probe] log when PDX_DEV_MODE=1, got %q", buf.String())
		}
	})
	t.Run("env_unset_silent", func(t *testing.T) {
		t.Setenv("PDX_DEV_MODE", "")
		m, _, _ := orchTestModule(t)

		var buf bytes.Buffer
		origOut := log.Writer()
		log.SetOutput(&buf)
		t.Cleanup(func() { log.SetOutput(origOut) })

		m.probeOrch.recordHookAt("work")

		if strings.Contains(buf.String(), "[probe]") {
			t.Fatalf("expected no [probe] log when PDX_DEV_MODE unset, got %q", buf.String())
		}
	})
}


// recordingProber is a test fake that captures Watch/StopWatch invocations.
// Implements the orchestrator's internal proberWatcher interface.
type recordingProber struct {
	mu        sync.Mutex
	watchOpts map[string]probe.WatchOptions
	stops     []string
}

func newRecordingProber() *recordingProber {
	return &recordingProber{watchOpts: make(map[string]probe.WatchOptions)}
}

func (r *recordingProber) Watch(target string, opts probe.WatchOptions, _ probe.ScreenChangeCallback) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.watchOpts[target] = opts
}

func (r *recordingProber) StopWatch(target string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops = append(r.stops, target)
}

