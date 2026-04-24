package store

import (
	"database/sql"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
)

func openTestFramesStore(t *testing.T) *FramesStore {
	t.Helper()
	events := openTestAgentEventStore(t)
	frames, err := events.Frames()
	if err != nil {
		t.Fatalf("frames: %v", err)
	}
	return frames
}

func TestFramesStore_UpsertAndRead(t *testing.T) {
	s := openTestFramesStore(t)

	frame, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "Sun Apr 20 01:30:00 2026",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.GetByIdentity("%5", 200, "Sun Apr 20 01:30:00 2026")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("frame not found")
	}
	if got.FrameID != frame.FrameID {
		t.Fatalf("frame_id = %q, want %q", got.FrameID, frame.FrameID)
	}
	if got.Status != agentpkg.StatusIdle {
		t.Fatalf("status = %q, want idle", got.Status)
	}
}

func TestFramesStore_NestedFrames_ParentFrameLink(t *testing.T) {
	s := openTestFramesStore(t)

	parent, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert parent: %v", err)
	}
	child, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "codex",
		PID:              300,
		PPID:             200,
		ProcessStartTime: "B",
		ParentFrameID:    parent.FrameID,
		Status:           agentpkg.StatusRunning,
		StartedAt:        20,
		LastSeenAt:       20,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert child: %v", err)
	}

	got, err := s.GetByIdentity("%5", 300, "B")
	if err != nil {
		t.Fatalf("GetByIdentity child: %v", err)
	}
	if got.ParentFrameID != child.ParentFrameID {
		t.Fatalf("parent_frame_id = %q, want %q", got.ParentFrameID, child.ParentFrameID)
	}
}

func TestFramesStore_OrphanPolicy(t *testing.T) {
	s := openTestFramesStore(t)

	parent, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert parent: %v", err)
	}
	_, err = s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "codex",
		PID:              300,
		PPID:             200,
		ProcessStartTime: "B",
		ParentFrameID:    parent.FrameID,
		Status:           agentpkg.StatusRunning,
		StartedAt:        20,
		LastSeenAt:       20,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert child: %v", err)
	}

	if err := s.Delete(parent.FrameID); err != nil {
		t.Fatalf("Delete parent: %v", err)
	}
	child, err := s.GetByIdentity("%5", 300, "B")
	if err != nil {
		t.Fatalf("GetByIdentity child: %v", err)
	}
	if child == nil {
		t.Fatal("child frame should remain after parent delete")
	}
	if child.ParentFrameID != "" {
		t.Fatalf("parent_frame_id = %q, want empty", child.ParentFrameID)
	}
}

func TestFramesStore_UniqueOnPidAndStartTime(t *testing.T) {
	s := openTestFramesStore(t)

	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame A: %v", err)
	}
	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "B",
		Status:           agentpkg.StatusIdle,
		StartedAt:        20,
		LastSeenAt:       20,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame B: %v", err)
	}

	frames, err := s.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("frames count = %d, want 2", len(frames))
	}
}

func TestFramesStore_UpsertSameIdentityKeepsStoredFrameID(t *testing.T) {
	s := openTestFramesStore(t)

	first, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert first: %v", err)
	}
	second, err := s.Upsert(Frame{
		FrameID:          "other-id",
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             101,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusRunning,
		StartedAt:        10,
		LastSeenAt:       20,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert second: %v", err)
	}
	if second.FrameID != first.FrameID {
		t.Fatalf("frame_id = %q, want %q", second.FrameID, first.FrameID)
	}
	got, err := s.GetByIdentity("%5", 200, "A")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("frame not found")
	}
	if got.FrameID != first.FrameID {
		t.Fatalf("stored frame_id = %q, want %q", got.FrameID, first.FrameID)
	}
	if got.PPID != 101 || got.Status != agentpkg.StatusRunning {
		t.Fatalf("stored frame = %+v, want updated fields", *got)
	}
}

func TestFrames_UpsertAndReadSubagentRefs(t *testing.T) {
	s := openTestFramesStore(t)

	want := []agentpkg.SubagentRef{{ID: "s1", Type: "cc", StartedAt: 10}}
	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Subagents:        want,
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.GetByIdentity("%5", 200, "A")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("frame not found")
	}
	if len(got.Subagents) != 1 {
		t.Fatalf("subagents len = %d, want 1", len(got.Subagents))
	}
	if got.Subagents[0] != want[0] {
		t.Fatalf("subagents[0] = %+v, want %+v", got.Subagents[0], want[0])
	}
}

func TestFrames_EmptySubagentsPreserved(t *testing.T) {
	s := openTestFramesStore(t)

	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Subagents:        nil,
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.GetByIdentity("%5", 200, "A")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("frame not found")
	}
	if got.Subagents == nil {
		t.Fatal("Subagents should be non-nil empty slice, got nil")
	}
	if len(got.Subagents) != 0 {
		t.Fatalf("Subagents len = %d, want 0", len(got.Subagents))
	}
}

func TestFrames_SubagentsJSONShapeSmoke(t *testing.T) {
	s := openTestFramesStore(t)

	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Subagents: []agentpkg.SubagentRef{{
			ID:        "s1",
			Type:      "cc",
			StartedAt: 10,
		}},
		Status:     agentpkg.StatusIdle,
		StartedAt:  10,
		LastSeenAt: 10,
		Verified:   true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var raw string
	err := s.db.QueryRow(`SELECT subagents_json FROM agent_frames WHERE pane_id = ? AND pid = ? AND process_start_time = ?`, "%5", 200, "A").Scan(&raw)
	if err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	for _, key := range []string{`"id"`, `"type"`, `"started_at"`, `"source_pid"`, `"source_start_time"`} {
		if !strings.Contains(raw, key) {
			t.Errorf("subagents_json missing key %s: %s", key, raw)
		}
	}
}

// Seeds a legacy `["id"]` shape row directly via SQL (bypassing Upsert) to
// simulate an agent.sqlite that predates the Phase 2 SubagentRef schema.
func seedLegacyFrameRow(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO agent_frames (
		frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
	) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?)`,
		"legacy-frame", "%5", "cc", 200, 100, "A",
		`["legacy-sub-1","legacy-sub-2"]`, "idle", 10, 10, 1); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
}

func TestMigrateFramesDB_ClearsLegacySubagentsJSON(t *testing.T) {
	events := openTestAgentEventStore(t)
	if _, err := events.Frames(); err != nil {
		t.Fatalf("initial Frames: %v", err)
	}
	seedLegacyFrameRow(t, events.db)

	// Re-run migration (simulates daemon restart).
	if err := migrateFramesDB(events.db); err != nil {
		t.Fatalf("migrateFramesDB: %v", err)
	}

	var count int
	if err := events.db.QueryRow(`SELECT COUNT(*) FROM agent_frames`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("agent_frames count = %d after migrate, want 0 (table should be truncated)", count)
	}
}

// Seeds a row with a malformed `subagents_json` that is neither []SubagentRef
// nor []string — simulates disk corruption or an unrecognized future shape.
func seedMalformedFrameRow(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO agent_frames (
		frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
	) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?)`,
		"malformed-frame", "%5", "cc", 300, 100, "A",
		`not-a-json`, "idle", 10, 10, 1); err != nil {
		t.Fatalf("seed malformed row: %v", err)
	}
}

func TestMigrateFramesDB_ClearsMixedLegacyAndNewRows(t *testing.T) {
	s := openTestFramesStore(t)

	// Insert one new-format row via Upsert.
	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Subagents:        []agentpkg.SubagentRef{{ID: "s1", Type: "cc", StartedAt: 10}},
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert new: %v", err)
	}
	// Seed one legacy row via direct SQL with distinct identity so it does
	// not collide with the new row above.
	if _, err := s.db.Exec(`INSERT INTO agent_frames (
		frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
	) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?)`,
		"legacy-frame-mixed", "%5", "cc", 201, 100, "B",
		`["legacy-id"]`, "idle", 10, 10, 1); err != nil {
		t.Fatalf("seed legacy row (mixed): %v", err)
	}

	if err := migrateFramesDB(s.db); err != nil {
		t.Fatalf("migrateFramesDB: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_frames`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("agent_frames count = %d after mixed migrate, want 0 (entire table truncated when any legacy row present)", count)
	}
}

func TestMigrateFramesDB_FailsOnMalformedSubagentsJSON(t *testing.T) {
	events := openTestAgentEventStore(t)
	if _, err := events.Frames(); err != nil {
		t.Fatalf("initial Frames: %v", err)
	}
	seedMalformedFrameRow(t, events.db)

	err := migrateFramesDB(events.db)
	if err == nil {
		t.Fatal("migrateFramesDB: want error for malformed subagents_json, got nil (daemon would silently wipe unrecognized state)")
	}
	if !strings.Contains(err.Error(), "malformed subagents_json") {
		t.Fatalf("error = %q, want to mention 'malformed subagents_json'", err.Error())
	}

	// Malformed row must still be on disk — daemon refused to truncate.
	var count int
	if err := events.db.QueryRow(`SELECT COUNT(*) FROM agent_frames`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("agent_frames count = %d after failed migrate, want 1 (malformed row preserved for manual inspection)", count)
	}
}

func TestFrames_DeleteIfUnchanged_DeletesWhenMatching(t *testing.T) {
	s := openTestFramesStore(t)

	frame, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	deleted, err := s.DeleteIfUnchanged(frame.FrameID, 10)
	if err != nil {
		t.Fatalf("DeleteIfUnchanged: %v", err)
	}
	if !deleted {
		t.Fatal("DeleteIfUnchanged returned (false, nil); want (true, nil) for matching last_seen_at")
	}

	got, err := s.GetByIdentity("%5", 200, "A")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got != nil {
		t.Fatalf("frame still present after DeleteIfUnchanged: %+v", *got)
	}
}

func TestFrames_DeleteIfUnchanged_SkipsWhenStale(t *testing.T) {
	s := openTestFramesStore(t)

	frame, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Simulate concurrent refresh: LastSeenAt bumped from 10 to 20 before our DELETE runs.
	if _, err := s.Upsert(Frame{
		FrameID:          frame.FrameID,
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       20,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert refresh: %v", err)
	}

	deleted, err := s.DeleteIfUnchanged(frame.FrameID, 10)
	if err != nil {
		t.Fatalf("DeleteIfUnchanged: %v", err)
	}
	if deleted {
		t.Fatal("DeleteIfUnchanged returned true for stale last_seen_at; want false (concurrent refresh)")
	}

	got, err := s.GetByIdentity("%5", 200, "A")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got == nil {
		t.Fatal("frame missing after skip; want preserved")
	}
	if got.LastSeenAt != 20 {
		t.Fatalf("LastSeenAt = %d, want 20", got.LastSeenAt)
	}
}

func TestFrames_DeleteIfUnchanged_NotFound(t *testing.T) {
	s := openTestFramesStore(t)

	deleted, err := s.DeleteIfUnchanged("no-such-frame", 10)
	if err != nil {
		t.Fatalf("DeleteIfUnchanged: %v", err)
	}
	if deleted {
		t.Fatal("DeleteIfUnchanged returned true for missing frame; want false")
	}
}

func TestMigrateFramesDB_PreservesNewSubagentsJSON(t *testing.T) {
	s := openTestFramesStore(t)

	if _, err := s.Upsert(Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Subagents:        []agentpkg.SubagentRef{{ID: "s1", Type: "cc", StartedAt: 10}},
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Re-run migration: new-format row must survive.
	if err := migrateFramesDB(s.db); err != nil {
		t.Fatalf("migrateFramesDB: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_frames`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("agent_frames count = %d after migrate, want 1 (new-format row should survive)", count)
	}
}
