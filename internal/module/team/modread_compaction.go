package teammod

import (
	"time"

	"github.com/wake/purdex/internal/team"
)

// modReader is the mod socket's team read: the store's role answer plus the session's effective compaction settings.
type modReader struct{ m *Module }

func (r modReader) ModTeamRead(sessionID string) (team.ModRead, error) {
	got, err := r.m.store.ModTeamRead(sessionID)
	if err != nil {
		return got, err
	}
	v, err := r.m.compactionView(sessionID)
	if err != nil {
		r.m.logModCompactionErr(err)
		return got, nil // no compaction: the mod keeps its last value
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
