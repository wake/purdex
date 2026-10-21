// Package watchwake is the pure part of the watcher's wake settings (docs/specs/2026-10-21-watcher-wake-settings-spec.md): the document of
// section 2, its validation (Normalize), its etag, and the migration from the old `watch` host setting. It reads no store and no clock; the
// watch module keeps the document and serves it.
package watchwake

// Version is the version of the document this build understands. A stored document with a larger version is read-only for this build.
const Version = 1

// Settings is the whole document. A PUT replaces all of it: every field is required.
type Settings struct {
	Version int `json:"version"`
	// Enabled is the master switch: with it off the wake-ups other than the patrol do not happen. The patrol has no switch at all.
	Enabled bool `json:"enabled"`
	// AutoStart: whether a wake-up other than the patrol may call up or start a watcher when none is awake (the old `auto_wake`).
	AutoStart bool  `json:"auto_start"`
	Rules     Rules `json:"rules"`
}

// Rules holds one rule per kind of wake-up.
type Rules struct {
	Patrol            Patrol   `json:"patrol"`
	DiskLow           DiskLow  `json:"disk_low"`
	WwdAlert          WwdAlert `json:"wwd_alert"`
	UnattendedChanged Simple   `json:"unattended_changed"`
	UserBack          UserBack `json:"user_back"`
}

// Patrol has no `enabled`: it cannot be removed. MaxGapHours bounds how far the intervals may be relaxed.
type Patrol struct {
	Prompt      string    `json:"prompt"`
	IntervalMin Intervals `json:"interval_min"`
	MaxGapHours int       `json:"max_gap_hours"`
	Times       []string  `json:"times"` // "HH:MM", sorted, read in TZ
	TZ          string    `json:"tz"`
}

// Intervals are minutes between patrols per situation; 0 leaves the situation to the max gap alone.
type Intervals struct {
	Resting int `json:"resting"`
	Present int `json:"present"`
	Away    int `json:"away"`
	Busy    int `json:"busy"`
	Calm    int `json:"calm"`
}

// DiskLow wakes the watcher once when the free disk space falls below BelowGB.
type DiskLow struct {
	Enabled bool   `json:"enabled"`
	Prompt  string `json:"prompt"`
	BelowGB int    `json:"below_gb"`
}

// WwdAlert chooses which wwd alerts wake the watcher: by level (a subset of push, digest, watch) and by kind (KindsOff are left alone).
type WwdAlert struct {
	Enabled  bool     `json:"enabled"`
	Prompt   string   `json:"prompt"`
	Levels   []string `json:"levels"`
	KindsOff []string `json:"kinds_off"`
}

// Simple is a rule with nothing but a switch and a prompt.
type Simple struct {
	Enabled bool   `json:"enabled"`
	Prompt  string `json:"prompt"`
}

// UserBack wakes the watcher when the person comes back to the computer after at least AwayMin minutes.
type UserBack struct {
	Enabled bool   `json:"enabled"`
	Prompt  string `json:"prompt"`
	AwayMin int    `json:"away_min"`
}

// The levels a wwd alert has, in the order the document keeps them.
var allLevels = []string{"push", "digest", "watch"}

// The defaults are today's behaviour (the old `watch` row's defaults). tz is the zone the fixed times are read in.
const (
	defaultMaxGapHours = 24
	defaultBelowGB     = 14
	defaultAwayMin     = 30
)

// Default is the document of a host that never set anything: every rule on, today's intervals, no fixed times.
func Default(tz string) Settings {
	return Settings{
		Version: Version, Enabled: true, AutoStart: true,
		Rules: Rules{
			Patrol: Patrol{
				IntervalMin: Intervals{Resting: 10, Present: 30, Away: 30, Busy: 10, Calm: 15},
				MaxGapHours: defaultMaxGapHours, Times: []string{}, TZ: tz,
			},
			DiskLow:           DiskLow{Enabled: true, BelowGB: defaultBelowGB},
			WwdAlert:          WwdAlert{Enabled: true, Levels: append([]string{}, allLevels...), KindsOff: []string{}},
			UnattendedChanged: Simple{Enabled: true},
			UserBack:          UserBack{Enabled: true, AwayMin: defaultAwayMin},
		},
	}
}

// ensureLists makes the lists non-nil, so they are [] on the wire.
func (s *Settings) ensureLists() {
	if s.Rules.Patrol.Times == nil {
		s.Rules.Patrol.Times = []string{}
	}
	if s.Rules.WwdAlert.Levels == nil {
		s.Rules.WwdAlert.Levels = []string{}
	}
	if s.Rules.WwdAlert.KindsOff == nil {
		s.Rules.WwdAlert.KindsOff = []string{}
	}
}
