package advisor

import (
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ---- the watcher's three pressure rules (spec §13.3, WWD switch S3-2b) ----

// mac is a macOS sample at `at` with the given mem.pressure (and RAM 16 GB, a read memory source). The zone is deliberately left at
// "warning": a rule must not look at it (air19's zone sits at warning on page-outs alone while mem.pressure is 1).
func mac(at time.Time, pressure int) wire.Facts {
	return wire.Facts{At: at, Platform: "darwin", Derived: wire.Derived{Zone: wire.ZoneWarning}, Mem: wire.Mem{TotalMB: 16384, Pressure: pressure}}
}

// lin is a Linux sample with the given PSI (nil: not readable).
func lin(at time.Time, psi *wire.PSI) wire.Facts {
	return wire.Facts{At: at, Platform: "linux", Mem: wire.Mem{TotalMB: 16384, Pressure: 1, PSI: psi}}
}

// hole is a sample whose memory source failed: no reading (TotalMB 0), as Sampler.LocalRecent gives it.
func hole(at time.Time) wire.Facts { return wire.Facts{At: at, Platform: "darwin"} }

// run5 makes n samples 5 s apart from start, sample i from f(i).
func run5(start time.Time, n int, f func(at time.Time, i int) wire.Facts) []wire.Facts {
	out := make([]wire.Facts, n)
	for i := range out {
		out[i] = f(start.Add(time.Duration(i)*5*time.Second), i)
	}
	return out
}

// recentInput is an Input whose 5 s samples are recs, judged at now.
func recentInput(now time.Time, recs ...wire.Facts) Input {
	return Input{Now: now, Facts: recs[len(recs)-1], Recent: func(since time.Time) []wire.Facts {
		var out []wire.Facts
		for _, f := range recs {
			if !f.At.Before(since) {
				out = append(out, f)
			}
		}
		return out
	}}
}

func raisedAs(in Input, key string) Input {
	in.Raised = func(k string) bool { return k == key }
	return in
}

// ---- pressure watch ----

func TestPressureWatch_SixtySecondsOfPressureSamples(t *testing.T) {
	// 12 samples (0, 5 … 55 s) are 60 s of sampling; 11 are 55 s
	for n, want := range map[int]bool{11: false, 12: true, 13: true} {
		recs := run5(t0, n, func(at time.Time, i int) wire.Facts { return mac(at, 2) })
		v, err := PressureWatch{}.Eval(recentInput(recs[n-1].At, recs...))
		if err != nil || v.Holds != want || v.Key != "pressure_watch" || v.Level != wire.AlertWatch {
			t.Errorf("%d pressure samples: %+v %v, want holds=%v", n, v, err, want)
		}
	}
	recs := run5(t0, 13, func(at time.Time, i int) wire.Facts { return mac(at, 2) })
	v, _ := PressureWatch{}.Eval(recentInput(recs[12].At, recs...))
	if !strings.Contains(v.Detail, "60") {
		t.Errorf("detail = %q", v.Detail)
	}
}

func TestPressureWatch_OneCalmSampleOrOneHoleBreaksTheMinute(t *testing.T) {
	for name, mid := range map[string]func(at time.Time) wire.Facts{
		"a calm sample":           func(at time.Time) wire.Facts { return mac(at, 1) },
		"a failed memory read":    hole,
		"an unknown level (0)":    func(at time.Time) wire.Facts { return mac(at, 0) },
		"the zone alone (calm 1)": func(at time.Time) wire.Facts { return mac(at, 1) }, // zone is warning in mac(): the rule must not read it
	} {
		recs := run5(t0, 13, func(at time.Time, i int) wire.Facts {
			if i == 6 {
				return mid(at)
			}
			return mac(at, 2)
		})
		v, err := PressureWatch{}.Eval(recentInput(recs[12].At, recs...))
		if err != nil || v.Holds {
			t.Errorf("%s: %+v %v", name, v, err)
		}
	}
}

// A gap between samples (the daemon paused, the sampler failed for a while) is a hole too: two pressure samples a minute apart are not a minute.
func TestPressureWatch_AGapIsNotPressure(t *testing.T) {
	recs := append(run5(t0, 6, func(at time.Time, i int) wire.Facts { return mac(at, 2) }),
		run5(t0.Add(60*time.Second), 6, func(at time.Time, i int) wire.Facts { return mac(at, 2) })...)
	v, err := PressureWatch{}.Eval(recentInput(recs[len(recs)-1].At, recs...))
	if err != nil || v.Holds {
		t.Fatalf("%+v %v", v, err)
	}
}

// air19: a high page-out rate keeps the zone at warning while mem.pressure stays 1: not pressure.
func TestPressureWatch_ZoneWarningWithPressureOneIsCalm(t *testing.T) {
	recs := run5(t0, 40, func(at time.Time, i int) wire.Facts {
		f := mac(at, 1)
		f.Mem.PageOutRate = fp(92)
		return f
	})
	v, err := PressureWatch{}.Eval(recentInput(recs[39].At, recs...))
	if err != nil || v.Holds {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestPressureWatch_LinuxUsesPSIAtTheWarningLine(t *testing.T) {
	for name, c := range map[string]struct {
		psi  *wire.PSI
		want bool
	}{
		"some10 at the line":     {&wire.PSI{Some10: 10}, true},
		"full10 at the line":     {&wire.PSI{Full10: 1}, true},
		"some10 just under":      {&wire.PSI{Some10: 9.9}, false},
		"full10 just under":      {&wire.PSI{Full10: 0.9}, false},
		"PSI not readable":       {nil, false},
		"some60 alone is danger": {&wire.PSI{Some60: 30}, false}, // not the warning line: it is pressure_danger's
	} {
		recs := run5(t0, 13, func(at time.Time, i int) wire.Facts { return lin(at, c.psi) })
		v, err := PressureWatch{}.Eval(recentInput(recs[12].At, recs...))
		if err != nil || v.Holds != c.want {
			t.Errorf("%s: %+v %v, want %v", name, v, err, c.want)
		}
	}
}

func TestPressureWatch_TwoEntriesInThirtyMinutes(t *testing.T) {
	// 4 samples of calm, 2 of pressure, repeated: each pressure run is 10 s, far short of a minute; what counts is the entries
	build := func(entries int) []wire.Facts {
		var recs []wire.Facts
		at := t0
		for e := 0; e < entries; e++ {
			for i := 0; i < 4; i++ {
				recs, at = append(recs, mac(at, 1)), at.Add(5*time.Second)
			}
			for i := 0; i < 2; i++ {
				recs, at = append(recs, mac(at, 2)), at.Add(5*time.Second)
			}
		}
		return append(recs, mac(at, 1)) // ends calm
	}
	for entries, want := range map[int]bool{0: false, 1: false, 2: true, 3: true} {
		recs := build(entries)
		v, err := PressureWatch{}.Eval(recentInput(recs[len(recs)-1].At, recs...))
		if err != nil || v.Holds != want {
			t.Errorf("%d entries: %+v %v, want holds=%v", entries, v, err, want)
		}
	}
	// the same two entries but the first is 31 minutes old: only one is in the window
	old := build(1)
	later := t0.Add(31 * time.Minute)
	recs := append(old, run5(later, 6, func(at time.Time, i int) wire.Facts {
		if i < 4 {
			return mac(at, 1)
		}
		return mac(at, 2)
	})...)
	v, _ := PressureWatch{}.Eval(recentInput(recs[len(recs)-1].At, recs...))
	if v.Holds {
		t.Errorf("an entry 31 minutes ago counted: %+v", v)
	}
}

// Calm, then a hole, then pressure is not an entry: nothing says the pressure began after the calm.
func TestPressureWatch_AHoleIsNotAnEntry(t *testing.T) {
	var recs []wire.Facts
	at := t0
	for e := 0; e < 2; e++ {
		recs = append(recs, mac(at, 1), hole(at.Add(5*time.Second)), mac(at.Add(10*time.Second), 2), mac(at.Add(15*time.Second), 1))
		at = at.Add(20 * time.Second)
	}
	v, err := PressureWatch{}.Eval(recentInput(recs[len(recs)-1].At, recs...))
	if err != nil || v.Holds {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestPressureWatch_ClearsAfterTenCalmMinutes(t *testing.T) {
	// a pressure sample, then calm: at 9 min it stands, at 10 min it clears
	build := func(calmFor time.Duration) []wire.Facts {
		recs := []wire.Facts{mac(t0, 2)}
		for at := t0.Add(5 * time.Second); !at.After(t0.Add(calmFor)); at = at.Add(5 * time.Second) {
			recs = append(recs, mac(at, 1))
		}
		return recs
	}
	for calm, want := range map[time.Duration]bool{9 * time.Minute: true, 10*time.Minute + 5*time.Second: false} {
		recs := build(calm)
		in := raisedAs(recentInput(recs[len(recs)-1].At, recs...), "pressure_watch")
		v, err := PressureWatch{}.Eval(in)
		if err != nil || v.Holds != want {
			t.Errorf("calm for %s: %+v %v, want holds=%v", calm, v, err, want)
		}
	}
	// nothing was read for the last ten minutes: it cannot be shown calm, so the alert is left as it was (an error, not a clear)
	recs := run5(t0, 130, func(at time.Time, i int) wire.Facts { return hole(at) })
	if _, err := (PressureWatch{}).Eval(raisedAs(recentInput(recs[129].At, recs...), "pressure_watch")); err == nil {
		t.Error("a raised alert was cleared on ten minutes of holes")
	}
	// and with no samples at all and not raised: simply does not hold
	in := Input{Now: t0, Recent: func(time.Time) []wire.Facts { return nil }}
	if v, err := (PressureWatch{}).Eval(in); err != nil || v.Holds {
		t.Errorf("no samples: %+v %v", v, err)
	}
	if _, err := (PressureWatch{}).Eval(Input{Now: t0}); err == nil {
		t.Error("no 5 s samples (a replay) must be unjudged")
	}
}

// ---- pressure danger ----

func TestPressureDanger_OneSampleAtLevelFour(t *testing.T) {
	for name, c := range map[string]struct {
		f    wire.Facts
		want bool
	}{
		"macOS level 4":         {mac(t0, 4), true},
		"macOS level 2":         {mac(t0, 2), false},
		"macOS level 1":         {mac(t0, 1), false},
		"a failed memory read":  {hole(t0), false},
		"Linux full10 at 10":    {lin(t0, &wire.PSI{Full10: 10}), true},
		"Linux full10 under":    {lin(t0, &wire.PSI{Full10: 9.9}), false},
		"Linux some60 at 25":    {lin(t0, &wire.PSI{Some60: 25}), true},
		"Linux some60 under":    {lin(t0, &wire.PSI{Some60: 24.9}), false},
		"Linux PSI unreadable":  {lin(t0, nil), false},
		"Linux some10 is watch": {lin(t0, &wire.PSI{Some10: 40}), false},
	} {
		v, err := PressureDanger{}.Eval(recentInput(t0, c.f))
		if err != nil || v.Holds != c.want || v.Key != "pressure_danger" || v.Level != wire.AlertPush {
			t.Errorf("%s: %+v %v, want holds=%v", name, v, err, c.want)
		}
	}
}

func TestPressureDanger_IsJudgedAtEverySample(t *testing.T) {
	if !(PressureDanger{}).EverySample() {
		t.Fatal("pressure_danger must be a SampleRule")
	}
	if _, ok := any(PressureWatch{}).(SampleRule); ok {
		t.Error("pressure_watch must not be judged at every sample")
	}
	if _, ok := any(SwapGrowth{}).(SampleRule); ok {
		t.Error("swap_growth must not be judged at every sample")
	}
}

// The look-back is one pass long: a level-4 sample 25 s ago is still news, one 40 s ago was seen by the pass before it.
func TestPressureDanger_LooksBackOnePass(t *testing.T) {
	for age, want := range map[time.Duration]bool{0: true, 25 * time.Second: true, 40 * time.Second: false} {
		recs := run5(t0, 14, func(at time.Time, i int) wire.Facts { return mac(at, 1) })
		now := recs[13].At
		recs = append(recs, mac(now.Add(-age), 4))
		sortByAt(recs)
		v, err := PressureDanger{}.Eval(recentInput(now, recs...))
		if err != nil || v.Holds != want {
			t.Errorf("a level-4 sample %s ago: %+v %v, want holds=%v", age, v, err, want)
		}
	}
}

func TestPressureDanger_ClearsAfterTwoMinutes(t *testing.T) {
	build := func(since time.Duration) []wire.Facts {
		recs := []wire.Facts{mac(t0, 4)}
		for at := t0.Add(5 * time.Second); !at.After(t0.Add(since)); at = at.Add(5 * time.Second) {
			recs = append(recs, mac(at, 1))
		}
		return recs
	}
	for since, want := range map[time.Duration]bool{115 * time.Second: true, 125 * time.Second: false} {
		recs := build(since)
		v, err := PressureDanger{}.Eval(raisedAs(recentInput(recs[len(recs)-1].At, recs...), "pressure_danger"))
		if err != nil || v.Holds != want {
			t.Errorf("last level-4 sample %s ago: %+v %v, want holds=%v", since, v, err, want)
		}
	}
	recs := run5(t0, 30, func(at time.Time, i int) wire.Facts { return hole(at) })
	if _, err := (PressureDanger{}).Eval(raisedAs(recentInput(recs[29].At, recs...), "pressure_danger")); err == nil {
		t.Error("a raised danger was cleared with nothing read for two minutes")
	}
	if _, err := (PressureDanger{}).Eval(Input{Now: t0}); err == nil {
		t.Error("no 5 s samples (a replay) must be unjudged")
	}
}

func sortByAt(recs []wire.Facts) {
	for i := 1; i < len(recs); i++ {
		for j := i; j > 0 && recs[j].At.Before(recs[j-1].At); j-- {
			recs[j], recs[j-1] = recs[j-1], recs[j]
		}
	}
}

// ---- swap growth ----

// swapMinutes is one record a minute for n+1 minutes ending at t0, the swap in use going from start to start+growth in a straight line;
// totalMB is the host's RAM.
func swapMinutes(n int, start, growth, totalMB int) []wire.Facts {
	var out []wire.Facts
	for i := 0; i <= n; i++ {
		f := minute(0, wire.ZoneNormal, start+growth*i/n)
		f.At = t0.Add(-time.Duration(n-i) * time.Minute)
		f.Mem.TotalMB = totalMB
		out = append(out, f)
	}
	return out
}

func TestSwapGrowth_ThresholdByRAMAndOverride(t *testing.T) {
	for name, c := range map[string]struct {
		total, growth, override int
		want                    bool
	}{
		"16 GB at 500":             {16384, 500, 0, true},
		"16 GB at 499":             {16384, 499, 0, false},
		"8 GB uses the 500 floor":  {8192, 500, 0, true},
		"8 GB at 499":              {8192, 499, 0, false},
		"24 GB needs 750":          {24576, 750, 0, true},
		"24 GB at 749":             {24576, 749, 0, false},
		"32 GB needs 1000":         {32768, 1000, 0, true},
		"override 900 raises":      {16384, 900, 900, true},
		"override 900 at 899":      {16384, 899, 900, false},
		"override beats the table": {24576, 300, 300, true},
	} {
		recs := swapMinutes(30, 1000, c.growth, c.total)
		r := SwapGrowth{ThresholdMB: c.override}
		v, err := r.Eval(minutesInput(t0, recs...))
		if err != nil || v.Holds != c.want || v.Key != "swap_growth" || v.Level != wire.AlertWatch {
			t.Errorf("%s: %+v %v, want holds=%v", name, v, err, c.want)
		}
	}
	v, _ := SwapGrowth{}.Eval(minutesInput(t0, swapMinutes(30, 1000, 600, 16384)...))
	if !strings.Contains(v.Detail, "600") {
		t.Errorf("detail = %q", v.Detail)
	}
}

func TestSwapGrowth_ClearsBelowHalfTheThreshold(t *testing.T) {
	for growth, want := range map[int]bool{250: true, 249: false, 400: true, 0: false} {
		recs := swapMinutes(30, 1000, growth, 16384)
		v, err := SwapGrowth{}.Eval(raisedAs(minutesInput(t0, recs...), "swap_growth"))
		if err != nil || v.Holds != want {
			t.Errorf("raised, growth %d: %+v %v, want holds=%v", growth, v, err, want)
		}
	}
	// not raised: half the threshold does not raise
	if v, _ := (SwapGrowth{}).Eval(minutesInput(t0, swapMinutes(30, 1000, 400, 16384)...)); v.Holds {
		t.Error("400 MB raised afresh")
	}
}

func TestSwapGrowth_ShortHistoryAndMissingReadings(t *testing.T) {
	// ten minutes of history cannot show a 30 minute growth: not raised, and a raised alert is left as it was
	short := swapMinutes(10, 1000, 900, 16384)
	if v, err := (SwapGrowth{}).Eval(minutesInput(t0, short...)); err != nil || v.Holds {
		t.Errorf("short history, not raised: %+v %v", v, err)
	}
	if _, err := (SwapGrowth{}).Eval(raisedAs(minutesInput(t0, short...), "swap_growth")); err == nil {
		t.Error("a raised alert was judged on ten minutes of history")
	}
	// a record without a swap reading is skipped, it does not count as 0
	recs := swapMinutes(30, 1000, 600, 16384)
	recs[0].Mem.SwapUsedMB = nil
	recs[1].Mem.SwapUsedMB = nil
	v, err := SwapGrowth{}.Eval(minutesInput(t0, recs...))
	if err != nil || !v.Holds {
		t.Errorf("two unread records at the start: %+v %v", v, err)
	}
	// the newest record is not now's: nothing is judged
	stale := swapMinutes(30, 1000, 900, 16384)
	if v, err := (SwapGrowth{}).Eval(minutesInput(t0.Add(10*time.Minute), stale...)); err != nil || v.Holds {
		t.Errorf("history that ended 10 minutes ago: %+v %v", v, err)
	}
	// swap going down is not growth
	if v, _ := (SwapGrowth{}).Eval(minutesInput(t0, swapMinutes(30, 5000, -2000, 16384)...)); v.Holds {
		t.Error("a falling swap raised")
	}
	if _, err := (SwapGrowth{}).Eval(Input{Now: t0}); err == nil {
		t.Error("no minute history must be unjudged")
	}
}
