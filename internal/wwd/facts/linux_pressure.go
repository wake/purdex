package facts

import (
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The Linux pressure mapping of spec §3.1 (starting values; M-L could not make
// a real host-wide shortage beside production services). The PSI it reads is
// that of the production slices, never the host-wide one.
const (
	psiSome10Warn = 10.0
	psiFull10Warn = 1.0
	psiFull10Dang = 10.0
	psiSome60Dang = 25.0

	availWarnFrac = 0.20
	availDangMB   = 1536
	swapWarnMB    = 512
	swapGrowDang  = 1024 // MB within swapGrowWindow

	swapGrowWindow = time.Minute
	riseAfter      = 10 * time.Second // a worse level must hold this long
	fallAfter      = 30 * time.Second // a better one this long
)

// PSIWarn says whether a PSI reading alone calls for warning (level 2): the PSI part of the mapping below, without the available-memory and
// swap conditions. The watcher's pressure rules (S3-2) judge a Linux sample by it, so they and the zone read the same numbers.
func PSIWarn(p wire.PSI) bool { return p.Some10 >= psiSome10Warn || p.Full10 >= psiFull10Warn }

// PSIDanger says whether a PSI reading alone calls for danger (level 4).
func PSIDanger(p wire.PSI) bool { return p.Full10 >= psiFull10Dang || p.Some60 >= psiSome60Dang }

type swapPoint struct {
	at time.Time
	mb int
}

// LinuxPressure turns readings into the 1 / 2 / 4 level the zone latch takes
// and holds the history the debounce and the swap-growth rule need. One value
// belongs to one goroutine.
type LinuxPressure struct {
	level   int // 0: unknown
	pending int // the level waiting to be taken, valid while waiting
	waiting bool
	since   time.Time // when pending was first called for
	swap    []swapPoint
}

// target is the level the readings call for right now, before any debounce.
func (p *LinuxPressure) target(now time.Time, totalMB, availMB, swapUsedMB int, psi *wire.PSI) int {
	keep := p.swap[:0]
	for _, s := range p.swap {
		if now.Sub(s.at) <= swapGrowWindow {
			keep = append(keep, s)
		}
	}
	p.swap = append(keep, swapPoint{now, swapUsedMB})
	grew := 0
	for _, s := range p.swap {
		grew = max(grew, swapUsedMB-s.mb)
	}

	dang := availMB < availDangMB || grew > swapGrowDang
	warn := float64(availMB) < availWarnFrac*float64(totalMB) || swapUsedMB > swapWarnMB
	if psi != nil {
		dang = dang || PSIDanger(*psi)
		warn = warn || PSIWarn(*psi)
	}
	switch {
	case dang:
		return 4
	case warn:
		return 2
	case psi == nil:
		// memory and swap can show trouble without PSI but cannot prove calm: with
		// no production-slice pressure to read (cgroup v1, PSI off) the level is unknown
		return 0
	}
	return 1
}

// Update takes a reading and returns the level: a worse one is taken after it has
// held for riseAfter, a better one after fallAfter, so a single spike moves
// nothing. Each new level has its own clock: the wait starts over whenever the
// level the readings call for changes. Unknown (0) and a calm first reading
// are taken at once; a first reading that is already 2 or 4 waits like any
// rise, the level staying unknown (0) until it has held.
func (p *LinuxPressure) Update(now time.Time, totalMB, availMB, swapUsedMB int, psi *wire.PSI) int {
	t := p.target(now, totalMB, availMB, swapUsedMB, psi)
	if t == p.level || t == 0 || (p.level == 0 && t == 1) {
		p.level, p.waiting = t, false
		return p.level
	}
	if !p.waiting || p.pending != t {
		p.pending, p.since, p.waiting = t, now, true
	}
	wait := fallAfter
	if t > p.level {
		wait = riseAfter
	}
	if now.Sub(p.since) >= wait {
		p.level, p.waiting = t, false
	}
	return p.level
}
