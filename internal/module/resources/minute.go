package resourcesmod

import (
	"time"

	"github.com/wake/purdex/internal/resources"
)

// The host timeline (spec D-8.2): the sampler goroutine folds each good tick
// into the current minute and writes the minute's row when the next minute's
// first tick arrives. A row holds the minute's peaks (load1, mem, measured,
// the gate's running and waiting counts), the last ncpu, and the `full`
// bookkeeping: how many ticks the flag was on, how many off→on transitions
// happened in the minute, and the longest run of it that was running in the
// minute (a run that began earlier and goes on counts). A tick that fails
// adds nothing, and a gap adds no row: a report treats a missing minute as
// no data, never as zero.
//
// It runs on the sampler goroutine only, takes no stateMu, and writes one
// row a minute (INSERT OR REPLACE; the file is WAL with a 5 s busy timeout).
// A write that fails is logged once per run of failures and not retried: the
// minute is lost, the next one is written as usual.
type minuteAgg struct {
	row      minuteRow
	open     bool // row holds the current minute
	prevFull bool
	runStart time.Time // when the current full run began (valid while prevFull)
	failing  bool
}

// noteMinute folds one good tick into the timeline. host is the tick's
// published host figures (Full already latched). The gate's slot records give
// the commands that run and wait (spec §6: they replace the lease counts, which
// are written 0 from L2 on); a store or a gate directory that cannot be read
// adds zeros, not an error. The whole tick is worked out before any of it is
// folded in, so a tick is in the minute entirely or not at all.
func (m *Module) noteMinute(host resources.HostUse, now time.Time) {
	if m.store == nil {
		return
	}
	heavyRunning, lightRunning, gateWaiting, _ := m.slotCounts()

	a := &m.minute
	at := now.Truncate(time.Minute).UnixMilli()
	if a.open && at < a.row.At {
		// The clock stepped back: the tick belongs to no minute that is open,
		// and a run measured across the step would have a negative length.
		a.prevFull = false
		return
	}
	if a.open && a.row.At != at {
		m.flushMinute()
	}
	if !a.open {
		a.row = minuteRow{At: at}
		a.open = true
	}
	r := &a.row
	r.Load1 = max(r.Load1, host.Load1)
	r.Mem = max(r.Mem, host.Mem)
	r.Measured = max(r.Measured, host.Measured)
	r.NCPU = host.NCPU
	if host.Full {
		if !a.prevFull {
			a.runStart = now
			r.FullStarts++
		}
		r.Full = true
		r.FullTicks++
		// A run seen for n ticks lasted n sampling intervals.
		r.FullLongestS = max(r.FullLongestS, int((max(0, now.Sub(a.runStart))+m.interval)/time.Second))
	}
	a.prevFull = host.Full
	// Lease v2: the tick's seconds go to its zone; an unknown zone counts as
	// sampled but belongs to none.
	secs := int(m.interval / time.Second)
	r.SampledS += secs
	zoneRank := -1
	switch resources.ParseZone(host.Zone) {
	case resources.ZoneNormal:
		r.NormalS += secs
		zoneRank = 0
	case resources.ZoneWarning:
		r.WarningS += secs
		zoneRank = 1
	case resources.ZoneDanger:
		r.DangerS += secs
		zoneRank = 2
	}
	r.ZoneMax = max(r.ZoneMax, zoneRank)
	r.PressureMax = max(r.PressureMax, host.Pressure)
	// The lease columns (held, heavy_held, sum_charge, waiting, unleased) stay as the row was made: 0.
	r.HeavyRunning = max(r.HeavyRunning, heavyRunning)
	r.LightRunning = max(r.LightRunning, lightRunning)
	r.GateWaiting = max(r.GateWaiting, gateWaiting)
}

// endMinute is the sampler going to mode off: the open minute is written and
// the run bookkeeping starts over, so the time off is not part of any run.
func (m *Module) endMinute() {
	if m.store == nil {
		return
	}
	m.flushMinute()
	m.minute.prevFull = false
}

// flushMinute writes the open minute.
func (m *Module) flushMinute() {
	a := &m.minute
	if !a.open {
		return
	}
	a.open = false
	if err := m.store.InsertMinute(a.row); err != nil {
		if !a.failing {
			m.logf("[resources] host timeline: %v", err)
		}
		a.failing = true
		return
	}
	if a.failing {
		m.logf("[resources] host timeline recovered")
	}
	a.failing = false
}
