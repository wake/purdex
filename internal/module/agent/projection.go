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
	sorted := append([]store.Frame(nil), frames...)
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
