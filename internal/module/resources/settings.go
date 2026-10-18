package resourcesmod

import "github.com/wake/purdex/internal/resources"

// settings is the host setting `resources`, re-read on every call (one
// indexed SQLite read; the hostconfig PUT has no change hook). Whatever
// cannot be read reads as mode measure, so the module fails open: no reader,
// or a reader error (logged once per run of failures).
//
// Without a database nothing can be queued, so advise and lease read as
// measure too; off needs no database and stays off.
func (m *Module) settings() resources.Settings {
	measure := resources.Settings{Mode: resources.ModeMeasure}.Effective()
	if m.settingsSrc == nil {
		return measure
	}
	s, err := m.settingsSrc.ResourcesSettings()
	if err != nil {
		m.noteSettings(err)
		return measure
	}
	m.noteSettings(nil)
	s = s.Effective()
	if m.store == nil && (s.Mode == resources.ModeAdvise || s.Mode == resources.ModeLease) {
		s.Mode = resources.ModeMeasure
	}
	return s
}

// noteSettings logs a settings problem when a run of failures starts, and the
// return to normal when it ends; the sampler tick and the lease routes call
// settings, so it must not log per call. The key is the run, not the error
// text: a reader whose message changes on every call (a timestamp, a SQLite
// detail) still logs once. The log call is under the lock so the two lines of
// a run cannot come out in the wrong order.
func (m *Module) noteSettings(err error) {
	m.noteMu.Lock()
	defer m.noteMu.Unlock()
	failing := err != nil
	if failing == m.settingsFailing {
		return
	}
	m.settingsFailing = failing
	if failing {
		m.logf("[resources] cannot read the resources setting, treating it as mode measure: %v", err)
	} else {
		m.logf("[resources] settings readable again")
	}
}
