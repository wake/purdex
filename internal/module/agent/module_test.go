package agent

import (
	"errors"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// TestNew_FailsOnMalformedFramesStore exercises the daemon startup path.
// The round-2 migrateFramesDB safeguard only blocks startup if Module.New
// propagates the events.Frames() error to its caller (cmd/pdx/main.go).
// Round-3 codex review (review-mocj2q7w-ypf3wd) flagged that the old
// signature dropped the error, so a malformed on-disk agent_frames would
// come up as m.frames == nil — silent degradation instead of fail-fast.
func TestNew_FailsOnMalformedFramesStore(t *testing.T) {
	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("OpenAgentEvent: %v", err)
	}
	t.Cleanup(func() { _ = events.Close() })

	// Prime the frames table so we can seed a malformed row into it.
	if _, err := events.Frames(); err != nil {
		t.Fatalf("initial Frames: %v", err)
	}
	if _, err := events.ExecRawForTest(`INSERT INTO agent_frames (
		frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
	) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?)`,
		"bad-frame", "%5", "cc", 400, 1, "t0",
		`not-a-json`, "idle", 10, 10, 1); err != nil {
		t.Fatalf("seed malformed row: %v", err)
	}

	// Daemon startup path: New() must surface the migration error so
	// cmd/pdx/main.go's log.Fatalf kicks in.
	m, err := New(events)
	if err == nil {
		t.Fatalf("New: want error for malformed frames row, got nil (module=%v)", m)
	}
	if !strings.Contains(err.Error(), "malformed subagents_json") {
		t.Fatalf("New error = %q, want to mention 'malformed subagents_json'", err.Error())
	}
}

// TestNew_TracesErrorIsNotFatal guards against round-4 regression: the
// module already tolerates m.traces == nil (monitor endpoints degrade, hook
// processing still runs). A trace-store init failure must NOT be elevated to
// a daemon-fatal condition (as it accidentally was in round 3).
func TestNew_TracesErrorIsNotFatal(t *testing.T) {
	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("OpenAgentEvent: %v", err)
	}
	t.Cleanup(func() { _ = events.Close() })

	origTraces := tracesInitFn
	tracesInitFn = func(*store.AgentEventStore) (*store.TraceStore, error) {
		return nil, errors.New("simulated trace migration failure")
	}
	t.Cleanup(func() { tracesInitFn = origTraces })

	m, err := New(events)
	if err != nil {
		t.Fatalf("New: want nil error when traces init fails, got %v", err)
	}
	if m == nil {
		t.Fatal("module should be constructed despite trace init failure")
	}
	if m.traces != nil {
		t.Fatal("m.traces must be nil after simulated trace error (degraded mode)")
	}
	if m.traceSink != nil {
		t.Fatal("m.traceSink must be nil after simulated trace error (degraded mode)")
	}
	// Frames must still be wired — they use the real init path.
	if m.frames == nil {
		t.Fatal("m.frames must be non-nil when events is non-nil and frames init succeeds")
	}
}

// TestManageActivityWatch_DefaultNoOp pins the W3 撤回 contract: regardless
// of the new status passed in (Waiting / Running / Idle / Error / Clear /
// arbitrary), manageActivityWatch never calls startWatch. The probe is
// recovery-only; W6 ProbeIntent will reintroduce explicit start sites.
//
// Driven against recordingProber.watchOpts — empty after every status means
// zero Watch() calls. StopWatch is also expected to be untouched here because
// no prior watcher is seeded (the StopsExistingWatcher test exercises the
// stop branch with a seeded entry).
func TestManageActivityWatch_DefaultNoOp(t *testing.T) {
	m := newTestModule(t)
	rec := newRecordingProber()
	m.probeOrch.watcher = rec

	statuses := []agentpkg.Status{
		agentpkg.StatusWaiting,
		agentpkg.StatusRunning,
		agentpkg.StatusIdle,
		agentpkg.StatusError,
		agentpkg.StatusClear,
	}
	for _, s := range statuses {
		m.manageActivityWatch("sess", "cc", s)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.watchOpts) != 0 {
		t.Fatalf("recordingProber observed Watch calls = %v, want empty (manageActivityWatch must not start watchers)", rec.watchOpts)
	}
	if len(rec.stops) != 0 {
		t.Fatalf("recordingProber observed StopWatch calls = %v, want empty (no prior watcher seeded)", rec.stops)
	}
}

// TestManageActivityWatch_StopsExistingWatcher pins the W3 撤回 stop-only
// path: when a watcher already exists for `session`, manageActivityWatch
// stops it and evicts the activeWatchers entry. Regardless of newStatus, no
// new watcher is started.
func TestManageActivityWatch_StopsExistingWatcher(t *testing.T) {
	m := newTestModule(t)
	rec := newRecordingProber()
	m.probeOrch.watcher = rec

	m.mu.Lock()
	m.activeWatchers["sess"] = "cc"
	m.mu.Unlock()

	m.manageActivityWatch("sess", "cc", agentpkg.StatusIdle)

	m.mu.Lock()
	_, present := m.activeWatchers["sess"]
	m.mu.Unlock()
	if present {
		t.Fatalf("activeWatchers[sess] still present after manageActivityWatch — stop-only eviction missing")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.stops) != 1 || rec.stops[0] != "sess:" {
		t.Fatalf("recordingProber stops = %v, want [sess:]", rec.stops)
	}
	if len(rec.watchOpts) != 0 {
		t.Fatalf("recordingProber observed Watch calls = %v, want empty (no auto-start)", rec.watchOpts)
	}
}

// TestRenameSessionLocked_StopOnly pins the W3 撤回 rename contract:
//   - oldname is removed from activeWatchers
//   - newname is NOT inserted into activeWatchers (stop-only)
//   - StopWatch fires once for oldname:
//   - lastHookAt migrates from oldname → newname (preserves graceWindow so
//     the future W6 caller cannot have a hook-set status overwritten by a
//     probe event arriving for the renamed session within probeGraceWindow)
func TestRenameSessionLocked_StopOnly(t *testing.T) {
	m := newTestModule(t)
	rec := newRecordingProber()
	m.probeOrch.watcher = rec

	m.mu.Lock()
	m.activeWatchers["oldname"] = "cc"
	m.mu.Unlock()
	m.probeOrch.recordHookAt("oldname")

	m.RenameSession("oldname", "newname")

	m.mu.Lock()
	if _, ok := m.activeWatchers["oldname"]; ok {
		m.mu.Unlock()
		t.Fatalf("activeWatchers[oldname] still present after rename")
	}
	if _, ok := m.activeWatchers["newname"]; ok {
		m.mu.Unlock()
		t.Fatalf("activeWatchers[newname] present after rename — W3 stop-only must not auto-start")
	}
	m.mu.Unlock()

	rec.mu.Lock()
	if len(rec.stops) != 1 || rec.stops[0] != "oldname:" {
		rec.mu.Unlock()
		t.Fatalf("recordingProber stops = %v, want [oldname:]", rec.stops)
	}
	if len(rec.watchOpts) != 0 {
		rec.mu.Unlock()
		t.Fatalf("recordingProber observed Watch calls = %v, want empty (rename is stop-only)", rec.watchOpts)
	}
	rec.mu.Unlock()

	// graceWindow migration: lastHookAt[oldname] cleared, lastHookAt[newname]
	// populated.
	m.probeOrch.graceMu.Lock()
	if _, ok := m.probeOrch.lastHookAt["oldname"]; ok {
		m.probeOrch.graceMu.Unlock()
		t.Fatalf("lastHookAt[oldname] still present after rename — migration missing")
	}
	if _, ok := m.probeOrch.lastHookAt["newname"]; !ok {
		m.probeOrch.graceMu.Unlock()
		t.Fatalf("lastHookAt[newname] missing after rename — graceWindow lost")
	}
	m.probeOrch.graceMu.Unlock()
}

// P1-T3: lookupTopFrameForSessionLocked tests
// -------------------------------------------
// helper returns the top-frame's pane_id + pid for a session under m.mu.
// Used by the W6-3 ProbeIntent dispatcher to capture the (paneID, senderPID)
// snapshot when arming a detector. Tests run with m.mu held to mirror the
// production caller contract.

// TestLookupTopFrameForSessionLocked_HappyPath seeds one frame for a session
// and asserts the helper returns its pane_id + pid.
func TestLookupTopFrameForSessionLocked_HappyPath(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux

	if _, err := m.frames.Upsert(store.Frame{
		FrameID:          "frame-1",
		PaneID:           "%5",
		AgentType:        "codex",
		PID:              4242,
		PPID:             1,
		ProcessStartTime: "Sun Apr 20 01:30:00 2026",
		Status:           agentpkg.StatusRunning,
		StartedAt:        100,
		LastSeenAt:       120,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}

	m.mu.Lock()
	paneID, pid, ok := m.lookupTopFrameForSessionLocked("work")
	m.mu.Unlock()

	if !ok {
		t.Fatalf("lookupTopFrameForSessionLocked: ok=false, want true")
	}
	if paneID != "%5" {
		t.Fatalf("paneID = %q, want %%5", paneID)
	}
	if pid != 4242 {
		t.Fatalf("pid = %d, want 4242", pid)
	}
}

// TestLookupTopFrameForSessionLocked_NoSession_NotOk asserts an unknown
// session returns ok=false (no panic, no stale read).
func TestLookupTopFrameForSessionLocked_NoSession_NotOk(t *testing.T) {
	m := newTestModule(t)
	m.tmux = tmux.NewFakeExecutor()

	m.mu.Lock()
	paneID, pid, ok := m.lookupTopFrameForSessionLocked("ghost")
	m.mu.Unlock()

	if ok {
		t.Fatalf("ok=true for unknown session, want false")
	}
	if paneID != "" || pid != 0 {
		t.Fatalf("paneID=%q pid=%d, want empty/0 for missing session", paneID, pid)
	}
}

// TestLookupTopFrameForSessionLocked_NoTopFrame_NotOk seeds tmux pane
// resolution but no frames row — projection.TopFrame is nil, helper returns
// ok=false.
func TestLookupTopFrameForSessionLocked_NoTopFrame_NotOk(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux

	m.mu.Lock()
	paneID, pid, ok := m.lookupTopFrameForSessionLocked("work")
	m.mu.Unlock()

	if ok {
		t.Fatalf("ok=true with no frames seeded, want false")
	}
	if paneID != "" || pid != 0 {
		t.Fatalf("paneID=%q pid=%d, want empty/0 for missing top frame", paneID, pid)
	}
}
