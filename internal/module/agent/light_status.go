package agent

import "time"

// LightStatus is the light (running | waiting | idle | error) of one frame: frameID, a live Claude Code root frame
// that reports sessionID (the frame ConfirmedOwners confirmed, so no other frame can stand in for it). It reads the frames store and checks the recorded pid with a signal-0 probe: no tmux listing, no process
// walk, so a caller may ask it every few hundred milliseconds (the conversation stream's header). It is not a proof
// of ownership like ConfirmedOwners: the caller must already know the session is live and use this only to keep the
// light current between its full lookups. The pid probe does not compare process start times (that is one `ps` per
// call, the cost this lookup exists to avoid): a pid reused after the agent exited can keep a stale light for at most
// the caller's full-lookup interval, after which ConfirmedOwners (which does compare them) says nobody.
// ok is false when no live frame reports the session.
func (m *Module) LightStatus(sessionID, frameID string) (status string, ok bool) {
	if m == nil || m.frames == nil || sessionID == "" || frameID == "" {
		return "", false
	}
	frames, err := m.frames.ListRootsBySessionID(sessionID)
	if err != nil {
		return "", false
	}
	for _, f := range frames {
		if f.FrameID == frameID && f.AgentType == "cc" && isPidAliveFn(f.PID) {
			return string(f.Status), true
		}
	}
	return "", false
}

// AbortedAt is when the Purdex mod last reported that it interrupted the main turn of sessionID's conversation
// (a turn.complete with aborted:true), as the daemon received it; ok is false when it has not since the session
// (re)started. `$.turn.abort` leaves no interruption marker in the transcript, so this is the only record of it.
func (m *Module) AbortedAt(sessionID string) (at time.Time, ok bool) {
	if m == nil || sessionID == "" {
		return time.Time{}, false
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	st := m.modStreams[m.modBySID[sessionID]]
	if st == nil || st.AbortedAt.IsZero() {
		return time.Time{}, false
	}
	return st.AbortedAt, true
}
