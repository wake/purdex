package store

import (
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
