package teammod

import (
	peersmod "github.com/wake/purdex/internal/module/peers"
)

// presenceLogEvery is how often the skip of one subject is logged: a registry file that cannot be verified delays every
// judgement that depends on it, and that must be visible without a line per second.
const presenceLogEvery = 10 * 60 * 1000 // ms

// sessionGone says whether sid is positively gone. Unknown is not: the caller leaves its act for the next round, and the subject
// (what it would have done, e.g. "approval <id>") is logged, rate-limited.
func (m *Module) sessionGone(subject, sid string) bool {
	switch m.origins.SessionPresence(sid) {
	case peersmod.PresenceGone:
		return true
	case peersmod.PresenceUnknown:
		m.logPresenceUnknown(subject, sid)
	}
	return false
}

// logPresenceUnknown logs that subject's judgement of session sid was skipped because its presence is unknown, at most once per
// presenceLogEvery per subject.
func (m *Module) logPresenceUnknown(subject, sid string) {
	now := m.now()
	m.presenceLogMu.Lock()
	last, seen := m.presenceLogged[subject]
	if seen && now-last < presenceLogEvery {
		m.presenceLogMu.Unlock()
		return
	}
	if m.presenceLogged == nil {
		m.presenceLogged = map[string]int64{}
	}
	m.presenceLogged[subject] = now
	m.presenceLogMu.Unlock()
	m.logf("[team] presence unknown for session %s: %s is left for the next round (the registry could not be read, or a live process has a registry file that could not be verified)", sid, subject)
}
