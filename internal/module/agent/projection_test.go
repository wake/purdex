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
			Subagents: []string{"sub-1"},
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
	if len(projections[0].Subagents) != 1 || projections[0].Subagents[0] != "sub-1" {
		t.Fatalf("subagents = %v, want [sub-1]", projections[0].Subagents)
	}
}
