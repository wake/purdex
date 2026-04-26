package agent

import (
	"sort"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

type SessionProjection struct {
	PaneID       string
	PrimaryFrame *store.Frame
	TopFrame     *store.Frame
	Subagents    []agentpkg.SubagentRef
}

func BuildSessionProjections(frames []store.Frame) []SessionProjection {
	byPane := make(map[string][]store.Frame)
	for _, frame := range frames {
		byPane[frame.PaneID] = append(byPane[frame.PaneID], frame)
	}

	paneIDs := make([]string, 0, len(byPane))
	for paneID := range byPane {
		paneIDs = append(paneIDs, paneID)
	}
	sort.Strings(paneIDs)

	projections := make([]SessionProjection, 0, len(paneIDs))
	for _, paneID := range paneIDs {
		if projection := buildPaneProjection(paneID, byPane[paneID]); projection.TopFrame != nil {
			projections = append(projections, projection)
		}
	}
	return projections
}

func buildPaneProjection(paneID string, frames []store.Frame) SessionProjection {
	if len(frames) == 0 {
		return SessionProjection{PaneID: paneID, Subagents: []agentpkg.SubagentRef{}}
	}

	// Phase 3.5 §2.4 — dedup: collect (SourcePID, SourceStartTime) keys
	// from every frame's IsProxy refs and exclude any standalone frame
	// in the pane that matches one of those keys. This hides the
	// partial-state visibility where a SessionStart racer landed
	// standalone but is also already attached as a proxy ref on the
	// canonical parent (cold-start race; hot-path canonicalize left
	// behind a partial state). Avoids the SPA seeing two competing
	// frames for the same proxy-collapsed agent.
	type claim struct {
		pid       int
		startTime string
	}
	claimed := make(map[claim]bool)
	for _, frame := range frames {
		for _, ref := range frame.Subagents {
			if ref.IsProxy {
				claimed[claim{ref.SourcePID, ref.SourceStartTime}] = true
			}
		}
	}

	visible := make([]store.Frame, 0, len(frames))
	var hidden int
	for _, frame := range frames {
		if claimed[claim{frame.PID, frame.ProcessStartTime}] {
			// Codex round 2 #Q1: dedup hides race-window standalones
			// (no own state) so SPA sees the canonical parent + proxy
			// ref. But if a partial canonicalization (parent attached
			// proxy ref + DeleteIfUnchanged failed) is followed by a
			// concurrent SubagentStart on the still-standalone child,
			// the child has accumulated its own native ref. Hiding
			// such a frame would erase that ref from projection — the
			// parent's IsProxy ref aggregates the child as a single
			// dot, not a fan-out. Keep stateful children visible
			// until sweep canonicalize (PR-3.5b) migrates the refs.
			if len(frame.Subagents) == 0 {
				hidden++
				continue
			}
		}
		visible = append(visible, frame)
	}
	if hidden > 0 {
		agentpkg.MetricProjectionDedupHidden.Add(int64(hidden))
	}
	if len(visible) == 0 {
		// Defensive: every frame in the pane was claimed by a proxy
		// ref (extreme edge case — pane has only proxy-attached frames
		// and no canonical owner). Falling back to the unfiltered
		// projection avoids dropping the pane entirely; sweep prune
		// would otherwise repair via pruneDeadProxyRefs (PR-3.5b).
		visible = frames
	}

	sorted := append([]store.Frame(nil), visible...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].StartedAt == sorted[j].StartedAt {
			return sorted[i].FrameID < sorted[j].FrameID
		}
		return sorted[i].StartedAt < sorted[j].StartedAt
	})

	primary := sorted[0]
	top := sorted[len(sorted)-1]
	subagents := append([]agentpkg.SubagentRef(nil), top.Subagents...)
	if subagents == nil {
		subagents = []agentpkg.SubagentRef{}
	}
	return SessionProjection{
		PaneID:       paneID,
		PrimaryFrame: &primary,
		TopFrame:     &top,
		Subagents:    subagents,
	}
}
