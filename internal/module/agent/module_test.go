package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/store"
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
