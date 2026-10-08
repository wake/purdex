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
		m.noteSettings(err.Error())
		return measure
	}
	m.noteSettings("")
	s = s.Effective()
	if m.store == nil && (s.Mode == resources.ModeAdvise || s.Mode == resources.ModeLease) {
		s.Mode = resources.ModeMeasure
	}
	return s
}

// noteSettings logs a settings problem when it changes, and the return to
// normal; the sampler tick and every GET call settings, so it must not log
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
