package teammod

import (
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// presenceLogEvery is how often the skip of one subject is logged: a registry file that cannot be verified delays every
// judgement that depends on it, and that must be visible without a line per second.
const presenceLogEvery = 10 * 60 * 1000 // ms

// presenceLogMax is the size at which the rate-limit map drops its expired entries.
const presenceLogMax = 256

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
	if len(m.presenceLogged) >= presenceLogMax { // entries past the window are no longer needed: drop them, the map stays bounded
		oldest, oldestAt := "", now+1
		for k, t := range m.presenceLogged {
			if now-t >= presenceLogEvery {
				delete(m.presenceLogged, k)
			} else if t < oldestAt {
				oldest, oldestAt = k, t
			}
		}
		if len(m.presenceLogged) >= presenceLogMax && oldest != "" { // none had expired: the oldest makes room
			delete(m.presenceLogged, oldest)
		}
	}
	m.presenceLogged[subject] = now
	m.presenceLogMu.Unlock()
	m.logf("[team] presence unknown for session %s: %s is left for the next round (the registry could not be read, or a live process has a registry file that could not be verified)", sid, subject)
}

// approvalSessionGone is sessionGone for a swept approval, with one bound: a row that has neither a lease nor a deadline (the
// settings hook's terminal_only card) has no clock but the registry, so an Unknown session holds it only for twice the stall
// since it opened — a registry file that never verifies must not keep a card on a finished dialog for ever. Every other row has a
// lease or a deadline that closes it anyway, so Unknown simply waits for that.
func (m *Module) approvalSessionGone(a team.Approval) bool {
	switch m.origins.SessionPresence(a.Origin.SessionID) {
	case peersmod.PresenceGone:
		return true
	case peersmod.PresenceUnknown:
		if a.LeaseUntil >= team.NoExpiryAt && a.DeadlineAt >= team.NoExpiryAt && m.now() >= a.CreatedAt+2*int64(team.RelayStallTimeoutS)*1000 {
			return true
		}
		m.logPresenceUnknown("approval "+a.ID, a.Origin.SessionID)
	}
	return false
}
