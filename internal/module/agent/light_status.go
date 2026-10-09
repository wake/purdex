package agent

// LightStatus is the light (running | waiting | idle | error) of the newest live Claude Code root frame that reports
// sessionID. It reads the frames store and checks the recorded pid with a signal-0 probe: no tmux listing, no process
// walk, so a caller may ask it every few hundred milliseconds (the conversation stream's header). It is not a proof
// of ownership like ConfirmedOwners: the caller must already know the session is live and use this only to keep the
// light current between its full lookups. ok is false when no live frame reports the session.
func (m *Module) LightStatus(sessionID string) (status string, ok bool) {
	if m == nil || m.frames == nil || sessionID == "" {
		return "", false
	}
	frames, err := m.frames.ListRootsBySessionID(sessionID)
	if err != nil {
		return "", false
	}
	var best string
	var bestSeen int64
	var bestID string
	for _, f := range frames {
		if f.AgentType != "cc" || !isPidAliveFn(f.PID) {
			continue
		}
		if !ok || f.LastSeenAt > bestSeen || (f.LastSeenAt == bestSeen && f.FrameID < bestID) {
			best, bestSeen, bestID, ok = string(f.Status), f.LastSeenAt, f.FrameID, true
		}
	}
	return best, ok
}
