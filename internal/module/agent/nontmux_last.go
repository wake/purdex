package agent

import (
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// nonTmuxLastTTL is how long a non-tmux session may stay silent before the
// snapshot forgets it. A session outside tmux has no pane or process the
// daemon can watch, so a SessionEnd that never arrives (a killed terminal)
// would otherwise keep it listed for the life of the daemon.
const nonTmuxLastTTL = 2 * time.Hour

// nonTmuxEntry is the last frame the slot sent for one non-tmux code and when
// it was sent.
type nonTmuxEntry struct {
	event agentpkg.NormalizedEvent
	at    time.Time
}

func (m *Module) nonTmuxClock() time.Time {
	if m.nonTmuxNow != nil {
		return m.nonTmuxNow()
	}
	return time.Now()
}

// noteNonTmuxLocked records n, just broadcast under code, as the session's
// last frame; a clear frame forgets the code. Entries silent for nonTmuxLastTTL
// are dropped here, on the way (no goroutine sweeps the table). The caller is
// inside the emit slot (emit.mu); this takes no lock.
func (m *Module) noteNonTmuxLocked(code string, n agentpkg.NormalizedEvent) {
	now := m.nonTmuxClock()
	m.expireNonTmuxLocked(now)
	if n.Status == string(agentpkg.StatusClear) {
		delete(m.nonTmuxLast, code)
		return
	}
	if m.nonTmuxLast == nil {
		m.nonTmuxLast = make(map[string]nonTmuxEntry)
	}
	m.nonTmuxLast[code] = nonTmuxEntry{event: n, at: now}
}

// expireNonTmuxLocked drops the entries whose last frame is nonTmuxLastTTL or
// more before now. Under emit.mu.
func (m *Module) expireNonTmuxLocked(now time.Time) {
	for code, e := range m.nonTmuxLast {
		if now.Sub(e.at) >= nonTmuxLastTTL {
			delete(m.nonTmuxLast, code)
		}
	}
}
