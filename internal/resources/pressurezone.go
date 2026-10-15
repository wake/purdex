package resources

// PressureZoneLatch is the daemon's own zone (host-load-control spec §6): the same signal the gate admits by, the macOS memory
// pressure level and the page rate, and nothing else. Memory in use as a percentage and the load average, which the older
// ZoneLatch also judges, do not enter it: CPU load stays informational. (ZoneLatch stays for the fleet view, which judges a
// probed host by those figures; it moves to this signal when its probes carry the level and the rate.)
//
// Entering is by any condition. Danger: pressure critical (level 4). Warning: pressure warn (level 2) or a page rate at the
// gate's limit. A zone is left, one level at a time, only when every leave condition of the current level holds: danger needs
// the level below 4; warning needs the level below 2 and the rate under ExitMargin × the limit. Leaving danger lands on normal
// directly when warning's own conditions are clear too. An unavailable reading is unknown and resets the latch; the next one
// starts again from normal. The zero value starts from normal; not safe for concurrent use.
type PressureZoneLatch struct{ z Zone }

// Update takes the newest reading — the level (0 unknown, read as normal), the page rate in pages per second and the rate at
// which the gate holds new work (pages per second; 0 or less switches the rate out) — and returns the zone.
func (l *PressureZoneLatch) Update(level int, rate, rateLimit float64, available bool) Zone {
	if !available {
		l.z = ZoneUnknown
		return ZoneUnknown
	}
	prev := l.z // unknown starts again from normal
	rated := rateLimit > 0

	next := ZoneNormal
	switch {
	case level >= 4:
		next = ZoneDanger
	case level >= 2 || (rated && rate >= rateLimit):
		next = ZoneWarning
	case prev >= ZoneWarning && !(level < 2 && (!rated || rate < ExitMargin*rateLimit)):
		next = ZoneWarning
	}
	l.z = next
	return next
}
