package derived

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

const tick = 5 * time.Second

var m0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// sample is the 5 s record n ticks after m0.
type sample struct {
	load1    float64
	availMB  int
	pressure int
	zone     wire.Zone
	heavy    int
	light    int
}

func rec(n int, s sample) wire.Facts {
	return wire.Facts{
		At:      m0.Add(time.Duration(n) * tick),
		CPU:     wire.CPU{NCPU: 10, Load1: s.load1},
		Mem:     wire.Mem{TotalMB: 16000, AvailableMB: s.availMB, Pressure: s.pressure},
		Derived: wire.Derived{Zone: s.zone},
		Worker:  wire.FactsWorker{Running: wire.Slots{Heavy: s.heavy, Light: s.light}},
	}
}

var calm = sample{load1: 1, availMB: 8000, pressure: 1, zone: wire.ZoneNormal}

func TestMinuteAggSecondsZonesAndRunningMax(t *testing.T) {
	var a MinuteAgg
	for n := 0; n < 12; n++ {
		s := calm
		switch {
		case n >= 8 && n < 10:
			s.zone = wire.ZoneWarning
		case n == 10:
			s.zone = wire.ZoneDanger
		case n == 11:
			s.zone = wire.ZoneUnknown // sampled, in no zone
		}
		s.heavy, s.light = []int{0, 2, 0, 1}[n%4], []int{0, 0, 3, 0}[n%4]
		a.Add(rec(n, s), tick)
	}
	got := a.Close()
	want := wire.MinuteStats{SampledS: 60, NormalS: 40, WarningS: 10, DangerS: 5, HeavyRunningMax: 2, LightRunningMax: 3}
	if got != want {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if got.GateWaitingMax != nil {
		t.Error("wwd has no gate to count: null")
	}
}

// A minute with few samples (wwd started inside it) says so; nothing is invented for the rest.
func TestMinuteAggCountsOnlyWhatWasSampled(t *testing.T) {
	var a MinuteAgg
	for n := 8; n < 12; n++ {
		a.Add(rec(n, calm), tick)
	}
	if got := a.Close(); got.SampledS != 20 || got.NormalS != 20 {
		t.Fatalf("%+v", got)
	}
	if got := a.Close(); got != (wire.MinuteStats{}) {
		t.Fatalf("a minute with no samples is all zero: %+v", got)
	}
}

// Full uses the latch (0.9 hysteresis), not each reading alone: load 9.5 on 10 cores is not enough to enter but keeps a
// run going once it began.
func TestMinuteAggFullRunsFollowTheLatch(t *testing.T) {
	var a MinuteAgg
	hot := func(load float64) sample { s := calm; s.load1 = load; return s }
	loads := []float64{1, 9.5, 10, 9.5, 9.5, 8.9, 1, 10, 1, 1, 1, 1} // 9.5 alone: not full; after 10: still full; 8.9: leaves
	for n, l := range loads {
		a.Add(rec(n, hot(l)), tick)
	}
	got := a.Close()
	// full at n=2,3,4 (a run of 3) and n=7 (a run of 1); n=1 (9.5 before any entry) is not full
	if got.FullTicks != 4 || got.FullStarts != 2 || got.FullLongestS != 15 {
		t.Fatalf("%+v", got)
	}
}

// Memory and pressure make it full too, by the same definition as the Purdex latch.
func TestMinuteAggFullByMemoryAndPressure(t *testing.T) {
	var a MinuteAgg
	lowMem := calm
	lowMem.availMB = 1000 // 93.75 % in use
	warn := calm
	warn.pressure = 2
	a.Add(rec(0, lowMem), tick)
	a.Add(rec(1, calm), tick) // 50 % in use, load low: leaves
	a.Add(rec(2, warn), tick)
	if got := a.Close(); got.FullTicks != 2 || got.FullStarts != 2 {
		t.Fatalf("%+v", got)
	}
}

// A run that began in an earlier minute and goes on counts in this one: it is not a new start, and its length is the
// whole run so far.
func TestMinuteAggRunAcrossMinutes(t *testing.T) {
	var a MinuteAgg
	hot := calm
	hot.load1 = 12
	for n := 9; n < 12; n++ { // the last three samples of minute one
		a.Add(rec(n, hot), tick)
	}
	first := a.Close()
	if first.FullTicks != 3 || first.FullStarts != 1 || first.FullLongestS != 15 {
		t.Fatalf("first %+v", first)
	}
	for n := 12; n < 15; n++ { // minute two begins hot, then cools
		s := hot
		if n >= 14 {
			s = calm
		}
		a.Add(rec(n, s), tick)
	}
	second := a.Close()
	if second.FullTicks != 2 || second.FullStarts != 0 || second.FullLongestS != 25 {
		t.Fatalf("second %+v", second)
	}
}

// Break (a clock step back, or the sampler off) ends the run: the next full sample is a new start.
func TestMinuteAggBreakEndsTheRun(t *testing.T) {
	var a MinuteAgg
	hot := calm
	hot.load1 = 12
	a.Add(rec(0, hot), tick)
	a.Break()
	a.Add(rec(1, hot), tick)
	if got := a.Close(); got.FullStarts != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestMinuteAggSnapshotDoesNotCloseTheMinute(t *testing.T) {
	var a MinuteAgg
	a.Add(rec(0, calm), tick)
	if s := a.Snapshot(); s.SampledS != 5 {
		t.Fatalf("%+v", s)
	}
	a.Add(rec(1, calm), tick)
	if s := a.Close(); s.SampledS != 10 {
		t.Fatalf("%+v", s)
	}
}

// Memory in use is the quantity Purdex's own live sampler computes (hostmem.FastReader.Mem): (total - (Free + Inactive)
// pages) / total, with Mach's Free already holding the speculative pages (#3074; the vm_stat text path adds Speculative
// separately to a Free that lacks them: the same sum). wwd's available_mb is (Free + Inactive) from Mach, so the share
// derived from the record is that quantity, up to the whole-MB rounding of the two figures.
func TestMemPctIsTheLiveSamplersArithmetic(t *testing.T) {
	const pageSize = 16384
	free, inactive := uint64(124_288), uint64(200_000) // Mach free_count includes the 24 288 speculative pages
	totalBytes := uint64(16 << 30)
	availBytes := (free + inactive) * pageSize
	want := 100 * (1 - float64(availBytes)/float64(totalBytes))
	got := memPct(wire.Mem{TotalMB: int(totalBytes >> 20), AvailableMB: int(availBytes >> 20)})
	if d := got - want; d > 0.01 || d < -0.01 {
		t.Fatalf("memPct %.4f, the live sampler's %.4f", got, want)
	}
}
