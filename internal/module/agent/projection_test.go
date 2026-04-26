package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

func TestProjection_TopFrameWins(t *testing.T) {
	projection := buildPaneProjection("%5", []store.Frame{
		{
			FrameID:   "a",
			PaneID:    "%5",
			AgentType: "cc",
			Status:    agentpkg.StatusIdle,
			StartedAt: 10,
		},
		{
			FrameID:   "b",
			PaneID:    "%5",
			AgentType: "codex",
			Status:    agentpkg.StatusRunning,
			StartedAt: 20,
		},
	})

	if projection.PrimaryFrame == nil || projection.PrimaryFrame.AgentType != "cc" {
		t.Fatalf("primary = %+v, want cc", projection.PrimaryFrame)
	}
	if projection.TopFrame == nil || projection.TopFrame.AgentType != "codex" {
		t.Fatalf("top = %+v, want codex", projection.TopFrame)
	}
}

func TestProjection_CcAndCodexCoexist(t *testing.T) {
	projections := BuildSessionProjections([]store.Frame{
		{
			FrameID:   "cc-1",
			PaneID:    "%5",
			AgentType: "cc",
			Status:    agentpkg.StatusIdle,
			StartedAt: 10,
		},
		{
			FrameID:   "codex-1",
			PaneID:    "%5",
			AgentType: "codex",
			Status:    agentpkg.StatusRunning,
			StartedAt: 20,
			Subagents: []agentpkg.SubagentRef{{ID: "sub-1", Type: "codex"}},
		},
	})

	if len(projections) != 1 {
		t.Fatalf("projection count = %d, want 1", len(projections))
	}
	if projections[0].PrimaryFrame == nil || projections[0].PrimaryFrame.AgentType != "cc" {
		t.Fatalf("primary = %+v, want cc", projections[0].PrimaryFrame)
	}
	if projections[0].TopFrame == nil || projections[0].TopFrame.AgentType != "codex" {
		t.Fatalf("top = %+v, want codex", projections[0].TopFrame)
	}
	if len(projections[0].Subagents) != 1 || projections[0].Subagents[0].ID != "sub-1" {
		t.Fatalf("subagents = %v, want [sub-1]", projections[0].Subagents)
	}
}

// ---------------------------------------------------------------------------
// Phase 3.5 PR-3.5a — buildPaneProjection dedup (plan §2.4 / §3.2 PD1-PD3)
// ---------------------------------------------------------------------------

// PD1 — buildPaneProjection excludes a standalone frame whose
// (PID, ProcessStartTime) matches an IsProxy ref carried by another frame
// in the same pane (cold-start race partial state).
func TestProjection_DedupExcludesProxyClaimedStandalone(t *testing.T) {
	startMetric := agentpkg.MetricProjectionDedupHidden.Value()
	projection := buildPaneProjection("%5", []store.Frame{
		{
			FrameID:          "cc-1",
			PaneID:           "%5",
			AgentType:        "cc",
			PID:              100,
			ProcessStartTime: "t100",
			StartedAt:        10,
			Subagents: []agentpkg.SubagentRef{{
				ID:              "proxy:codex:200:t200",
				Type:            "codex",
				SourcePID:       200,
				SourceStartTime: "t200",
				IsProxy:         true,
			}},
		},
		{
			FrameID:          "codex-standalone",
			PaneID:           "%5",
			AgentType:        "codex",
			PID:              200,
			ProcessStartTime: "t200",
			StartedAt:        20, // newer — without dedup would win TopFrame
		},
	})

	if projection.TopFrame == nil || projection.TopFrame.FrameID != "cc-1" {
		t.Fatalf("TopFrame = %+v, want cc-1 (codex standalone hidden by dedup)", projection.TopFrame)
	}
	if projection.PrimaryFrame == nil || projection.PrimaryFrame.FrameID != "cc-1" {
		t.Fatalf("PrimaryFrame = %+v, want cc-1", projection.PrimaryFrame)
	}
	delta := agentpkg.MetricProjectionDedupHidden.Value() - startMetric
	if delta != 1 {
		t.Fatalf("MetricProjectionDedupHidden delta = %d, want +1", delta)
	}
}

// PD2 — when every frame in the pane is claimed by some proxy ref (extreme
// edge case, e.g. cycle), buildPaneProjection falls back to the unfiltered
// list rather than dropping the pane projection entirely.
func TestProjection_FallbackWhenAllFramesClaimed(t *testing.T) {
	projection := buildPaneProjection("%5", []store.Frame{
		{
			FrameID:          "a",
			PaneID:           "%5",
			AgentType:        "cc",
			PID:              100,
			ProcessStartTime: "t100",
			StartedAt:        10,
			// a claims b
			Subagents: []agentpkg.SubagentRef{{
				ID:              "proxy:codex:200:t200",
				Type:            "codex",
				SourcePID:       200,
				SourceStartTime: "t200",
				IsProxy:         true,
			}},
		},
		{
			FrameID:          "b",
			PaneID:           "%5",
			AgentType:        "codex",
			PID:              200,
			ProcessStartTime: "t200",
			StartedAt:        20,
			// b claims a (cycle)
			Subagents: []agentpkg.SubagentRef{{
				ID:              "proxy:cc:100:t100",
				Type:            "cc",
				SourcePID:       100,
				SourceStartTime: "t100",
				IsProxy:         true,
			}},
		},
	})

	if projection.TopFrame == nil {
		t.Fatalf("TopFrame is nil — fallback should keep pane visible")
	}
}

// PD3 — without IsProxy refs in the pane, dedup logic is a no-op and the
// existing buildPaneProjection behavior is preserved.
func TestProjection_NoProxyRefsUnchangedBehavior(t *testing.T) {
	projection := buildPaneProjection("%5", []store.Frame{
		{FrameID: "a", PaneID: "%5", AgentType: "cc", PID: 100, ProcessStartTime: "t100", StartedAt: 10},
		{FrameID: "b", PaneID: "%5", AgentType: "codex", PID: 200, ProcessStartTime: "t200", StartedAt: 20},
	})
	if projection.PrimaryFrame == nil || projection.PrimaryFrame.FrameID != "a" {
		t.Fatalf("Primary = %+v, want a", projection.PrimaryFrame)
	}
	if projection.TopFrame == nil || projection.TopFrame.FrameID != "b" {
		t.Fatalf("Top = %+v, want b", projection.TopFrame)
	}
}

// IT5 — projection dedup hides a standalone frame whose proxy ref already
// lives on the canonical parent (plan §3.1).
func TestProjection_IT5_PartialStateHiddenByProjectionDedup(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:200:t200",
		Type:            "codex",
		SourcePID:       200,
		SourceStartTime: "t200",
		IsProxy:         true,
	}}
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed parent + ref: %v", err)
	}
	seedFrame(t, m, "%5", "codex", 200, "t200", 50) // partial state row

	startMetric := agentpkg.MetricProjectionDedupHidden.Value()
	proj, err := m.projectPane("%5")
	if err != nil {
		t.Fatalf("projectPane: %v", err)
	}
	if proj.TopFrame == nil || proj.TopFrame.AgentType != "cc" {
		t.Fatalf("TopFrame = %+v, want cc (codex standalone hidden)", proj.TopFrame)
	}
	delta := agentpkg.MetricProjectionDedupHidden.Value() - startMetric
	if delta != 1 {
		t.Fatalf("MetricProjectionDedupHidden delta = %d, want +1", delta)
	}
}

func TestLiveFrameProjections_PreservesFrameOnLookupError(t *testing.T) {
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
	origStart := processStartTimeFn
	processStartTimeFn = func(pid int) (string, error) { return "", errStub("ps failed") }
	t.Cleanup(func() { processStartTimeFn = origStart })

	projections, err := m.liveFrameProjections()
	if err != nil {
		t.Fatalf("liveFrameProjections: %v", err)
	}
	if len(projections) != 1 {
		t.Fatalf("projection count = %d, want 1", len(projections))
	}
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
}
