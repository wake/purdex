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

type swapPoint struct {
	at time.Time
	mb int
}

// LinuxPressure turns readings into the 1 / 2 / 4 level the zone latch takes
// and holds the history the debounce and the swap-growth rule need. One value
// belongs to one goroutine.
type LinuxPressure struct {
	level   int
	pending int // the level waiting to be taken, 0 when none
	since   time.Time
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
		dang = dang || psi.Full10 >= psiFull10Dang || psi.Some60 >= psiSome60Dang
		warn = warn || psi.Some10 >= psiSome10Warn || psi.Full10 >= psiFull10Warn
	}
	switch {
	case dang:
		return 4
	case warn:
		return 2
	}
	return 1
}

// Update takes a reading and returns the level: a worse one is taken after it has
// held for riseAfter, a better one after fallAfter, so a single spike moves
// nothing. The first reading sets the level at once.
func (p *LinuxPressure) Update(now time.Time, totalMB, availMB, swapUsedMB int, psi *wire.PSI) int {
	t := p.target(now, totalMB, availMB, swapUsedMB, psi)
	if p.level == 0 {
		p.level = t
		return p.level
	}
	if t == p.level {
		p.pending = 0
		return p.level
	}
	up := t > p.level
	// the wait runs while the readings keep pointing the same way
	if p.pending == 0 || (p.pending > p.level) != up {
		p.since = now
	}
	p.pending = t
	wait := fallAfter
	if up {
		wait = riseAfter
	}
	if now.Sub(p.since) >= wait {
		p.level, p.pending = t, 0
	}
	return p.level
}
