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
// (source hook) until the mod reports something that moves the light and
// happened after it
// (lights.StreamState.StatusEventAt) or hookEdgeTTL passes. Every other hook
// stays below the mod: a Notification or a PermissionRequest would put a
// waiting back after the mod has seen the approval (spec §12.1 item 16), and a
// detail-only hook says nothing about the turn.
//
// Time basis. The edge's time is when the hook reached the daemon; the mod's
// is when its event happened (the event's own at, the mod's Date.now(), clamped
// to its arrival), not when it arrived. Mod events reach the daemon about 1.2 s
// late and up to about 3 s (alpha.609), so a turn.start that happened before a
// Stop is received after it, and the receive order would call that "the mod
// caught up" and reveal a running that is already over. The mod reaches the
// daemon only through a Unix socket on the same host, so both stamps come off
// one wall clock and can be compared. Inside one stream events stay ordered by
// seq alone.

// hookEdgeTTL bounds an edge whose turn the mod never confirms (a slash
// command submits a prompt and starts no turn): past it the pane goes back to
// the mod's light. Mod events arrive up to about 3 s late, so it is 5 s; a mod
// that is later than that shows its old light until its events land (or its
// stream goes stale).
const hookEdgeTTL = 5 * time.Second

// hookEdge is one frame's last turn-boundary hook: the status it set, when the
// daemon applied it, and the conversation it belongs to.
type hookEdge struct {
	status agentpkg.Status
	at     time.Time
	sid    string
}

// atMs is the edge's time in whole milliseconds, the precision of the mod's
// event time (lights.StreamState.StatusEventAt). Every comparison of the two
// uses it, so an event stamped with the millisecond the hook arrived in counts
// as at or after the hook: a tie goes to the mod.
func (e hookEdge) atMs() time.Time { return e.at.Truncate(time.Millisecond) }

// markAt is a stream's StatusEventAt as the edge's readers see it at now. A mark
// later than the clock was left by a wall clock that has since gone back (the
// next mod event starts it over, lights.StreamState.Apply); until then it counts
// as zero, so it cannot hold an edge down.
func markAt(statusEventAt, now time.Time) time.Time {
	if statusEventAt.After(now) {
		return time.Time{}
	}
	return statusEventAt
}

// setHookEdge records frameID's edge unless a newer one is already there
// (hooks of one process are applied out of order now and then), the frame's
// last SessionStart arrived at or after the hook (hookEdgeClearedAt: a Stop of
// the old conversation noted after the new one began must not leave an edge in
// it), or the mod has already reported a light event that happened at or after
// the hook's arrival: it caught up before the edge could be noted, and the event that did
// it has already marked the pane dirty.
func (m *Module) setHookEdge(frameID string, e hookEdge) {
	if frameID == "" {
		return
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	if cleared, ok := m.hookEdgeClearedAt[frameID]; ok && !e.at.After(cleared) {
		return
	}
	if cur, ok := m.hookEdge[frameID]; ok && cur.at.After(e.at) {
		return
	}
	if st := m.modStreams[m.modBySID[e.sid]]; st != nil && st.SID == e.sid && !markAt(st.StatusEventAt, m.modClock()).Before(e.atMs()) {
		return
	}
	if m.hookEdge == nil {
		m.hookEdge = make(map[string]hookEdge)
	}
	m.hookEdge[frameID] = e
}

// clearHookEdge is a SessionStart that arrived at recv: drop frameID's edge and
// remember the arrival so an older hook cannot set one again. The stamp only
// moves forward.
func (m *Module) clearHookEdge(frameID string, recv time.Time) {
	if frameID == "" {
		return
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	delete(m.hookEdge, frameID)
	if m.hookEdgeClearedAt == nil {
		m.hookEdgeClearedAt = make(map[string]time.Time)
	}
	if recv.After(m.hookEdgeClearedAt[frameID]) {
		m.hookEdgeClearedAt[frameID] = recv
	}
}

// forgetHookEdge drops everything kept for frameID — the edge and the
// SessionStart stamp — because the frame is gone (a no-op without any).
func (m *Module) forgetHookEdge(frameID string) {
	if frameID == "" {
		return
	}
	m.modMu.Lock()
	delete(m.hookEdge, frameID)
	delete(m.hookEdgeClearedAt, frameID)
	m.modMu.Unlock()
}

// noteHookEdge keeps the edge in step with a cc hook that was just applied to
// the sender's frame: a root frame's UserPromptSubmit that made it running or
// Stop that made it idle records one, a SessionStart drops it, anything else
// does nothing. It reads the sender's own frame, as noteHookBackground does,
// and holds no lock; takes modMu only inside the setters. Call it after
// applyFrameEvent and before the projection the event emits is built. recv is
// when the daemon received the hook, read before anything was applied; it is
// the edge's time.
func (m *Module) noteHookEdge(req EventRequest, lifecycle agentpkg.LifecycleEventKind, result agentpkg.DeriveResult, meta FrameTraceMeta, recv time.Time) {
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
		m.clearHookEdge(frame.FrameID, recv)
		return
	}
	// The hook must have written its status to the frame it was applied to:
	// a skipped event, or a frame a newer write has since moved on, has no
	// edge to show.
	if meta.Decision != "updated_frame" || frame.Status != want || frame.SessionID == "" {
		return
	}
	m.setHookEdge(frame.FrameID, hookEdge{status: want, at: recv, sid: frame.SessionID})
}

// wins reports whether e still decides the light of a pane showing a live
// stream at now: it belongs to the conversation sid, it arrived after the last
// event that moved the stream's light happened (statusEventAt; a tie in the
// same millisecond goes to the mod, see atMs), and it has not run out.
func (e hookEdge) wins(sid string, statusEventAt, now time.Time) bool {
	return e.sid == sid && e.atMs().After(markAt(statusEventAt, now)) && now.Sub(e.at) < hookEdgeTTL
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

// edgeSupersededLocked reports whether sid has a live edge that the mod event
// just applied takes over: the event moved the stream's StatusEventAt (from
// prev to cur) and it happened at or after the hook arrived (the opposite of
// hookEdge.wins). An event that happened before the hook, however late it
// arrived, leaves the edge in charge. The light's status may be the same, but
// its source changes from hook to mod and the SPA is told. modMu must be held.
func (m *Module) edgeSupersededLocked(sid string, prev, cur, now time.Time) bool {
	if !cur.After(markAt(prev, now)) {
		return false
	}
	for _, e := range m.hookEdge {
		if e.sid == sid && !cur.Before(e.atMs()) && now.Sub(e.at) < hookEdgeTTL {
			return true
		}
	}
	return false
}
