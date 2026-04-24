package agent

import (
	"sort"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

type FrameTraceMeta struct {
	FrameID       string
	ParentFrameID string
	Decision      string
	Reason        string
	Before        any
	After         any
}

func (m *Module) applyFrameEvent(req EventRequest, result agentpkg.DeriveResult, broadcastTs int64) (*SessionProjection, FrameTraceMeta, error) {
	if m.frames == nil {
		return nil, FrameTraceMeta{Decision: "skipped", Reason: "frame_store_unavailable", Before: map[string]any{}, After: map[string]any{}}, nil
	}
	if req.EventName != "SubagentStart" && req.EventName != "SubagentStop" && !result.Valid {
		projection, err := m.projectPane(req.TmuxPaneID)
		return projection, FrameTraceMeta{Decision: "skipped", Reason: "derive_invalid", Before: map[string]any{}, After: map[string]any{}}, err
	}

	frame, err := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
	if err != nil {
		return nil, FrameTraceMeta{}, err
	}
	before := summarizeFrame(frame)

	switch req.EventName {
	case "SessionEnd":
		if frame != nil {
			if err := m.frames.Delete(frame.FrameID); err != nil {
				return nil, FrameTraceMeta{}, err
			}
			projection, err := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				FrameID:       frame.FrameID,
				ParentFrameID: frame.ParentFrameID,
				Decision:      "deleted_frame",
				Reason:        "session_end",
				Before:        before,
				After:         map[string]any{},
			}, err
		}
		projection, err := m.projectPane(req.TmuxPaneID)
		return projection, FrameTraceMeta{
			Decision: "skipped",
			Reason:   "session_end_without_frame",
			Before:   before,
			After:    map[string]any{},
		}, err
	case "SubagentStart", "SubagentStop":
		if frame == nil {
			projection, err := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				Decision: "skipped",
				Reason:   "frame_missing",
				Before:   before,
				After:    map[string]any{},
			}, err
		}
		agentID, _ := result.Detail["agent_id"].(string)
		if agentID == "" {
			projection, err := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				FrameID:       frame.FrameID,
				ParentFrameID: frame.ParentFrameID,
				Decision:      "skipped",
				Reason:        "subagent_id_missing",
				Before:        before,
				After:         before,
			}, err
		}
		ref := agentpkg.SubagentRef{
			ID: agentID,
			// Type is the canonical agent family that owns this subagent (cc /
			// codex / opencode), not the payload's per-subagent sub-variant
			// (e.g. opencode's `agent_type: "Explore"`). Keeping Type aligned
			// to frame.AgentType lets the SPA use it for agent-family color
			// lookup without provider-specific special cases.
			Type:      frame.AgentType,
			StartedAt: broadcastTs,
			// SourcePID / SourceStartTime / IsProxy left zero: native SubagentStart
			// refs have no distinct source process identity. Proxy attaches (PR-2b)
			// set these explicitly.
		}
		frame.Subagents = updateSubagents(frame.Subagents, req.EventName, ref)
		frame.LastSeenAt = broadcastTs
		stored, err := m.frames.Upsert(*frame)
		if err != nil {
			return nil, FrameTraceMeta{}, err
		}
		projection, err := m.projectPane(req.TmuxPaneID)
		return projection, FrameTraceMeta{
			FrameID:       stored.FrameID,
			ParentFrameID: stored.ParentFrameID,
			Decision:      "updated_frame",
			Reason:        "subagent_membership_changed",
			Before:        before,
			After:         summarizeFrame(&stored),
		}, err
	}

	info, err := readProcessInfoFn(req.SenderPID)
	if err != nil {
		return nil, FrameTraceMeta{}, err
	}

	subagents := []agentpkg.SubagentRef{}
	startedAt := broadcastTs
	parentFrameID := ""
	if frame != nil {
		subagents = append([]agentpkg.SubagentRef(nil), frame.Subagents...)
		startedAt = frame.StartedAt
		parentFrameID = frame.ParentFrameID
	}
	if req.EventName == "SessionStart" {
		subagents = []agentpkg.SubagentRef{}
	}
	if parentFrameID == "" {
		parent, err := m.frames.FindByPanePID(req.TmuxPaneID, info.PPID)
		if err != nil {
			return nil, FrameTraceMeta{}, err
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

	stored, err := m.frames.Upsert(store.Frame{
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
		return nil, FrameTraceMeta{}, err
	}
	projection, err := m.projectPane(req.TmuxPaneID)
	reason := "parent_frame_missing"
	if stored.ParentFrameID != "" {
		reason = "parent_frame_found"
	}
	decision := "created_frame"
	if frame != nil {
		decision = "updated_frame"
	}
	return projection, FrameTraceMeta{
		FrameID:       stored.FrameID,
		ParentFrameID: stored.ParentFrameID,
		Decision:      decision,
		Reason:        reason,
		Before:        before,
		After:         summarizeFrame(&stored),
	}, err
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

func summarizeFrame(frame *store.Frame) map[string]any {
	if frame == nil {
		return map[string]any{}
	}
	return map[string]any{
		"frame_id":         frame.FrameID,
		"pane_id":          frame.PaneID,
		"agent_type":       frame.AgentType,
		"pid":              frame.PID,
		"ppid":             frame.PPID,
		"parent_frame_id":  frame.ParentFrameID,
		"process_start_at": frame.ProcessStartTime,
		"status":           string(frame.Status),
		"subagents":        append([]agentpkg.SubagentRef(nil), frame.Subagents...),
	}
}

// updateSubagents mutates a frame's subagent list in response to a
// SubagentStart / SubagentStop event. Matching is by ref.ID only; Type does
// not participate so a cross-type hook (proxy path in PR-2b) cleanly replaces
// a native ref on stop. On SubagentStart the first-write wins — an existing
// ref keeps its StartedAt/SourcePID/SourceStartTime/IsProxy.
func updateSubagents(current []agentpkg.SubagentRef, eventName string, ref agentpkg.SubagentRef) []agentpkg.SubagentRef {
	if current == nil {
		current = []agentpkg.SubagentRef{}
	}
	switch eventName {
	case "SubagentStart":
		for _, existing := range current {
			if existing.ID == ref.ID {
				return current
			}
		}
		return append(current, ref)
	case "SubagentStop":
		filtered := make([]agentpkg.SubagentRef, 0, len(current))
		for _, existing := range current {
			if existing.ID != ref.ID {
				filtered = append(filtered, existing)
			}
		}
		return filtered
	default:
		return current
	}
}

func syncProjectionState(currentStatus map[string]agentpkg.Status, subagents map[string][]agentpkg.SubagentRef, tmuxSession string, projection *SessionProjection) {
	if projection == nil || projection.TopFrame == nil {
		delete(currentStatus, tmuxSession)
		delete(subagents, tmuxSession)
		return
	}
	currentStatus[tmuxSession] = projection.TopFrame.Status
	subagents[tmuxSession] = append([]agentpkg.SubagentRef(nil), projection.Subagents...)
}

func buildProjectionNormalized(projection *SessionProjection, fallbackAgentType, eventName string, broadcastTs int64, result agentpkg.DeriveResult) agentpkg.NormalizedEvent {
	normalized := agentpkg.NormalizedEvent{
		AgentType:    fallbackAgentType,
		Status:       string(result.Status),
		Model:        result.Model,
		Subagents:    []agentpkg.SubagentRef{},
		RawEventName: eventName,
		BroadcastTs:  broadcastTs,
		Detail:       result.Detail,
	}
	if projection == nil {
		return normalized
	}
	normalized.Subagents = append([]agentpkg.SubagentRef(nil), projection.Subagents...)
	if projection.TopFrame == nil {
		normalized.Status = string(agentpkg.StatusClear)
		return normalized
	}
	normalized.AgentType = projection.TopFrame.AgentType
	normalized.Status = string(projection.TopFrame.Status)
	return normalized
}

// strFromDetail looks up a string field in a DeriveResult.Detail map.
// Returns "" when the key is missing or the value is not a string.
func strFromDetail(detail map[string]any, key string) string {
	if detail == nil {
		return ""
	}
	if v, ok := detail[key].(string); ok {
		return v
	}
	return ""
}

func (m *Module) projectionForSession(sessionName string) (*SessionProjection, error) {
	projections, err := m.liveFrameProjections()
	if err != nil {
		return nil, err
	}
	return m.selectSessionProjection(sessionName, projections), nil
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
	return m.projectionForSession(sessionName)
}

func (m *Module) selectSessionProjection(sessionName string, projections []SessionProjection) *SessionProjection {
	var selected *SessionProjection
	for i := range projections {
		name, _ := m.resolvePaneSession(projections[i].PaneID)
		if name != sessionName {
			continue
		}
		if selected == nil || projectionSortGreater(projections[i], *selected) {
			projection := projections[i]
			selected = &projection
		}
	}
	return selected
}

type namedProjection struct {
	SessionName string
	SessionCode string
	Projection  SessionProjection
}

func (m *Module) liveSessionProjections() ([]namedProjection, error) {
	projections, err := m.liveFrameProjections()
	if err != nil {
		return nil, err
	}
	if len(projections) == 0 {
		return nil, nil
	}
	selected := make(map[string]namedProjection)
	for _, projection := range projections {
		sessionName, sessionCode := m.resolvePaneSession(projection.PaneID)
		if sessionName == "" {
			continue
		}
		current, ok := selected[sessionName]
		if !ok || projectionSortGreater(projection, current.Projection) {
			selected[sessionName] = namedProjection{
				SessionName: sessionName,
				SessionCode: sessionCode,
				Projection:  projection,
			}
		}
	}
	sessionNames := make([]string, 0, len(selected))
	for sessionName := range selected {
		sessionNames = append(sessionNames, sessionName)
	}
	sort.Strings(sessionNames)
	out := make([]namedProjection, 0, len(sessionNames))
	for _, sessionName := range sessionNames {
		out = append(out, selected[sessionName])
	}
	return out, nil
}

func projectionSortGreater(candidate, current SessionProjection) bool {
	if candidate.TopFrame == nil {
		return false
	}
	if current.TopFrame == nil {
		return true
	}
	if candidate.TopFrame.StartedAt != current.TopFrame.StartedAt {
		return candidate.TopFrame.StartedAt > current.TopFrame.StartedAt
	}
	return candidate.TopFrame.FrameID > current.TopFrame.FrameID
}
