package agent

import (
	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

func (m *Module) applyFrameEvent(req EventRequest, result agentpkg.DeriveResult, broadcastTs int64) (*SessionProjection, error) {
	if m.frames == nil {
		return nil, nil
	}
	if req.EventName != "SubagentStart" && req.EventName != "SubagentStop" && !result.Valid {
		return m.projectPane(req.TmuxPaneID)
	}

	frame, err := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
	if err != nil {
		return nil, err
	}

	switch req.EventName {
	case "SessionEnd":
		if frame != nil {
			if err := m.frames.Delete(frame.FrameID); err != nil {
				return nil, err
			}
		}
		return m.projectPane(req.TmuxPaneID)
	case "SubagentStart", "SubagentStop":
		if frame == nil {
			return m.projectPane(req.TmuxPaneID)
		}
		agentID, _ := result.Detail["agent_id"].(string)
		if agentID == "" {
			return m.projectPane(req.TmuxPaneID)
		}
		frame.Subagents = updateSubagents(frame.Subagents, req.EventName, agentID)
		frame.LastSeenAt = broadcastTs
		if _, err := m.frames.Upsert(*frame); err != nil {
			return nil, err
		}
		return m.projectPane(req.TmuxPaneID)
	}

	info, err := readProcessInfoFn(req.SenderPID)
	if err != nil {
		return nil, err
	}

	subagents := []string{}
	startedAt := broadcastTs
	parentFrameID := ""
	if frame != nil {
		subagents = append([]string(nil), frame.Subagents...)
		startedAt = frame.StartedAt
		parentFrameID = frame.ParentFrameID
	}
	if parentFrameID == "" {
		parent, err := m.frames.FindByPanePID(req.TmuxPaneID, info.PPID)
		if err != nil {
			return nil, err
		}
		if parent != nil && (frame == nil || parent.FrameID != frame.FrameID) {
			parentFrameID = parent.FrameID
		}
	}

	status := result.Status
	if status == "" && frame != nil {
		status = frame.Status
	}
	if status == "" {
		status = agentpkg.StatusIdle
	}

	_, err = m.frames.Upsert(store.Frame{
		FrameID:          frameID(frame),
		PaneID:           req.TmuxPaneID,
		AgentType:        req.AgentType,
		PID:              req.SenderPID,
		PPID:             info.PPID,
		ProcessStartTime: req.SenderStartTime,
		ParentFrameID:    parentFrameID,
		Subagents:        subagents,
		Status:           status,
		StartedAt:        startedAt,
		LastSeenAt:       broadcastTs,
		Verified:         true,
	})
	if err != nil {
		return nil, err
	}
	return m.projectPane(req.TmuxPaneID)
}

func (m *Module) projectPane(paneID string) (*SessionProjection, error) {
	if m.frames == nil {
		return nil, nil
	}
	frames, err := m.frames.ListByPane(paneID)
	if err != nil {
		return nil, err
	}
	projection := buildPaneProjection(paneID, frames)
	return &projection, nil
}

func frameID(frame *store.Frame) string {
	if frame == nil {
		return ""
	}
	return frame.FrameID
}

func updateSubagents(current []string, eventName, agentID string) []string {
	if current == nil {
		current = []string{}
	}
	switch eventName {
	case "SubagentStart":
		for _, existing := range current {
			if existing == agentID {
				return current
			}
		}
		return append(current, agentID)
	case "SubagentStop":
		filtered := make([]string, 0, len(current))
		for _, existing := range current {
			if existing != agentID {
				filtered = append(filtered, existing)
			}
		}
		return filtered
	default:
		return current
	}
}

func syncProjectionState(currentStatus map[string]agentpkg.Status, subagents map[string][]string, tmuxSession string, projection *SessionProjection) {
	if projection == nil || projection.TopFrame == nil {
		delete(currentStatus, tmuxSession)
		delete(subagents, tmuxSession)
		return
	}
	currentStatus[tmuxSession] = projection.TopFrame.Status
	subagents[tmuxSession] = append([]string(nil), projection.Subagents...)
}

func buildProjectionNormalized(projection *SessionProjection, fallbackAgentType, eventName string, broadcastTs int64, result agentpkg.DeriveResult) agentpkg.NormalizedEvent {
	normalized := agentpkg.NormalizedEvent{
		AgentType:    fallbackAgentType,
		Status:       string(result.Status),
		Model:        result.Model,
		Subagents:    []string{},
		RawEventName: eventName,
		BroadcastTs:  broadcastTs,
		Detail:       result.Detail,
	}
	if projection == nil {
		return normalized
	}
	normalized.Subagents = append([]string(nil), projection.Subagents...)
	if projection.TopFrame == nil {
		normalized.Status = string(agentpkg.StatusClear)
		return normalized
	}
	normalized.AgentType = projection.TopFrame.AgentType
	normalized.Status = string(projection.TopFrame.Status)
	return normalized
}

func (m *Module) projectionForSession(sessionName string) (*SessionProjection, error) {
	projections, err := m.liveFrameProjections()
	if err != nil {
		return nil, err
	}
	for i := range projections {
		name, _ := m.resolvePaneSession(projections[i].PaneID)
		if name == sessionName {
			projection := projections[i]
			return &projection, nil
		}
	}
	return nil, nil
}

func (m *Module) setProjectionTopStatus(sessionName string, status agentpkg.Status) (*SessionProjection, error) {
	projection, err := m.projectionForSession(sessionName)
	if err != nil || projection == nil || projection.TopFrame == nil {
		return projection, err
	}
	frame := *projection.TopFrame
	frame.Status = status
	if _, err := m.frames.Upsert(frame); err != nil {
		return nil, err
	}
	return m.projectPane(frame.PaneID)
}
