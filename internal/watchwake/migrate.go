package watchwake

import "github.com/wake/purdex/internal/watchsettings"

// FromWatchRow is the first document of a host that has the old `watch` row (spec section 4): auto_wake becomes auto_start; the four patrol
// intervals and the disk threshold move to their new places, with away starting as present; tz is the zone given (the source's local zone).
// The row's `enabled` is NOT carried over: it only ever switched the patrol, and the patrol cannot be switched off, so the master switch and
// every rule start on. A value the new document would refuse (the old row could hold a zero or a number out of range) becomes the default.
func FromWatchRow(w watchsettings.Settings, tz string) Settings {
	s := Default(tz)
	s.AutoStart = w.AutoWake
	d := s.Rules.Patrol.IntervalMin
	p := &s.Rules.Patrol.IntervalMin
	p.Present = orDefault(w.PatrolPresentMin, 1, maxIntervalMinutes, d.Present)
	p.Away = p.Present
	p.Busy = orDefault(w.PatrolBusyMin, 1, maxIntervalMinutes, d.Busy)
	p.Calm = orDefault(w.PatrolCalmMin, 1, maxIntervalMinutes, d.Calm)
	p.Resting = orDefault(w.PatrolRestMin, 1, maxIntervalMinutes, d.Resting)
	s.Rules.DiskLow.BelowGB = orDefault(w.DiskLowGB, 1, 100000, defaultBelowGB)
	return s
}

func orDefault(v, lo, hi, def int) int {
	if v < lo || v > hi {
		return def
	}
	return v
}
