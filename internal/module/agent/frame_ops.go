package agent

import (
	"fmt"
	"sort"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

// proxyMaxDepth caps the PPID ancestor walk during proxy subagent detection.
// 5 is enough to cover observed layouts like codex → codex-companion → cc
// (2 hops) with 3 hops of buffer for shell/tmux wrappers; beyond that we
// fall back to creating a new frame rather than paying unbounded syscall
// cost on a genuinely deep chain.
const proxyMaxDepth = 5

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
		// frame == nil: sender has no frame of its own. This is either a
		// genuine orphan SessionEnd, or the SessionEnd of a process that was
		// previously proxy-attached to another frame (Phase 2 PR-2b §1.5).
		// Probe the pane's frames for a matching proxy ref and detach it.
		removed, parentFrame, parentBefore, parentAfter, err := m.removeProxyRefForSender(req.TmuxPaneID, req.SenderPID, req.SenderStartTime, broadcastTs)
		if err != nil {
			return nil, FrameTraceMeta{}, err
		}
		if removed {
			projection, perr := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				FrameID:       parentFrame.FrameID,
				ParentFrameID: parentFrame.ParentFrameID,
				Decision:      "updated_frame",
				Reason:        "proxy_subagent_detached",
				Before:        parentBefore,
				After:         parentAfter,
			}, perr
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

	// Proxy subagent fast-path (Phase 2 PR-2b, plan §1.4): when a SessionStart
	// arrives from a sender that has no existing frame of its own and a PPID
	// ancestor walk locates an alive cross-type parent in the same pane, we
	// collapse the event into a proxy ref attached to that parent rather than
	// creating a standalone frame. Observed in practice for codex spawned from
	// inside a cc session via codex-companion: cc owns the UX, codex should
	// show as a dot on cc's tab, not as a separate lit-up frame.
	if req.EventName == "SessionStart" && frame == nil {
		parent, perr := m.findProxyParent(req)
		if perr != nil {
			return nil, FrameTraceMeta{}, perr
		}
		if parent != nil {
			parentBefore := summarizeFrame(parent)
			ref := agentpkg.SubagentRef{
				ID:              fmt.Sprintf("proxy:%s:%d:%s", req.AgentType, req.SenderPID, req.SenderStartTime),
				Type:            req.AgentType,
				StartedAt:       broadcastTs,
				SourcePID:       req.SenderPID,
				SourceStartTime: req.SenderStartTime,
				IsProxy:         true,
			}
			parent.Subagents = updateSubagents(parent.Subagents, "SubagentStart", ref)
			parent.LastSeenAt = broadcastTs
			stored, err := m.frames.Upsert(*parent)
			if err != nil {
				return nil, FrameTraceMeta{}, err
			}
			projection, err := m.projectPane(req.TmuxPaneID)
			return projection, FrameTraceMeta{
				FrameID:       stored.FrameID,
				ParentFrameID: stored.ParentFrameID,
				Decision:      "updated_frame",
				Reason:        "proxy_subagent_attached",
				Before:        parentBefore,
				After:         summarizeFrame(&stored),
			}, err
		}
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
// SubagentStart / SubagentStop event. On SubagentStart the first-write wins;
// an existing ref keeps its StartedAt/SourcePID/SourceStartTime/IsProxy.
//
// Identity key is kind-aware (R2 fix): proxy refs identify by
// (SourcePID, SourceStartTime) — the sender process — while native refs
// identify by ID (the agent_id string supplied by the provider). Cross-kind
// refs (one proxy, one native) never match even if ID strings coincide.
// This isolates namespaces so a native ref whose agent_id happens to collide
// with a synthesized proxy ID cannot shadow or evict a proxy ref, and vice
// versa.
func updateSubagents(current []agentpkg.SubagentRef, eventName string, ref agentpkg.SubagentRef) []agentpkg.SubagentRef {
	if current == nil {
		current = []agentpkg.SubagentRef{}
	}
	switch eventName {
	case "SubagentStart":
		for _, existing := range current {
			if subagentRefMatches(existing, ref) {
				return current
			}
		}
		return append(current, ref)
	case "SubagentStop":
		filtered := make([]agentpkg.SubagentRef, 0, len(current))
		for _, existing := range current {
			if !subagentRefMatches(existing, ref) {
				filtered = append(filtered, existing)
			}
		}
		return filtered
	default:
		return current
	}
}

// subagentRefMatches returns true when two refs identify the same subagent.
// Proxy refs compare by (SourcePID, SourceStartTime); native refs compare by
// ID. Cross-kind (one proxy, one native) is never a match — preserves the
// isolation between the two namespaces (see updateSubagents doc).
func subagentRefMatches(a, b agentpkg.SubagentRef) bool {
	if a.IsProxy != b.IsProxy {
		return false
	}
	if a.IsProxy {
		return a.SourcePID == b.SourcePID && a.SourceStartTime == b.SourceStartTime
	}
	return a.ID == b.ID
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
	// Refresh LastSeenAt so probe-driven status transitions count as "recent
	// activity" for the idle sweep rule (sweep.go frameIdleThreshold). Without
	// this bump, a live agent at a shell prompt that emits no hooks for 1h
	// would be mis-classified as idle and have its frame silently deleted.
	frame.LastSeenAt = time.Now().UnixNano()
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

// removeProxyRefForSender scans the pane's frames for a proxy SubagentRef
// whose SourcePID+SourceStartTime match the sender that is emitting SessionEnd,
// filters it out, refreshes the owning frame's LastSeenAt and persists via
// Upsert. Matching is by identity fields (SourcePID+SourceStartTime), not by
// ref.ID string, so the detach remains robust across potential future ID
// format changes.
//
// Returns (removed, ownerFrameAfter, ownerBefore, ownerAfter, err):
//   - removed=true with ownerFrameAfter populated when a ref was detached.
//   - removed=false, zeroFrame, nil, nil, nil when nothing matched.
func (m *Module) removeProxyRefForSender(paneID string, senderPID int, senderStartTime string, broadcastTs int64) (bool, store.Frame, any, any, error) {
	if m.frames == nil {
		return false, store.Frame{}, nil, nil, nil
	}
	frames, err := m.frames.ListByPane(paneID)
	if err != nil {
		return false, store.Frame{}, nil, nil, err
	}
	for _, frame := range frames {
		hit := -1
		for i, ref := range frame.Subagents {
			if ref.SourcePID == senderPID && ref.SourceStartTime == senderStartTime {
				hit = i
				break
			}
		}
		if hit < 0 {
			continue
		}
		before := summarizeFrame(&frame)
		filtered := make([]agentpkg.SubagentRef, 0, len(frame.Subagents)-1)
		filtered = append(filtered, frame.Subagents[:hit]...)
		filtered = append(filtered, frame.Subagents[hit+1:]...)
		frame.Subagents = filtered
		frame.LastSeenAt = broadcastTs
		stored, err := m.frames.Upsert(frame)
		if err != nil {
			return false, store.Frame{}, nil, nil, err
		}
		return true, stored, before, summarizeFrame(&stored), nil
	}
	return false, store.Frame{}, nil, nil, nil
}

// findProxyParent walks the sender's PPID ancestor chain (capped at
// proxyMaxDepth) looking for an alive, identity-verified, cross-type frame in
// the same pane. See plan §1.4 for full contract.
//
// Returns (parent, nil) when a proxy candidate is found; (nil, nil) when the
// walk should not proxy-attach (no ancestor has a frame / same-type hard
// stop / all cross-type candidates stale or dead / depth exceeded / proc info
// or start_time read errors that make identity unverifiable). Non-nil error
// is returned only when the frames store fails.
func (m *Module) findProxyParent(req EventRequest) (*store.Frame, error) {
	if m.frames == nil {
		return nil, nil
	}
	info, err := readProcessInfoFn(req.SenderPID)
	if err != nil {
		return nil, nil
	}
	ppid := info.PPID
	for depth := 0; depth < proxyMaxDepth; depth++ {
		if ppid <= 1 {
			return nil, nil
		}
		candidate, err := m.frames.FindByPanePID(req.TmuxPaneID, ppid)
		if err != nil {
			return nil, err
		}
		if candidate != nil {
			// Same-type ancestor: pane already has a frame of our agent_type,
			// meaning this SessionStart is a re-session / update of that frame
			// — not a cross-type proxy. Hard-stop the walk (don't continue to
			// some cross-type grandparent that would be a semantic mismatch).
			if candidate.AgentType == req.AgentType {
				return nil, nil
			}
			if isPidAliveFn(candidate.PID) {
				actualStart, serr := processStartTimeFn(candidate.PID)
				if serr != nil {
					// v5 rule: identity unverifiable → abort walk (consistent
					// with verify.go's "lookup error → don't infer" convention).
					// Prevents mis-attaching to an outer cross-type ancestor
					// when the immediate candidate's start_time is transiently
					// unreadable.
					return nil, nil
				}
				if actualStart == candidate.ProcessStartTime {
					return candidate, nil
				}
				// Identity mismatch (PID reused) → stale frame; continue walk
				// to look for a real parent further up.
			}
			// Dead candidate: also continue walk; sweep will clear it.
		}
		// No frame at this PID — walk one more level up.
		ancestorInfo, err := readProcessInfoFn(ppid)
		if err != nil {
			return nil, nil
		}
		if ancestorInfo.PPID == ppid {
			return nil, nil
		}
		ppid = ancestorInfo.PPID
	}
	return nil, nil
}
