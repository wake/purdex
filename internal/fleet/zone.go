package fleet

import (
	"time"

	"github.com/wake/purdex/internal/resources"
)

// Disk latch lines, in percent (spec §5): a level is entered at its line and left only once the reading is under the
// leave line.
const (
	diskWarnEnter   = 85
	diskWarnLeave   = 83
	diskDangerEnter = 95
	diskDangerLeave = 93
)

// DiskLatch is the zone of disk use, with hysteresis. The zero value starts from normal. Not safe for concurrent use.
type DiskLatch struct{ z resources.Zone }

// Update takes the newest used percentage; ok false (no reading) is unknown and resets the latch. Like the resources
// rule, a level is entered directly by any reading at its line and left one level at a time.
func (l *DiskLatch) Update(usedPct float64, ok bool) resources.Zone {
	if !ok {
		l.z = resources.ZoneUnknown
		return l.z
	}
	prev := l.z
	switch {
	case usedPct >= diskDangerEnter:
		l.z = resources.ZoneDanger
	case prev == resources.ZoneDanger && usedPct >= diskDangerLeave:
		l.z = resources.ZoneDanger
	case usedPct >= diskWarnEnter:
		l.z = resources.ZoneWarning
	case prev >= resources.ZoneWarning && usedPct >= diskWarnLeave:
		l.z = resources.ZoneWarning
	default:
		l.z = resources.ZoneNormal
	}
	return l.z
}

// Combine joins the resources part and the disk part of a host's zone: the worse of warning / danger if either is one,
// otherwise unknown if either is unknown, otherwise normal.
func Combine(res, disk resources.Zone) resources.Zone {
	if worst := max(res, disk); worst >= resources.ZoneWarning {
		return worst
	}
	if res == resources.ZoneUnknown || disk == resources.ZoneUnknown {
		return resources.ZoneUnknown
	}
	return resources.ZoneNormal
}

// ProbeLatch is the resources zone of one probed host: the daemon's ZoneLatch fed with the probe's memory and load, memory
// pressure 0 (a probe cannot read it). Not safe for concurrent use.
type ProbeLatch struct{ l resources.ZoneLatch }

// Update takes a good reading and the current resources settings.
func (p *ProbeLatch) Update(r Reading, s resources.Settings) resources.Zone {
	return p.l.Update(resources.HostUse{Mem: r.Mem, Load1: r.Load1, NCPU: r.NCPU}, true, s)
}

// Reset is for a probe that failed: no reading, so the zone is unknown and the latch starts again.
func (p *ProbeLatch) Reset() resources.Zone {
	return p.l.Update(resources.HostUse{}, false, resources.Settings{})
}

// TrendWindow is the fixed window a probed host's trend is read over: the most the resources setting allows. With 30 s
// samples it holds about ten, and the rule needs six spanning half the window.
const TrendWindow = 300 * time.Second

// Sample turns a good reading into a trend sample (pressure unknown).
func Sample(r Reading, at time.Time) resources.TrendSample {
	return resources.TrendSample{At: at, Mem: r.Mem, CPU: r.CPU}
}

// ProbeTrend is resources.TrendOf over the ring with the window forced to TrendWindow whatever the settings say; the
// memory and cpu slope limits come from the settings.
func ProbeTrend(ring *resources.Ring, now time.Time, s resources.Settings) resources.Trend {
	w := int(TrendWindow / time.Second)
	s.TrendWindowS = &w
	return resources.TrendOf(ring.All(), now, s)
}
