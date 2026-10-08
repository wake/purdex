package resourcesmod

import (
	"time"

	"github.com/wake/purdex/internal/resources"
)

// The host timeline (spec D-8.2): the sampler goroutine folds each good tick
// into the current minute and writes the minute's row when the next minute's
// first tick arrives. A row holds the minute's peaks (load1, mem, measured,
// the lease counts, Σ charge, unleased), the last ncpu, and the `full`
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
// published host figures (Full already latched).
func (m *Module) noteMinute(host resources.HostUse, now time.Time) {
	if m.store == nil {
		return
	}
	a := &m.minute
	at := now.Truncate(time.Minute).UnixMilli()
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
		r.FullLongestS = max(r.FullLongestS, int((now.Sub(a.runStart)+m.interval)/time.Second))
	}
	a.prevFull = host.Full

	held, err := m.store.Active()
	if err != nil {
		return // the lease figures of this tick are unknown, not zero
	}
	waiting, err := m.store.CountWaiting()
	if err != nil {
		return
	}
	s := m.settings()
	use := m.leaseUseSnapshot()
	var sum, cpu, mem float64
	heavy := 0
	for _, l := range held {
		sum += resources.Charge(resources.Lease{ID: l.ID, Weight: l.Weight, GrantedAt: time.UnixMilli(l.GrantedAt),
			Measured: l.EWMA, Samples: l.Samples}, now, s)
		cpu += use[l.ID].CPU
		mem += use[l.ID].Mem
		if l.Weight >= s.HeavyMin() {
			heavy++
		}
	}
	r.Held = max(r.Held, len(held))
	r.HeavyHeld = max(r.HeavyHeld, heavy)
	r.SumCharge = max(r.SumCharge, sum)
	r.Waiting = max(r.Waiting, waiting)
	r.Unleased = max(r.Unleased, max(0, float64(host.Measured)-max(cpu, mem)))
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
