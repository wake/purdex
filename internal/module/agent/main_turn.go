package agent

import "github.com/wake/purdex/internal/lights"

// MainTurnRunning says whether the session's main conversation is in a turn right now, from the Purdex mod's stream
// for the session: running is its TurnID being set; ok is false when no live stream reports the session (none, ended,
// or silent for longer than lights.LiveWindow), in which case nothing is known. A caller that can use the answer only
// to decide when to try (the lease notice outbox: a passed position is offered again when its session can take a
// notice) treats an unknown session as one it can try.
func (m *Module) MainTurnRunning(sessionID string) (running, ok bool) {
	if m == nil || sessionID == "" {
		return false, false
	}
	m.modMu.Lock()
	defer m.modMu.Unlock()
	st := m.modStreams[m.modBySID[sessionID]]
	if st == nil || st.Ended || st.SID != sessionID || m.modClock().Sub(st.LastEvent) > lights.LiveWindow {
		return false, false
	}
	return st.TurnID != "", true
}
