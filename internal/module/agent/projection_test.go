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

// PD4 — codex round 3 #R1 fix supersedes round 2 #Q1: dedup HIDES a
// claimed stateful child (uniformly, regardless of own state) BUT merges
// its Subagents into the projection.Subagents output so the SPA still
// sees the child's native refs on the canonical parent's subagents list.
// Q1 kept the child visible — that produced TopFrame ambiguity (older
// parent vs newer child winning Top by StartedAt) and dropped either
// the parent's IsProxy ref or the child's native ref depending on
// selection. R1 picks one canonical owner (the unclaimed parent) and
// preserves both refs by merging.
//
// Race scenario unchanged from Q1 trail:
//   1. cc + codex SessionStart race → standalone codex created
//   2. cc reconcile attaches IsProxy ref to cc but the DeleteIfUnchanged
//      against the codex row fails (concurrent writer)
//   3. A SubagentStart hook for codex arrives and writes a native ref
//      into codex.Subagents
//
// Result: TopFrame == cc; Subagents = [cc's IsProxy codex ref,
// child's native task-codex-1 ref] (both preserved on wire).
func TestProjection_DedupKeepsClaimedStandaloneWithNativeSubagents(t *testing.T) {
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
			FrameID:          "codex-standalone-with-state",
			PaneID:           "%5",
			AgentType:        "codex",
			PID:              200,
			ProcessStartTime: "t200",
			StartedAt:        20,
			// Codex frame has own native subagent (post-partial-state
			// concurrent SubagentStart)
			Subagents: []agentpkg.SubagentRef{{
				ID:        "task-codex-1",
				Type:      "codex",
				StartedAt: 30,
			}},
		},
	})

	// R1: cc is the canonical owner (unclaimed); codex is hidden but
	// its native ref is merged into projection.Subagents.
	if projection.TopFrame == nil || projection.TopFrame.FrameID != "cc-1" {
		t.Fatalf("TopFrame = %+v, want cc-1 (R1: claimed child hidden uniformly)", projection.TopFrame)
	}
	if projection.PrimaryFrame == nil || projection.PrimaryFrame.FrameID != "cc-1" {
		t.Fatalf("PrimaryFrame = %+v, want cc-1", projection.PrimaryFrame)
	}
	hasProxy := false
	hasNative := false
	for _, ref := range projection.Subagents {
		if ref.IsProxy && ref.SourcePID == 200 && ref.SourceStartTime == "t200" {
			hasProxy = true
		}
		if !ref.IsProxy && ref.ID == "task-codex-1" {
			hasNative = true
		}
	}
	if !hasProxy || !hasNative {
		t.Fatalf("Subagents = %+v, want both proxy(codex:200:t200) and native(task-codex-1) — R1 merge missing", projection.Subagents)
	}
	delta := agentpkg.MetricProjectionDedupHidden.Value() - startMetric
	if delta != 1 {
		t.Fatalf("MetricProjectionDedupHidden delta = %d, want +1 (claimed stateful child hidden under R1)", delta)
	}
}

// PD5 — guard the existing PD1 case still hides empty-Subagents standalone.
// Together with PD4 these establish the gate boundary at len > 0.
func TestProjection_DedupStillHidesEmptyStandalone(t *testing.T) {
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
			FrameID:          "codex-empty",
			PaneID:           "%5",
			AgentType:        "codex",
			PID:              200,
			ProcessStartTime: "t200",
			StartedAt:        20,
			// No own subagents — race-window standalone safe to hide
		},
	})
	if projection.TopFrame == nil || projection.TopFrame.FrameID != "cc-1" {
		t.Fatalf("TopFrame = %+v, want cc-1 (empty standalone must still be hidden)", projection.TopFrame)
	}
	delta := agentpkg.MetricProjectionDedupHidden.Value() - startMetric
	if delta != 1 {
		t.Fatalf("MetricProjectionDedupHidden delta = %d, want +1 (empty standalone hidden)", delta)
	}
}

// PD6 — codex round 3 #R1: dedup merges a hidden stateful child's
// Subagents into the projection output even when the parent is the
// older frame (parent.StartedAt < child.StartedAt — child would have
// won TopFrame without dedup). The merge moves the child's native ref
// onto the canonical parent's subagents list so wire output keeps both
// the parent's IsProxy ref and the child's native ref.
func TestProjection_DedupMergesHiddenStatefulChildSubagents(t *testing.T) {
	startMetric := agentpkg.MetricProjectionDedupHidden.Value()
	projection := buildPaneProjection("%5", []store.Frame{
		{
			FrameID:          "cc-1",
			PaneID:           "%5",
			AgentType:        "cc",
			PID:              100,
			ProcessStartTime: "t100",
			StartedAt:        10, // older
			Subagents: []agentpkg.SubagentRef{{
				ID:              "proxy:codex:200:t200",
				Type:            "codex",
				SourcePID:       200,
				SourceStartTime: "t200",
				IsProxy:         true,
			}},
		},
		{
			FrameID:          "codex-1",
			PaneID:           "%5",
			AgentType:        "codex",
			PID:              200,
			ProcessStartTime: "t200",
			StartedAt:        20, // newer — would win Top without dedup
			Subagents: []agentpkg.SubagentRef{{
				ID:        "task-1",
				Type:      "codex",
				StartedAt: 30,
			}},
		},
	})

	if projection.TopFrame == nil || projection.TopFrame.FrameID != "cc-1" {
		t.Fatalf("TopFrame = %+v, want cc-1 (claimed child hidden, parent canonical)", projection.TopFrame)
	}
	if len(projection.Subagents) != 2 {
		t.Fatalf("Subagents count = %d, want 2 (proxy + merged native)", len(projection.Subagents))
	}
	hasProxy := false
	hasNative := false
	for _, ref := range projection.Subagents {
		if ref.IsProxy && ref.SourcePID == 200 && ref.SourceStartTime == "t200" {
			hasProxy = true
		}
		if !ref.IsProxy && ref.ID == "task-1" {
			hasNative = true
		}
	}
	if !hasProxy || !hasNative {
		t.Fatalf("Subagents = %+v, want both proxy and merged native(task-1)", projection.Subagents)
	}
	if delta := agentpkg.MetricProjectionDedupHidden.Value() - startMetric; delta != 1 {
		t.Fatalf("MetricProjectionDedupHidden delta = %d, want +1", delta)
	}
}

// PD7 — codex round 3 #R1: dedup merges hidden stateful child's
// subagents even when the parent is newer. Reverse-StartedAt of PD6:
// parent.StartedAt > child.StartedAt — without dedup the parent would
// already have been Top, but the child carries its own native ref. R1
// merges the child's ref onto the parent's projection.Subagents list
// even though the parent's selection wasn't ambiguous.
//
// This guards the merge logic against a "hide-only-if-overruled"
// shortcut. Claimed standalones are hidden uniformly; merge runs
// uniformly. The final state matches PD6.
func TestProjection_DedupMergesHiddenStatefulChildSubagents_ParentNewer(t *testing.T) {
	startMetric := agentpkg.MetricProjectionDedupHidden.Value()
	projection := buildPaneProjection("%5", []store.Frame{
		{
			FrameID:          "cc-1",
			PaneID:           "%5",
			AgentType:        "cc",
			PID:              100,
			ProcessStartTime: "t100",
			StartedAt:        20, // newer
			Subagents: []agentpkg.SubagentRef{{
				ID:              "proxy:codex:200:t200",
				Type:            "codex",
				SourcePID:       200,
				SourceStartTime: "t200",
				IsProxy:         true,
			}},
		},
		{
			FrameID:          "codex-1",
			PaneID:           "%5",
			AgentType:        "codex",
			PID:              200,
			ProcessStartTime: "t200",
			StartedAt:        10, // older
			Subagents: []agentpkg.SubagentRef{{
				ID:        "task-1",
				Type:      "codex",
				StartedAt: 30,
			}},
		},
	})

	if projection.TopFrame == nil || projection.TopFrame.FrameID != "cc-1" {
		t.Fatalf("TopFrame = %+v, want cc-1", projection.TopFrame)
	}
	if len(projection.Subagents) != 2 {
		t.Fatalf("Subagents count = %d, want 2 (proxy + merged native)", len(projection.Subagents))
	}
	hasProxy := false
	hasNative := false
	for _, ref := range projection.Subagents {
		if ref.IsProxy && ref.SourcePID == 200 && ref.SourceStartTime == "t200" {
			hasProxy = true
		}
		if !ref.IsProxy && ref.ID == "task-1" {
			hasNative = true
		}
	}
	if !hasProxy || !hasNative {
		t.Fatalf("Subagents = %+v, want both proxy and merged native(task-1)", projection.Subagents)
	}
	if delta := agentpkg.MetricProjectionDedupHidden.Value() - startMetric; delta != 1 {
		t.Fatalf("MetricProjectionDedupHidden delta = %d, want +1", delta)
	}
}

// PD8 — codex round 3 #R1 boundary: dedup merge avoids double-listing
// a SubagentRef that already lives on the parent under the same kind-
// aware identity. If a hidden child's Subagents list contains a proxy
// ref pointing to the same source as one already on the parent (an
// unusual but possible state e.g. mirrored after a cycle), the merge
// must dedup by subagentRefMatches and not produce two entries.
func TestProjection_DedupMergeAvoidsDuplicateProxyRef(t *testing.T) {
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
			FrameID:          "codex-1",
			PaneID:           "%5",
			AgentType:        "codex",
			PID:              200,
			ProcessStartTime: "t200",
			StartedAt:        20,
			// Same proxy identity as parent's ref (cross-listed state).
			Subagents: []agentpkg.SubagentRef{{
				ID:              "proxy:codex:200:t200",
				Type:            "codex",
				SourcePID:       200,
				SourceStartTime: "t200",
				IsProxy:         true,
			}},
		},
	})

	if projection.TopFrame == nil || projection.TopFrame.FrameID != "cc-1" {
		t.Fatalf("TopFrame = %+v, want cc-1", projection.TopFrame)
	}
	// Only one proxy ref to (200, t200) should appear — not two.
	matches := 0
	for _, ref := range projection.Subagents {
		if ref.IsProxy && ref.SourcePID == 200 && ref.SourceStartTime == "t200" {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("proxy(200,t200) count = %d, want 1 (merge must dedup by identity)", matches)
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
