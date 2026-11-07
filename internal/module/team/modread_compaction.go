package teammod

import (
	"time"

	"github.com/wake/purdex/internal/team"
)

// modReader is the mod socket's team read: the store's role answer plus the session's effective compaction settings.
type modReader struct {
	m    *Module
	view func(sessionID string) (team.CompactionView, error) // nil: m.compactionView (tests inject a racing one)
}

func (r modReader) ModTeamRead(sessionID string) (team.ModRead, error) {
	got, err := r.m.store.ModTeamRead(sessionID)
	if err != nil {
		return got, err
	}
	// Strict: the mod is handed a policy only when every setting that decides it was read, once (spec §3); otherwise
	// the mod keeps its last value. (The App's compactionView stays lenient.)
	view := r.view
	if view == nil {
		view = func(sid string) (team.CompactionView, error) { return r.m.compactionViewWith(sid, true) }
	}
	v, err := view(sessionID)
	if err != nil {
		r.m.logModCompactionErr(err)
		return got, nil
	}
	if v.Role != got.Role { // the role was read twice and changed in between: the next poll is consistent
		return got, nil
	}
	got.Compaction = &v.Effective
	return got, nil
}

// logModCompactionErr logs the same error at most once per compactionLogEvery (the mod polls every 15 s).
func (m *Module) logModCompactionErr(err error) {
	msg, now := err.Error(), time.Now()
	m.compactionLog.mu.Lock()
	quiet := msg == m.compactionLog.modMsg && now.Sub(m.compactionLog.modAt) < compactionLogEvery
	if !quiet {
		m.compactionLog.modMsg, m.compactionLog.modAt = msg, now
	}
	m.compactionLog.mu.Unlock()
	if !quiet {
		m.logf("[team] could not fill compaction for the mod read: %v", err)
	}
}
