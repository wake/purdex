package resourcesmod

import (
	"time"

	"github.com/wake/purdex/internal/resources"
)

const (
	// bootGrace is how long a waiting row survives its poller being gone at
	// boot: the client needs the restart time to come back (spec D-4).
	bootGrace = 30 * time.Second
	// retention is how long ended rows are kept.
	retention = 7 * 24 * time.Hour
)

// boot is the reconcile that runs before the sampler starts, under stateMu.
// Rows live in resources.db, so there is nothing to rebuild in memory: held
// rows keep their state and their persisted ewma/samples (the next pass reads
// them from the store, so the charge resumes instead of warming up again),
// waiting rows get the boot grace on their lease, and old ended rows go.
// Liveness of holders is not judged here: until the first process snapshot
// exists, unknown counts as alive (the sweeper, P1-2b, honours that).
func (m *Module) boot() {
	if m.store == nil {
		return
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	now := m.now()
	if _, err := m.store.ExtendWaiting(now.Add(bootGrace).UnixMilli()); err != nil {
		m.logf("[resources] boot grace: %v", err)
	}
	if _, err := m.store.Prune(now.Add(-retention).UnixMilli()); err != nil {
		m.logf("[resources] boot prune: %v", err)
	}
	m.bumpLocked()
}

// settings is the host setting `resources`, re-read on every call (one
// indexed SQLite read; the hostconfig PUT has no change hook). Whatever
// cannot be read or stored reads as mode measure: no database, no reader, or
// a reader error (logged once per run of failures) all fail open.
func (m *Module) settings() resources.Settings {
	measure := resources.Settings{Mode: resources.ModeMeasure}.Effective()
	if m.store == nil || m.settingsSrc == nil {
		return measure
	}
	s, err := m.settingsSrc.ResourcesSettings()
	if err != nil {
		m.noteSettings(err.Error())
		return measure
	}
	m.noteSettings("")
	return s.Effective()
}

// noteSettings logs a settings problem when it changes, and the return to
// normal; the sampler tick and every POST call settings, so it must not log
// per call.
func (m *Module) noteSettings(problem string) {
	m.noteMu.Lock()
	changed := problem != m.settingsNote
	m.settingsNote = problem
	m.noteMu.Unlock()
	if !changed {
		return
	}
	if problem == "" {
		m.logf("[resources] settings readable again")
	} else {
		m.logf("[resources] cannot read the resources setting, treating it as mode measure: %s", problem)
	}
}
