package resources

// Zone is how loaded the host is, for admission and alerts (lease v2 spec
// D-1′).
type Zone int

const (
	// ZoneUnknown is a reading that cannot be judged: the sampler is failing
	// or off, or the cpu count is missing. Admission fails open on it.
	ZoneUnknown Zone = iota
	ZoneNormal
	ZoneWarning
	ZoneDanger
)

func (z Zone) String() string {
	switch z {
	case ZoneNormal:
		return "normal"
	case ZoneWarning:
		return "warning"
	case ZoneDanger:
		return "danger"
	}
	return "unknown"
}

// zoneLeaveMargin is how far below its entry line a memory reading has to
// fall before the zone is left, in points.
const zoneLeaveMargin = 2

// ZoneLatch turns the stateless readings of HostUse into the zone the
// sampler publishes. The zero value starts from normal. One latch belongs to
// one sampler goroutine; it is not safe for concurrent use.
type ZoneLatch struct{ z Zone }

// Update takes the newest reading and returns the zone.
//
// Entering is by any condition. Danger: pressure critical (4) or memory at
// mem_danger_pct. Warning: pressure warn (2), memory at mem_warn_pct, or
// load1 at the core count (load never makes danger: CPU saturation slows,
// memory exhaustion crashes). A zone is left, one level at a time, only when
// every leave condition of the current level holds: danger needs pressure
// below 4 and memory 2 points under its line; warning needs pressure below 2,
// memory 2 points under its line and load1 under 0.9 x ncpu. Leaving danger
// can land on normal directly when warning's own conditions are clear too.
// An unavailable reading, or one without a cpu count, is unknown and resets
// the latch.
func (l *ZoneLatch) Update(h HostUse, available bool, s Settings) Zone {
	if !available || h.NCPU <= 0 {
		l.z = ZoneUnknown
		return ZoneUnknown
	}
	warn, danger := float64(s.MemWarn()), float64(s.MemDanger())
	ncpu := float64(h.NCPU)
	prev := l.z // unknown starts again from normal

	next := ZoneNormal
	switch {
	case h.Pressure >= 4 || h.Mem >= danger:
		next = ZoneDanger
	case prev == ZoneDanger && !(h.Pressure < 4 && h.Mem < danger-zoneLeaveMargin):
		next = ZoneDanger
	case h.Pressure >= 2 || h.Mem >= warn || h.Load1 >= ncpu:
		next = ZoneWarning
	case prev >= ZoneWarning && !(h.Pressure < 2 && h.Mem < warn-zoneLeaveMargin && h.Load1 < ExitMargin*ncpu):
		next = ZoneWarning
	}
	l.z = next
	return next
}

// ParseZone is the inverse of Zone.String; anything else is unknown.
func ParseZone(s string) Zone {
	for _, z := range []Zone{ZoneNormal, ZoneWarning, ZoneDanger} {
		if z.String() == s {
			return z
		}
	}
	return ZoneUnknown
}
