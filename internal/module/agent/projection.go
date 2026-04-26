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
	// Codex round 3 #R1: collect Subagents from claimed-and-stateful
	// frames so their refs survive into the projection even though the
	// child frame is suppressed by dedup. Without this, the partial-
	// canonicalization race (reconcileCreatedFrameAsProxy attached the
	// child as proxy on parent then DeleteIfUnchanged failed) followed
	// by a concurrent SubagentStart on the child leaves wire-format
	// dropping either the parent's proxy ref (if child becomes Top) or
	// the child's native ref (if parent becomes Top). Merging into
	// output preserves both on the canonical parent's subagents list
	// until sweep canonicalize (PR-3.5b) migrates the refs.
	var mergedFromHidden []agentpkg.SubagentRef
	for _, frame := range frames {
		if claimed[claim{frame.PID, frame.ProcessStartTime}] {
			// Race-window standalone with no own state: hide entirely.
			if len(frame.Subagents) == 0 {
				hidden++
				continue
			}
			// Stateful claimed child: hide the frame from visible
			// TopFrame selection BUT merge its Subagents into the
			// output. Suppresses visual duplication while preserving
			// subagent state on wire.
			mergedFromHidden = append(mergedFromHidden, frame.Subagents...)
			hidden++
			continue
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
		// In the fallback path, the merged-hidden refs are already
		// represented on the visible frames themselves (visible == frames),
		// so skip merging to avoid double-listing.
		mergedFromHidden = nil
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
	// R1 merge: append refs from hidden stateful children that aren't
	// already represented in TopFrame.Subagents.
	//
	// Codex round 4 #S1: dedup must be owner-aware here because frames
	// being merged come from different agent families. subagentRefMatches
	// compares native refs by ID alone (correct WITHIN a frame, where the
	// same agent family doesn't generate colliding IDs), but cross-frame
	// native IDs are independent ID spaces — cc's "call-1" and codex's
	// "call-1" are distinct subagents. Inline owner-aware dedup:
	//   - Proxy refs: dedup by (SourcePID, SourceStartTime). IsProxy
	//     claims target the SAME source process across frames, so the
	//     identity carries across the frame boundary.
	//   - Native refs: dedup by (Type, ID). Different Type means a
	//     different agent family, hence a different ID space.
	// subagentRefMatches semantics are unchanged — within-frame callers
	// in frame_ops.go (SubagentStart/Stop on a single frame) never see a
	// cross-family collision because each frame belongs to one family.
	for _, refFromHidden := range mergedFromHidden {
		duplicate := false
		for _, existing := range subagents {
			if refFromHidden.IsProxy {
				if existing.IsProxy &&
					existing.SourcePID == refFromHidden.SourcePID &&
					existing.SourceStartTime == refFromHidden.SourceStartTime {
					duplicate = true
					break
				}
			} else {
				if !existing.IsProxy &&
					existing.Type == refFromHidden.Type &&
					existing.ID == refFromHidden.ID {
					duplicate = true
					break
				}
			}
		}
		if !duplicate {
			subagents = append(subagents, refFromHidden)
		}
	}
	return SessionProjection{
		PaneID:       paneID,
		PrimaryFrame: &primary,
		TopFrame:     &top,
		Subagents:    subagents,
	}
}
