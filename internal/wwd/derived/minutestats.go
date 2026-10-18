package derived

import (
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// MinuteAgg totals the 5 s samples of one minute into wire.MinuteStats, with the same definitions as the Purdex host
// timeline it replaces (internal/module/resources/minute.go): full is the latched flag, a run is counted per
// off-to-on transition, and a run that began in an earlier minute counts in the minute it goes on in. The latch and the
// run live across minutes; Close only starts the next minute's totals. One value belongs to one goroutine.
type MinuteAgg struct {
	latch    FullLatch
	st       wire.MinuteStats
	prevFull bool
	runStart time.Time // when the current run began (valid while prevFull)
}

// memPct is the share of memory in use, from the record's own readings: (total - available) / total, the quantity Purdex's live
// sampler computes (hostmem.FastReader.Mem; see TestMemPctIsTheLiveSamplersArithmetic). A total of 0 reads as 0.
func memPct(m wire.Mem) float64 {
	if m.TotalMB <= 0 {
		return 0
	}
	return 100 * float64(m.TotalMB-min(m.AvailableMB, m.TotalMB)) / float64(m.TotalMB)
}

// Add folds one good sample, which stands for interval, into the open minute. A sample that failed to be taken is not
// added (the minute simply has fewer seconds sampled).
func (a *MinuteAgg) Add(f wire.Facts, interval time.Duration) {
	full := a.latch.Update(f.CPU.Load1, f.CPU.NCPU, memPct(f.Mem), f.Mem.Pressure)
	if full {
		if !a.prevFull {
			a.runStart = f.At
			a.st.FullStarts++
		}
		a.st.FullTicks++
		// a run seen for n samples lasted n sampling intervals
		a.st.FullLongestS = max(a.st.FullLongestS, int((max(0, f.At.Sub(a.runStart))+interval)/time.Second))
	}
	a.prevFull = full
	secs := int(interval / time.Second)
	a.st.SampledS += secs
	switch f.Derived.Zone {
	case wire.ZoneNormal:
		a.st.NormalS += secs
	case wire.ZoneWarning:
		a.st.WarningS += secs
	case wire.ZoneDanger:
		a.st.DangerS += secs
	}
	a.st.HeavyRunningMax = max(a.st.HeavyRunningMax, f.Worker.Running.Heavy)
	a.st.LightRunningMax = max(a.st.LightRunningMax, f.Worker.Running.Light)
}

// Break ends the run in progress (the clock stepped back, so a length measured across the step would be negative): the
// next full sample is a new start.
func (a *MinuteAgg) Break() { a.prevFull = false }

// Snapshot is the open minute's totals so far.
func (a *MinuteAgg) Snapshot() wire.MinuteStats { return a.st }

// Close returns the open minute's totals and starts the next minute's from zero; the run in progress carries on.
func (a *MinuteAgg) Close() wire.MinuteStats {
	st := a.st
	a.st = wire.MinuteStats{}
	return st
}
