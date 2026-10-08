package agent

import (
	"encoding/json"
	"fmt"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/modevents"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
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
//  1. cc + codex SessionStart race → standalone codex created
//  2. cc reconcile attaches IsProxy ref to cc but the DeleteIfUnchanged
//     against the codex row fails (concurrent writer)
//  3. A SubagentStart hook for codex arrives and writes a native ref
//     into codex.Subagents
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

// PD9 — codex round 4 #S1: cross-frame native ID collision must NOT be
// treated as duplicate. Different agent families generate native IDs
// independently (e.g. cc's "call-1" vs codex's "call-1" are distinct
// subagents in independent ID spaces). The merge dedup must compare
// (Type, ID) for natives, not ID alone, otherwise the hidden child's
// native ref is silently dropped from the wire output.
func TestBuildPaneProjection_PD9_CrossFrameNativeIDCollisionPreserved(t *testing.T) {
	parent := store.Frame{
		FrameID: "frame-cc-1", PaneID: "%1", AgentType: "cc",
		PID: 100, ProcessStartTime: "T1", StartedAt: 200,
		Subagents: []agentpkg.SubagentRef{
			// cc has its own native call-1
			{ID: "call-1", Type: "cc", IsProxy: false},
			// cc also claims codex (race-window proxy attach happened)
			{ID: "proxy:codex:200:T2", Type: "codex", IsProxy: true,
				SourcePID: 200, SourceStartTime: "T2"},
		},
	}
	child := store.Frame{
		FrameID: "frame-codex-1", PaneID: "%1", AgentType: "codex",
		PID: 200, ProcessStartTime: "T2", StartedAt: 100,
		Subagents: []agentpkg.SubagentRef{
			// codex has its own native call-1 — same ID as cc's but different Type
			{ID: "call-1", Type: "codex", IsProxy: false},
		},
	}
	projection := buildPaneProjection("%1", []store.Frame{parent, child})
	if projection.TopFrame == nil || projection.TopFrame.FrameID != "frame-cc-1" {
		t.Fatalf("expected cc as TopFrame, got %v", projection.TopFrame)
	}
	// Both native refs must survive into projection.Subagents
	var ccCallSeen, codexCallSeen, proxySeen bool
	for _, ref := range projection.Subagents {
		switch {
		case !ref.IsProxy && ref.Type == "cc" && ref.ID == "call-1":
			ccCallSeen = true
		case !ref.IsProxy && ref.Type == "codex" && ref.ID == "call-1":
			codexCallSeen = true
		case ref.IsProxy && ref.SourcePID == 200 && ref.SourceStartTime == "T2":
			proxySeen = true
		}
	}
	if !ccCallSeen {
		t.Errorf("cc native call-1 missing from projection.Subagents: %+v", projection.Subagents)
	}
	if !codexCallSeen {
		t.Errorf("codex native call-1 dropped by cross-frame ID dedup (S1 regression): %+v", projection.Subagents)
	}
	if !proxySeen {
		t.Errorf("proxy ref missing from projection.Subagents: %+v", projection.Subagents)
	}
	if len(projection.Subagents) != 3 {
		t.Errorf("expected 3 refs (cc native, codex native, proxy), got %d: %+v", len(projection.Subagents), projection.Subagents)
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

func TestProjectPane_FiltersDetachedAliveFramesFromTop(t *testing.T) {
	m := newSweepTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "codex",
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
	origAncestor := pidAncestorIncludesFn
	isPidAliveFn = func(pid int) bool { return pid == 200 }
	processStartTimeFn = func(pid int) (string, error) { return "live", nil }
	pidAncestorIncludesFn = func(pid, ancestor int) bool { return false }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		pidAncestorIncludesFn = origAncestor
	})

	proj, err := m.projectPane("%5")
	if err != nil {
		t.Fatalf("projectPane: %v", err)
	}
	if proj == nil || proj.TopFrame != nil {
		t.Fatalf("TopFrame = %+v, want nil for detached frame", proj.TopFrame)
	}
	normalized := buildProjectionNormalized(proj, "cc", "PdxSessionEnd", 1, agentpkg.DeriveResult{Valid: true})
	if normalized.Status != string(agentpkg.StatusClear) {
		b, _ := json.Marshal(normalized)
		t.Fatalf("normalized = %s, want status clear", b)
	}
}

func TestProjectPane_KeepsPaneOwnedFrameAsTop(t *testing.T) {
	m := newSweepTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPanePID("%5", "100")
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
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
	origAncestor := pidAncestorIncludesFn
	isPidAliveFn = func(pid int) bool { return pid == 200 }
	processStartTimeFn = func(pid int) (string, error) { return "live", nil }
	pidAncestorIncludesFn = func(pid, ancestor int) bool { return pid == 200 && ancestor == 100 }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		pidAncestorIncludesFn = origAncestor
	})

	proj, err := m.projectPane("%5")
	if err != nil {
		t.Fatalf("projectPane: %v", err)
	}
	if proj.TopFrame == nil || proj.TopFrame.AgentType != "cc" {
		t.Fatalf("TopFrame = %+v, want cc", proj.TopFrame)
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

// ---- U1-2b-1: the per-tmux-session representative pane ----

// rankPane is a hand-built projection for the selection rule alone: status is
// both the frame's and the effective one (no overlay), bg the pane's own
// background symbol.
func rankPane(pane, frameID string, status agentpkg.Status, startedAt int64, bg string) SessionProjection {
	return SessionProjection{
		PaneID:     pane,
		TopFrame:   &store.Frame{FrameID: frameID, PaneID: pane, AgentType: "cc", Status: status, StartedAt: startedAt},
		Subagents:  []agentpkg.SubagentRef{},
		Status:     status,
		Source:     SourceHook,
		Background: bg,
	}
}

func selectWork(m *Module, projections []SessionProjection) *SessionProjection {
	return m.selectSessionProjectionBy("work", projections, func(string) string { return "work" })
}

// TestSelectSession_HighestPriorityPaneWins: spec 7 - the session shows its
// most urgent pane, not the most recently started one.
func TestSelectSession_HighestPriorityPaneWins(t *testing.T) {
	m := newTestModule(t)
	got := selectWork(m, []SessionProjection{
		rankPane("%5", "f-old", agentpkg.StatusWaiting, 10, ""),
		rankPane("%6", "f-new", agentpkg.StatusRunning, 20, ""),
	})
	if got == nil || got.PaneID != "%5" || got.EffectiveStatus() != agentpkg.StatusWaiting {
		t.Fatalf("selected %+v, want the older waiting pane %%5", got)
	}
}

// TestSelectSession_ErrorBeatsWaiting also pins the rest of the order:
// waiting > running > idle > clear.
func TestSelectSession_ErrorBeatsWaiting(t *testing.T) {
	m := newTestModule(t)
	order := []agentpkg.Status{agentpkg.StatusError, agentpkg.StatusWaiting, agentpkg.StatusRunning, agentpkg.StatusIdle, agentpkg.StatusClear}
	for i, want := range order {
		// every lower-ranked pane is newer, so only the rank can make `want` win
		var ps []SessionProjection
		for j := i; j < len(order); j++ {
			ps = append(ps, rankPane(fmt.Sprintf("%%%d", j), fmt.Sprintf("f%d", j), order[j], int64(10+j), ""))
		}
		got := selectWork(m, ps)
		if got == nil || got.EffectiveStatus() != want {
			t.Fatalf("among %v selected %+v, want %s", order[i:], got, want)
		}
	}
}

// TestSelectSession_TieFallsBackToLatestStart: equal rank - the newer start
// wins; equal start - the larger frame id (deterministic).
func TestSelectSession_TieFallsBackToLatestStart(t *testing.T) {
	m := newTestModule(t)
	got := selectWork(m, []SessionProjection{
		rankPane("%5", "f-a", agentpkg.StatusRunning, 10, ""),
		rankPane("%6", "f-b", agentpkg.StatusRunning, 20, ""),
	})
	if got == nil || got.PaneID != "%6" {
		t.Fatalf("same rank: selected %+v, want the newer pane %%6", got)
	}
	got = selectWork(m, []SessionProjection{
		rankPane("%6", "f-b", agentpkg.StatusIdle, 20, ""),
		rankPane("%5", "f-a", agentpkg.StatusIdle, 20, ""),
		rankPane("%7", "f-c", agentpkg.StatusIdle, 20, ""),
	})
	if got == nil || got.PaneID != "%7" {
		t.Fatalf("same rank and start: selected %+v, want the largest frame id (%%7)", got)
	}
}

// seedRankPane seeds one verified frame of a pane with the given hook status.
func seedRankPane(t *testing.T, m *Module, pane string, pid int, status agentpkg.Status, startedAt int64, sid string) store.Frame {
	t.Helper()
	f, err := m.frames.Upsert(store.Frame{
		PaneID: pane, AgentType: "cc", PID: pid, PPID: 1, ProcessStartTime: fmt.Sprintf("s%d", pid),
		Status: status, StartedAt: startedAt, LastSeenAt: startedAt, Verified: true, SessionID: sid, Cwd: "/w",
	})
	if err != nil {
		t.Fatalf("seed %s: %v", pane, err)
	}
	return f
}

// TestSelectSession_ModOverlayDecidesRank: the rank is the effective status.
// Pane A's frame says idle but its live mod stream says waiting; pane B's
// frame says running and has no mod: A represents the session.
func TestSelectSession_ModOverlayDecidesRank(t *testing.T) {
	m, _ := overlayModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	fakeTmux.SetPaneSessionName("%6", "work")
	m.tmux = fakeTmux
	seedRankPane(t, m, "%5", 501, agentpkg.StatusIdle, 10, modSID1)
	seedRankPane(t, m, "%6", 502, agentpkg.StatusRunning, 20, modSID2)
	feedMod(m, modStrm, modStart, modTurnStart,
		modEv(modSID1, modevents.TypeToolCheck, `{"tool_use_id":"t1","decision":"ask"}`))

	projections, err := m.liveFrameProjections()
	if err != nil || len(projections) != 2 {
		t.Fatalf("liveFrameProjections: %v (n=%d)", err, len(projections))
	}
	got := m.selectSessionProjection("work", projections)
	if got == nil || got.PaneID != "%5" || got.EffectiveStatus() != agentpkg.StatusWaiting || got.Source != SourceMod {
		t.Fatalf("selected %+v, want pane %%5 waiting from the mod", got)
	}
}

// TestSelectSession_BackgroundIsHighestAcrossPanes: the representative pane
// has no background, another pane runs a workflow - the session shows the
// workflow; the dots, agent type and source stay the representative pane's.
func TestSelectSession_BackgroundIsHighestAcrossPanes(t *testing.T) {
	m := newTestModule(t)
	rep := rankPane("%5", "f-rep", agentpkg.StatusWaiting, 10, "")
	rep.Subagents = []agentpkg.SubagentRef{{ID: "dot-rep", Type: "cc"}}
	other := rankPane("%6", "f-other", agentpkg.StatusIdle, 20, "workflow")
	other.Subagents = []agentpkg.SubagentRef{{ID: "dot-other", Type: "cc"}}
	third := rankPane("%7", "f-third", agentpkg.StatusIdle, 30, "schedule")
	in := []SessionProjection{rep, other, third}

	got := selectWork(m, in)
	if got == nil || got.PaneID != "%5" {
		t.Fatalf("selected %+v, want the waiting pane %%5", got)
	}
	if got.Background != "workflow" {
		t.Fatalf("background = %q, want workflow (highest across panes)", got.Background)
	}
	if len(got.Subagents) != 1 || got.Subagents[0].ID != "dot-rep" || got.Source != SourceHook || got.TopFrame.AgentType != "cc" {
		t.Fatalf("dots/source/type must stay the representative pane's, got %+v", got)
	}
	if in[0].Background != "" {
		t.Fatalf("the input slice was mutated: %+v", in[0])
	}

	// workflow > monitor > schedule > none
	for _, c := range []struct{ a, b, want string }{
		{"", "schedule", "schedule"}, {"schedule", "monitor", "monitor"}, {"monitor", "workflow", "workflow"}, {"workflow", "", "workflow"}, {"", "", ""},
	} {
		got := selectWork(m, []SessionProjection{
			rankPane("%5", "f1", agentpkg.StatusRunning, 20, c.a),
			rankPane("%6", "f2", agentpkg.StatusIdle, 10, c.b),
		})
		if got.Background != c.want {
			t.Errorf("backgrounds %q + %q: got %q, want %q", c.a, c.b, got.Background, c.want)
		}
	}
}
