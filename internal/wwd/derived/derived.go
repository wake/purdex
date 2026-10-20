// Package derived is the one place a host's zone, headroom and sessions_room are
// decided (spec §3.2). Every reader of the pool gets these from the facts
// record; none re-derives them.
package derived

import "github.com/wake/purdex/internal/wwd/wire"

const (
	// PageRateLimit is the macOS page rate (pageouts + swapouts per second over
	// 10 s) at and above which a host is swapping for real (the gate's "auto":
	// measured on mlab 2026-10-15, every full run stayed at or under 28/s). On
	// Linux the rate is not used for the zone: pass 0 as the limit.
	PageRateLimit = 50.0
	// ExitMargin is how far below its entry line a reading must fall before a
	// zone is left.
	ExitMargin = 0.9

	sessionReserveMB = 1024 // memory kept for the host itself before any session
	sessionMB        = 512  // one more agent session (watcher 10-18: about 0.5 GB)
	sessionsCap      = 8
)

// ZoneLatch is internal/resources.PressureZoneLatch, copied because wwd must not
// import that package (it pulls in internal/agent); a test keeps the two equal.
// Entering a zone is by any condition; a zone is left, one level at a time, only
// when every leave condition of the current one holds. An unavailable reading is
// unknown and resets the latch. The zero value starts from normal; not safe for
// concurrent use.
type ZoneLatch struct{ z wire.Zone }

func zoneRank(z wire.Zone) int {
	switch z {
	case wire.ZoneDanger:
		return 3
	case wire.ZoneWarning:
		return 2
	}
	return 1
}

// Update takes the newest reading: the pressure level (1 normal, 2 warning, 4
// critical), the page rate, the rate at which a zone is entered (0 or less
// switches the rate out) and whether the reading exists at all.
func (l *ZoneLatch) Update(level int, rate, rateLimit float64, available bool) wire.Zone {
	if !available {
		l.z = wire.ZoneUnknown
		return wire.ZoneUnknown
	}
	prev := l.z // unknown starts again from normal
	rated := rateLimit > 0

	next := wire.ZoneNormal
	switch {
	case level >= 4:
		next = wire.ZoneDanger
	case level >= 2 || (rated && rate >= rateLimit):
		next = wire.ZoneWarning
	case zoneRank(prev) >= 2 && !(level < 2 && (!rated || rate < ExitMargin*rateLimit)):
		next = wire.ZoneWarning
	}
	l.z = next
	return next
}

// Hold is an active exception as headroom sees it: it always holds heavy work,
// and light work too when Light is set.
type Hold struct {
	Name    string
	Light   bool
	Release bool // a release hold (spec §13.3): the light slots drop to 1 while it lasts
}

// releaseLightSlots is the light slots a release hold leaves: min(setting, 1). facts' worker.slots still shows the setting.
func releaseLightSlots(slots int, holds []Hold) int {
	for _, h := range holds {
		if h.Release {
			return min(slots, 1)
		}
	}
	return slots
}

// Input is what the judgement needs from the rest of the record.
type Input struct {
	Pressure      int     // kernel level, or the Linux mapping (2 warning, 4 danger)
	PressureKnown bool    // false: the platform could not say
	PageRate      float64 // pageouts + swapouts per second; ignored when RateLimit is 0
	RateLimit     float64 // PageRateLimit on macOS, 0 on Linux
	Slots         wire.Slots
	Running       wire.Slots
	OwnerActive   bool
	Exceptions    []Hold
	AvailableMB   int
	CompressorMB  int // memory held by the compressor (0 where there is none)
	// GateHeavyFull: the pdx gate on this host has all its heavy slots held (or cannot be read). Until WWD-6 a queue job is outside the
	// gate's slots, so a full gate stops new heavy work here; the job already running is not touched (#3447).
	GateHeavyFull bool
	// DiskLow: the work volume has less free space than the worker's disk floor (DiskLow). Heavy work is held; light work is not.
	DiskLow bool
}

// Compute is the derived block of a facts record. l keeps the zone between
// calls (one latch per host). Headroom is decided by the reasons of reasons.go.
func Compute(l *ZoneLatch, in Input) wire.Derived {
	d, _ := ComputeWithReasons(l, in)
	return d
}

func free(slots, running int) int { return max(slots-running, 0) }
