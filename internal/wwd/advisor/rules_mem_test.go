package advisor

import (
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func ip(v int) *int         { return &v }
func fp(v float64) *float64 { return &v }

// minute is one one-minute record at t0+i minutes: the zone it spent its seconds in (all of them), the swap in use at its end.
func minute(i int, zone wire.Zone, swapMB int) wire.Facts {
	st := wire.MinuteStats{SampledS: 60}
	switch zone {
	case wire.ZoneNormal:
		st.NormalS = 60
	case wire.ZoneWarning:
		st.WarningS = 60
	case wire.ZoneDanger:
		st.DangerS = 60
	}
	return wire.Facts{
		At: t0.Add(time.Duration(i) * time.Minute), Derived: wire.Derived{Zone: zone}, Stats: &st,
		Mem: wire.Mem{TotalMB: 16384, AvailableMB: 6000, SwapUsedMB: ip(swapMB)},
	}
}

func minutesInput(now time.Time, recs ...wire.Facts) Input {
	return Input{
		Now: now, Facts: recs[len(recs)-1],
		Minutes: func(since time.Time) []wire.Facts {
			var out []wire.Facts
			for _, f := range recs {
				if !f.At.Before(since) {
					out = append(out, f)
				}
			}
			return out
		},
	}
}

// ---- pressure held ----

// recsOf builds one-minute records at t0+i minutes: record i spent its minute in zones[i] and ended with swap(i) MB in use. The five
// minutes the rule judges are the last five; the one before them is the baseline whose swap reading starts the window.
func recsOf(zones []wire.Zone, swap func(i int) int) []wire.Facts {
	var recs []wire.Facts
	for i, z := range zones {
		recs = append(recs, minute(i, z, swap(i)))
	}
	return recs
}

// nowOf is half a minute into the minute after the last record, a moment the newest whole minute is 30 s old.
func nowOf(recs []wire.Facts) time.Time { return recs[len(recs)-1].At.Add(30 * time.Second) }

func TestPressureHeld_FiveMinutesAtWarningAndSwapUpAGB(t *testing.T) {
	w, n := wire.ZoneWarning, wire.ZoneNormal
	recs := recsOf([]wire.Zone{n, w, w, w, w, w}, func(i int) int { return 900 + i*250 }) // 900 → 2150: +1250 over five minutes
	v, err := PressureHeld{}.Eval(minutesInput(nowOf(recs), recs...))
	if err != nil || !v.Holds || v.Key != "pressure_held" || v.Level != wire.AlertPush {
		t.Fatalf("%+v %v", v, err)
	}
	if !strings.Contains(v.Detail, "5") {
		t.Errorf("detail = %q", v.Detail)
	}
}

func TestPressureHeld_EachConditionIsNeeded(t *testing.T) {
	w, n, d := wire.ZoneWarning, wire.ZoneNormal, wire.ZoneDanger
	mk := func(zones []wire.Zone, swap func(i int) int) Input {
		recs := recsOf(zones, swap)
		return minutesInput(nowOf(recs), recs...)
	}
	grow := func(i int) int { return 900 + i*300 }
	flat := func(i int) int { return 900 }
	for name, c := range map[string]struct {
		in   Input
		want bool
	}{
		"swap flat":                       {mk([]wire.Zone{n, w, w, w, w, w}, flat), false},
		"swap up only 800 MB in five":     {mk([]wire.Zone{n, w, w, w, w, w}, func(i int) int { return 900 + i*160 }), false},
		"only 4 minutes at warning":       {mk([]wire.Zone{n, n, w, w, w, w}, grow), false},
		"a normal minute in the middle":   {mk([]wire.Zone{n, w, w, n, w, w}, grow), false},
		"danger counts as above":          {mk([]wire.Zone{n, d, w, w, w, d}, grow), true},
		"the baseline minute may be high": {mk([]wire.Zone{w, w, w, w, w, w}, grow), true},
		"seven minutes, the last five":    {mk([]wire.Zone{n, n, w, w, w, w, w}, grow), true},
		// growth that happened in the first minute of the window counts: the window starts at the baseline's reading, not at the first
		// whole minute's own end
		"growth in the first minute": {mk([]wire.Zone{n, w, w, w, w, w}, func(i int) int { return []int{900, 1950, 2000, 2050, 2100, 2150}[i] }), true},
		// growth before the baseline is not in the window
		"growth only before the window": {mk([]wire.Zone{n, w, w, w, w, w}, func(i int) int { return []int{2200, 2200, 2250, 2300, 2350, 2400}[i] }), false},
	} {
		v, err := PressureHeld{}.Eval(c.in)
		if err != nil || v.Holds != c.want {
			t.Errorf("%s: %+v %v", name, v, err)
		}
	}
}

// A minute that was warning for part of it only is not "at warning for the minute": the stats say how many seconds were normal.
func TestPressureHeld_APartlyNormalMinuteBreaksIt(t *testing.T) {
	w, n := wire.ZoneWarning, wire.ZoneNormal
	recs := recsOf([]wire.Zone{n, w, w, w, w, w}, func(i int) int { return 900 + i*300 })
	recs[3].Stats.WarningS, recs[3].Stats.NormalS = 40, 20
	if v, _ := (PressureHeld{}).Eval(minutesInput(nowOf(recs), recs...)); v.Holds {
		t.Fatal("a minute with 20 normal seconds counted as at warning")
	}
}

// Once raised it stands while the zone is still at warning or worse, even if the swap stopped growing; it clears when the zone is normal.
func TestPressureHeld_StaysWhileTheZoneStaysHigh(t *testing.T) {
	var recs []wire.Facts
	for i := 0; i < 8; i++ {
		recs = append(recs, minute(i, wire.ZoneWarning, 2400)) // swap no longer growing
	}
	in := minutesInput(t0.Add(7*time.Minute+30*time.Second), recs...)
	if v, _ := (PressureHeld{}).Eval(in); v.Holds {
		t.Fatal("raised afresh with flat swap")
	}
	in.Raised = func(string) bool { return true }
	if v, _ := (PressureHeld{}).Eval(in); !v.Holds {
		t.Fatal("cleared while the zone is still at warning")
	}
	recs = append(recs, minute(8, wire.ZoneNormal, 2400))
	in = minutesInput(t0.Add(8*time.Minute+30*time.Second), recs...)
	in.Raised = func(string) bool { return true }
	if v, _ := (PressureHeld{}).Eval(in); v.Holds {
		t.Fatal("still held with the zone normal")
	}
}

// Old history, a hole in it, or a missing swap reading (at either end of the window) is not a verdict of "held".
func TestPressureHeld_StaleOrMissingData(t *testing.T) {
	w, n := wire.ZoneWarning, wire.ZoneNormal
	fresh := func() []wire.Facts {
		return recsOf([]wire.Zone{n, w, w, w, w, w}, func(i int) int { return 900 + i*300 })
	}
	recs := fresh()
	if v, _ := (PressureHeld{}).Eval(minutesInput(t0.Add(30*time.Minute), recs...)); v.Holds {
		t.Fatal("history that ended 25 minutes ago counted as now")
	}
	recs = fresh()
	recs[5].Mem.SwapUsedMB = nil
	if v, _ := (PressureHeld{}).Eval(minutesInput(nowOf(recs), recs...)); v.Holds {
		t.Fatal("no swap reading at the end counted as growth")
	}
	recs = fresh()
	recs[0].Mem.SwapUsedMB = nil
	if v, _ := (PressureHeld{}).Eval(minutesInput(nowOf(recs), recs...)); v.Holds {
		t.Fatal("no baseline swap reading counted as growth")
	}
	recs = fresh()
	recs[0].At = recs[0].At.Add(-time.Minute) // a missing minute between the baseline and the window
	if v, _ := (PressureHeld{}).Eval(minutesInput(nowOf(recs), recs...)); v.Holds {
		t.Fatal("a hole between the baseline and the window counted")
	}
	if _, err := (PressureHeld{}).Eval(Input{Now: t0}); err == nil {
		t.Fatal("no history at all is not a verdict")
	}
}

// The minute still being filled does not count toward the five: the alert must not fire before the pressure has lasted five minutes. Five
// whole minutes (and the baseline before them) with a partial newest one are enough; a partial minute inside the five, or a record without
// stats, is not.
func TestPressureHeld_OnlyWholeMinutesStartIt(t *testing.T) {
	w, n := wire.ZoneWarning, wire.ZoneNormal
	whole := func() []wire.Facts {
		return recsOf([]wire.Zone{n, w, w, w, w, w}, func(i int) int { return 900 + i*300 })
	}
	partial := minute(6, w, 2700)
	partial.Stats.SampledS, partial.Stats.WarningS = 20, 20 // the open minute, 20 s in
	now := t0.Add(6*time.Minute + 20*time.Second)
	if v, _ := (PressureHeld{}).Eval(minutesInput(now, append(whole(), partial)...)); !v.Holds {
		t.Fatal("five whole minutes, their baseline and an open one should hold")
	}
	if v, _ := (PressureHeld{}).Eval(minutesInput(now, append(whole()[1:], partial)...)); v.Holds {
		t.Fatal("four whole minutes after the baseline and an open one are only four minutes: it fired early")
	}
	short := whole()
	short[3] = minute(3, w, 1800)
	short[3].Stats.SampledS, short[3].Stats.WarningS = 35, 35 // the sampler failed for 25 s of it
	if v, _ := (PressureHeld{}).Eval(minutesInput(nowOf(short), short...)); v.Holds {
		t.Fatal("a minute that lost 25 s of samples counted as a whole minute")
	}
	nostats := whole()
	nostats[2].Stats = nil
	if v, _ := (PressureHeld{}).Eval(minutesInput(nowOf(nostats), nostats...)); v.Holds {
		t.Fatal("a minute with no stats counted as a whole minute")
	}
}

// A raised alert is kept up by whole minutes, not by the open one: a minute that lost its samples proves nothing about the pressure, and
// the open minute only ends it (a normal second in it) and never prolongs it.
func TestPressureHeld_AMinuteWithoutSamplesDoesNotKeepItUp(t *testing.T) {
	raised := func(recs ...wire.Facts) Input {
		in := minutesInput(t0.Add(time.Duration(len(recs)-1)*time.Minute+40*time.Second), recs...)
		in.Raised = func(string) bool { return true }
		return in
	}
	var recs []wire.Facts
	for i := 0; i < 5; i++ {
		recs = append(recs, minute(i, wire.ZoneWarning, 2400))
	}
	// the open minute: 5 s in, one warning sample; no normal second
	open := minute(5, wire.ZoneWarning, 2400)
	open.Stats.SampledS, open.Stats.WarningS = 5, 5
	if v, _ := (PressureHeld{}).Eval(raised(append(recs, open)...)); !v.Holds {
		t.Fatal("the last whole minute was high and the open one is not normal: it should stand")
	}
	// a normal second in the open minute ends it at once
	open.Stats.NormalS, open.Stats.SampledS = 5, 10
	if v, _ := (PressureHeld{}).Eval(raised(append(recs, open)...)); v.Holds {
		t.Fatal("a normal second in the open minute did not end it")
	}
	// the minutes before lost their samples and no whole minute is recent: nothing keeps it up
	lost := minute(5, wire.ZoneWarning, 2400)
	lost.Stats.SampledS, lost.Stats.WarningS = 5, 5
	old := recs[:3] // whole minutes at 0..2, then nothing whole until the lost one
	in := minutesInput(t0.Add(5*time.Minute+30*time.Second), append(old, lost)...)
	in.Raised = func(string) bool { return true }
	if v, _ := (PressureHeld{}).Eval(in); v.Holds {
		t.Fatal("a minute that lost its samples kept the alert up")
	}
}

// ---- thrash ----

func thrashSample(at time.Time, compressorMB int, rate float64) wire.Facts {
	return wire.Facts{At: at, Mem: wire.Mem{TotalMB: 16384, CompressorMB: ip(compressorMB), DecompressRate: fp(rate)}}
}

func samplesInput(now time.Time, recs ...wire.Facts) Input {
	in := Input{Now: now, Facts: recs[len(recs)-1]}
	in.Recent = func(since time.Time) []wire.Facts {
		var out []wire.Facts
		for _, f := range recs {
			if !f.At.Before(since) {
				out = append(out, f)
			}
		}
		return out
	}
	return in
}

func every5s(from time.Time, n, compressorMB int, rate float64) []wire.Facts {
	var out []wire.Facts
	for i := 0; i < n; i++ {
		out = append(out, thrashSample(from.Add(time.Duration(i)*5*time.Second), compressorMB, rate))
	}
	return out
}

func TestThrash_CompressorOverFourGiBAndDecompressingFastForAMinute(t *testing.T) {
	recs := every5s(t0, 13, 4300, 95000) // 60 s of samples, 12 intervals
	v, err := Thrash{}.Eval(samplesInput(t0.Add(60*time.Second), recs...))
	if err != nil || !v.Holds || v.Key != "thrash" || v.Level != wire.AlertDigest {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestThrash_EachConditionIsNeeded(t *testing.T) {
	now := t0.Add(60 * time.Second)
	for name, c := range map[string]struct {
		recs []wire.Facts
		want bool
	}{
		"compressor at 4096 is not over": {every5s(t0, 13, 4096, 95000), false},
		"rate at 80000 is not over":      {every5s(t0, 13, 4300, 80000), false},
		"only 40 s of it":                {append(every5s(t0, 5, 4300, 10), every5s(t0.Add(25*time.Second), 8, 4300, 95000)...), false},
		"one slow sample in the minute":  {append(every5s(t0, 6, 4300, 95000), append([]wire.Facts{thrashSample(t0.Add(30*time.Second), 4300, 1000)}, every5s(t0.Add(35*time.Second), 6, 4300, 95000)...)...), false},
	} {
		v, err := Thrash{}.Eval(samplesInput(now, c.recs...))
		if err != nil || v.Holds != c.want {
			t.Errorf("%s: %+v %v", name, v, err)
		}
	}
}

// Two high samples a minute apart, or one hole in the middle, are not a minute of thrashing: the span between the ends is not enough.
func TestThrash_AHoleBetweenSamplesIsNotContinuous(t *testing.T) {
	now := t0.Add(60 * time.Second)
	holed := append(every5s(t0, 3, 4300, 95000), every5s(t0.Add(45*time.Second), 4, 4300, 95000)...) // 10 s … 45 s missing
	if v, _ := (Thrash{}).Eval(samplesInput(now, holed...)); v.Holds {
		t.Fatalf("a 30 s hole counted: %+v", v)
	}
	ends := []wire.Facts{thrashSample(t0, 4300, 95000), thrashSample(t0.Add(60*time.Second), 4300, 95000)}
	if v, _ := (Thrash{}).Eval(samplesInput(now, ends...)); v.Holds {
		t.Fatalf("two samples a minute apart counted: %+v", v)
	}
	oneMissed := every5s(t0, 13, 4300, 95000)
	oneMissed = append(oneMissed[:5], oneMissed[6:]...) // one sample lost: a 10 s step is still fine
	if v, _ := (Thrash{}).Eval(samplesInput(now, oneMissed...)); !v.Holds {
		t.Fatal("one lost sample should not break it")
	}
}

// Where decompress_rate is not read (Linux, or a record from before the proto 2 writers) the rule does not apply: no hold, no error.
func TestThrash_NoDecompressRateIsNotThrash(t *testing.T) {
	recs := every5s(t0, 13, 4300, 95000)
	for i := range recs {
		recs[i].Mem.DecompressRate = nil
	}
	v, err := Thrash{}.Eval(samplesInput(t0.Add(60*time.Second), recs...))
	if err != nil || v.Holds {
		t.Fatalf("%+v %v", v, err)
	}
}

// Without the 5 s samples (a replay from the minute rollups) thrash is unjudged, never an estimate: a rollup keeps each field's own peak, so
// a compressor peak and a decompress peak that never shared a sample would read as thrashing, and it says nothing of how long.
func TestThrash_WithoutSamplesIsUnjudged(t *testing.T) {
	f := minute(0, wire.ZoneNormal, 0)
	f.Mem.CompressorMB, f.Mem.DecompressRate = ip(4300), fp(95000)
	if _, err := (Thrash{}).Eval(minutesInput(t0.Add(30*time.Second), f)); err == nil {
		t.Fatal("a minute rollup was taken for a minute of thrashing")
	}
}

// On macOS a sample with no decompress_rate means the writer gate is shut (the field is a proto 2 one): that is unjudged, which leaves a
// raised alert as it was, not "not thrashing", which would clear it. On Linux the field is never read, and the rule does not apply.
func TestThrash_NoDecompressRateIsUnjudgedOnMacAndNotApplicableOnLinux(t *testing.T) {
	recs := every5s(t0, 13, 4300, 95000)
	for i := range recs {
		recs[i].Mem.DecompressRate = nil
	}
	in := samplesInput(t0.Add(60*time.Second), recs...)
	in.Facts.Platform = "darwin"
	if _, err := (Thrash{}).Eval(in); err == nil {
		t.Fatal("a macOS sample without decompress_rate was read as not thrashing")
	}
	in.Facts.Platform = "linux"
	if v, err := (Thrash{}).Eval(in); err != nil || v.Holds {
		t.Fatalf("linux: %+v %v", v, err)
	}
}

// ---- resident growth ----

func procFacts(procs ...wire.Proc) wire.Facts {
	return wire.Facts{
		At: t0, Mem: wire.Mem{TotalMB: 16384, AvailableMB: 6000, SwapUsedMB: ip(900)}, Procs: procs,
	}
}

func resident(name string, mb int, kind wire.OwnerKind) wire.Proc {
	return wire.Proc{PID: 7, Name: name, MemMB: mb, MemKind: "footprint", Owner: &wire.ProcOwner{Kind: kind}}
}

func TestResidentGrowth_OneSystemOrResidentProcessOverMaxOf4GBAnd25Percent(t *testing.T) {
	in := Input{Now: t0, Facts: procFacts(resident("avconferenced", 4200, wire.OwnerSystem), resident("node", 9000, wire.OwnerSession))}
	v, err := ResidentGrowth{}.Eval(in)
	if err != nil || !v.Holds || v.Key != "resident_growth" || v.Level != wire.AlertDigest || !strings.Contains(v.Detail, "avconferenced") {
		t.Fatalf("%+v %v", v, err)
	}
	// 4096 on a 16 GB host is both 4 GB and 25 %: the limit is ≥
	if v, _ := (ResidentGrowth{}).Eval(Input{Now: t0, Facts: procFacts(resident("x", 4096, wire.OwnerResident))}); !v.Holds {
		t.Fatal("exactly the limit holds")
	}
	if v, _ := (ResidentGrowth{}).Eval(Input{Now: t0, Facts: procFacts(resident("x", 4095, wire.OwnerResident))}); v.Holds {
		t.Fatal("just under holds")
	}
	// a session's or a job's own process is not this rule's: only system and resident
	if v, _ := (ResidentGrowth{}).Eval(Input{Now: t0, Facts: procFacts(resident("node", 9000, wire.OwnerSession), resident("go", 9000, wire.OwnerJob))}); v.Holds {
		t.Fatal("a session or job process held")
	}
}

// 25 % of a big host is more than 4 GB.
func TestResidentGrowth_TheLimitScalesWithRAM(t *testing.T) {
	f := procFacts(resident("x", 6000, wire.OwnerSystem))
	f.Mem.TotalMB = 32768 // 25 % = 8192
	if v, _ := (ResidentGrowth{}).Eval(Input{Now: t0, Facts: f}); v.Holds {
		t.Fatal("6000 MB on a 32 GB host is under 25 %")
	}
	f.Procs[0].MemMB = 8192
	if v, _ := (ResidentGrowth{}).Eval(Input{Now: t0, Facts: f}); !v.Holds {
		t.Fatal("8192 MB on a 32 GB host is the limit")
	}
}

// A single process by its own footprint, never a sum over a group: ten 1 GB helpers are not one 10 GB resident.
func TestResidentGrowth_NeverASum(t *testing.T) {
	var ps []wire.Proc
	for i := 0; i < 10; i++ {
		ps = append(ps, resident("Helper", 1000, wire.OwnerResident))
	}
	if v, _ := (ResidentGrowth{}).Eval(Input{Now: t0, Facts: procFacts(ps...)}); v.Holds {
		t.Fatal("a sum over a group held")
	}
}

func TestResidentGrowth_TheSimulatorIsExcluded(t *testing.T) {
	for _, name := range []string{"SimLaunchHost-arm64", "launchd_sim", "Simulator", "com.apple.CoreSimulator.CoreSimulatorService"} {
		if v, _ := (ResidentGrowth{}).Eval(Input{Now: t0, Facts: procFacts(resident(name, 9000, wire.OwnerResident))}); v.Holds {
			t.Errorf("%s held", name)
		}
	}
}

// A known resident is not reported while the host has ≥ 50 % memory available and swap did not grow in the window; otherwise it stands.
func TestResidentGrowth_KnownResidentSuppression(t *testing.T) {
	rule := ResidentGrowth{Known: []string{"OrbStack Helper"}}
	mk := func(avail int, swapFrom, swapTo int) Input {
		f := procFacts(resident("OrbStack Helper", 5000, wire.OwnerResident))
		f.Mem.AvailableMB = avail
		f.Mem.SwapUsedMB = ip(swapTo)
		start := minute(0, wire.ZoneNormal, swapFrom)
		start.At = t0.Add(-suppressWindow)
		return Input{Now: t0, Facts: f, Minutes: func(time.Time) []wire.Facts { return []wire.Facts{start} }}
	}
	for name, c := range map[string]struct {
		in   Input
		want bool
	}{
		"plenty of memory, swap flat: suppressed": {mk(9000, 900, 900), false},
		"swap shrank: suppressed":                 {mk(9000, 900, 800), false},
		"swap grew: it stands":                    {mk(9000, 900, 1000), true},
		"under 50 % available: it stands":         {mk(8000, 900, 900), true},
	} {
		v, err := rule.Eval(c.in)
		if err != nil || v.Holds != c.want {
			t.Errorf("%s: %+v %v", name, v, err)
		}
	}
	// an unknown process is never suppressed
	f := procFacts(resident("avconferenced", 5000, wire.OwnerSystem))
	f.Mem.AvailableMB = 9000
	if v, _ := rule.Eval(Input{Now: t0, Facts: f}); !v.Holds {
		t.Fatal("an unknown resident was suppressed")
	}
	// the swap trend cannot be read (no history): the suppression condition is not met, so the item stands
	f = procFacts(resident("OrbStack Helper", 5000, wire.OwnerResident))
	f.Mem.AvailableMB = 9000
	if v, _ := rule.Eval(Input{Now: t0, Facts: f}); !v.Holds {
		t.Fatal("suppressed without being able to tell that swap did not grow")
	}
}

// The suppression needs history reaching back the whole window: a baseline from a few minutes ago (a fresh start, a hole) shows nothing
// about the ten minutes, so the known resident is reported.
func TestResidentGrowth_KnownResidentNeedsTheWholeWindowOfHistory(t *testing.T) {
	rule := ResidentGrowth{Known: []string{"OrbStack Helper"}}
	f := procFacts(resident("OrbStack Helper", 5000, wire.OwnerResident))
	f.Mem.AvailableMB = 9000
	baseline := func(age time.Duration) Input {
		b := minute(0, wire.ZoneNormal, 900)
		b.At = t0.Add(-age)
		return Input{Now: t0, Facts: f, Minutes: func(time.Time) []wire.Facts { return []wire.Facts{b} }}
	}
	if v, _ := rule.Eval(baseline(suppressWindow)); v.Holds {
		t.Fatal("a baseline from the start of the window did not suppress")
	}
	if v, _ := rule.Eval(baseline(suppressWindow - time.Minute)); v.Holds {
		t.Fatal("a baseline within the slack did not suppress")
	}
	if v, _ := rule.Eval(baseline(3 * time.Minute)); !v.Holds {
		t.Fatal("a baseline from 3 minutes ago suppressed a 10-minute question")
	}
}

func TestResidentGrowth_TheLargestOneIsNamed(t *testing.T) {
	f := procFacts(resident("a", 4500, wire.OwnerSystem), resident("b", 7000, wire.OwnerResident))
	v, _ := ResidentGrowth{}.Eval(Input{Now: t0, Facts: f})
	if !v.Holds || !strings.Contains(v.Detail, "b") || !strings.Contains(v.Detail, "7000") {
		t.Fatalf("%+v", v)
	}
}
