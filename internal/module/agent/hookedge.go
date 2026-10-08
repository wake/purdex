package agent

import (
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// Hook turn edges (U1-2 fix, spec §12.1). The mod reports a turn through a
// 150 ms batch and an event delay, so a hook beats it by about a second at
// each turn boundary: a UserPromptSubmit lands while the mod still says idle
// (the pane would flash an idle frame, which the SPA marks unread, at the top
// of every turn) and a Stop lands while it still says running.
//
// Newer-hook-wins is allowed only at those two edges: a root cc frame's
// UserPromptSubmit that made it running, and its Stop that made it idle. Such
// a hook is remembered as an edge, and applyModOverlay shows its status
// (source hook) until the mod reports something that moves the light after it
// (lights.StreamState.StatusEventAt) or hookEdgeTTL passes. Every other hook
// stays below the mod: a Notification or a PermissionRequest would put a
// waiting back after the mod has seen the approval (spec §12.1 item 16), and a
// detail-only hook says nothing about the turn.

// hookEdgeTTL bounds an edge whose turn the mod never confirms (a slash
// command submits a prompt and starts no turn): past it the pane goes back to
// the mod's light.
const hookEdgeTTL = 3 * time.Second

// hookEdge is one frame's last turn-boundary hook: the status it set, when the
// daemon applied it, and the conversation it belongs to.
type hookEdge struct {
	status agentpkg.Status
	at     time.Time
	sid    string
}

// setHookEdge records frameID's edge unless a newer one is already there
// (hooks of one process are applied out of order now and then).
func (m *Module) setHookEdge(frameID string, e hookEdge) {
	if frameID == "" {
		return
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	if cur, ok := m.hookEdge[frameID]; ok && cur.at.After(e.at) {
		return
	}
	if m.hookEdge == nil {
		m.hookEdge = make(map[string]hookEdge)
	}
	m.hookEdge[frameID] = e
}

// forgetHookEdge drops frameID's edge: the frame is gone, or a SessionStart
// began a new conversation (a no-op without one).
func (m *Module) forgetHookEdge(frameID string) {
	if frameID == "" {
		return
	}
	m.modMu.Lock()
	delete(m.hookEdge, frameID)
	m.modMu.Unlock()
}

// noteHookEdge keeps the edge in step with a cc hook that was just applied to
// the sender's frame: a root frame's UserPromptSubmit that made it running or
// Stop that made it idle records one, a SessionStart drops it, anything else
// does nothing. It reads the sender's own frame, as noteHookBackground does,
// and holds no lock; takes modMu only inside the setters. Call it after
// applyFrameEvent and before the projection the event emits is built.
func (m *Module) noteHookEdge(req EventRequest, lifecycle agentpkg.LifecycleEventKind, result agentpkg.DeriveResult, meta FrameTraceMeta) {
	if req.AgentType != "cc" || m.frames == nil {
		return
	}
	var want agentpkg.Status
	switch {
	case lifecycle == agentpkg.LifecycleSessionStart:
		// a new conversation: want stays empty, the edge is dropped below
	case lifecycle == agentpkg.LifecycleUserPromptSubmit && result.Status == agentpkg.StatusRunning:
		want = agentpkg.StatusRunning
	case lifecycle == agentpkg.LifecycleStop && result.Status == agentpkg.StatusIdle:
		want = agentpkg.StatusIdle
	default:
		return
	}
	frame, err := m.frames.GetByIdentity(req.TmuxPaneID, req.SenderPID, req.SenderStartTime)
	if err != nil || frame == nil || frame.ParentFrameID != "" {
		return
	}
	if want == "" {
		m.forgetHookEdge(frame.FrameID)
		return
	}
	// The hook must have written its status to the frame it was applied to:
	// a skipped event, or a frame a newer write has since moved on, has no
	// edge to show.
	if meta.Decision != "updated_frame" || frame.Status != want || frame.SessionID == "" {
		return
	}
	m.setHookEdge(frame.FrameID, hookEdge{status: want, at: m.modClock(), sid: frame.SessionID})
}

// wins reports whether e still decides the light of a pane showing a live
// stream at now: it belongs to the conversation sid, it is newer than the last
// event that moved the stream's light (statusEventAt), and it has not run out.
func (e hookEdge) wins(sid string, statusEventAt, now time.Time) bool {
	return e.sid == sid && e.at.After(statusEventAt) && now.Sub(e.at) < hookEdgeTTL
}

// expireHookEdgesLocked drops the edges that ran out by now and marks their
// conversations dirty: the light a pane shows changes when its edge expires,
// and nothing else tells the worker. modMu must be held.
func (m *Module) expireHookEdgesLocked(now time.Time, dirty map[string]string) {
	for id, e := range m.hookEdge {
		if now.Sub(e.at) < hookEdgeTTL {
			continue
		}
		delete(m.hookEdge, id)
		if _, ok := dirty[e.sid]; !ok {
			dirty[e.sid] = modEventEdgeExpired
		}
	}
}

// edgeSupersededLocked reports whether sid has an edge that the mod event just
// applied at now (st.StatusEventAt) takes over. The light's status may be the
// same, but its source changes from hook to mod and the SPA is told. modMu
// must be held.
func (m *Module) edgeSupersededLocked(sid string, statusEventAt, now time.Time) bool {
	if !statusEventAt.Equal(now) {
		return false
	}
	for _, e := range m.hookEdge {
		if e.sid == sid && e.at.Before(now) && now.Sub(e.at) < hookEdgeTTL {
			return true
		}
	}
	return false
}
