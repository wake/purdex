package advisor

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// fallingInput is inputWith(free, hist) that also says whether the alert is raised now (the evaluator's Raised).
func fallingInput(free float64, hist []wire.Facts, raised bool) Input {
	in := inputWith(free, hist)
	in.Raised = func(key string) bool { return raised && key == "disk_falling" }
	return in
}

// sawtooth is the volume of a host whose Go build cache is trimmed on a timer (#3926): the cache grows 2.5 GB/h, and every 90 minutes a trim
// gives 3.0 GB back, so over a period the volume loses 3.75 - 3.0 = 0.75 GB net, 0.5 GB/h.
// It makes the last 180 minutes ending at the bottom of a tooth (just before a trim), one record a minute.
func sawtooth() []wire.Facts {
	const period, end = 90, 179 // end % period == period-1: the newest record is the bottom of a tooth
	free := func(i int) float64 { return 24 - 2.5*float64(i%period)/60 - 0.75*float64(i/period) }
	var out []wire.Facts
	for i := 0; i <= end; i++ {
		out = append(out, diskFacts(t0.Add(-time.Duration(end-i)*time.Minute), "/Users", free(i)))
	}
	return out
}

// headTail is the rate the rule used to measure: the oldest to the newest record of the last hour.
func headTail(hist []wire.Facts) float64 {
	var first, last wire.Facts
	have := false
	for _, f := range hist {
		if f.At.Before(t0.Add(-time.Hour)) {
			continue
		}
		if !have {
			first, have = f, true
		}
		last = f
	}
	return (first.Disk[1].FreeGB - last.Disk[1].FreeGB) / last.At.Sub(first.At).Hours()
}

// The cache trim's sawtooth does not raise the alert: seen over three hours by least squares the fall is about 1 GB/h, below the 2 GB/h that
// raises it, where the last hour's two ends, taken at the bottom of a tooth, said 2.5 GB/h and would have raised it.
func TestDiskFalling_ASawtoothFromACacheTrimIsNotAFall(t *testing.T) {
	hist := sawtooth()
	now := hist[len(hist)-1].Disk[1].FreeGB // ~19.5 GB
	if old := headTail(hist); old < fallingGBPerHour || now-old*fallingHorizon.Hours() >= fallingFloorGB {
		t.Fatalf("the old head-and-tail rate %.2f at %.1f GB would not have raised it: the case proves nothing", old, now)
	}
	in := fallingInput(now, hist, false)
	rate, ok := fallRate(in, "/Users")
	if !ok || rate < 0.5 || rate > 1.5 {
		t.Fatalf("least-squares rate = %.2f (%v), want about 1 GB/h", rate, ok)
	}
	if v, _ := (DiskFalling{}).Eval(in); v.Holds {
		t.Fatalf("a sawtooth raised the alert: %+v", v)
	}
}

// A steady 3 GB/h from 20 GB over three hours raises it, and the detail says what it was measured over.
func TestDiskFalling_ASteadyFallOverThreeHoursRaises(t *testing.T) {
	v, err := DiskFalling{}.Eval(fallingInput(20, history(180, 29, 3), false))
	if err != nil || !v.Holds {
		t.Fatalf("%+v %v", v, err)
	}
	if !strings.Contains(v.Detail, "3 小時") || !strings.Contains(v.Detail, "3.0") {
		t.Errorf("detail = %q", v.Detail)
	}
}

// The clearing is later than the raising: raised at ≥ 2 GB/h with < 10 GB projected in 6 h, it stays while ≥ 1.5 GB/h and < 12 GB projected.
func TestDiskFalling_ClearsLaterThanItRaises(t *testing.T) {
	for name, c := range map[string]struct {
		free, rate float64
		raised     bool
		want       bool
	}{
		"not raised, 2.1 GB/h from 20 GB raises":               {20, 2.1, false, true},
		"not raised, 1.9 GB/h does not raise":                  {20, 1.9, false, false},
		"not raised, 1.7 GB/h does not raise":                  {20, 1.7, false, false},
		"raised, 1.9 GB/h stays":                               {20, 1.9, true, true},
		"raised, 1.7 GB/h stays (≥ 1.5 and 20-10.2 < 12)":      {20, 1.7, true, true},
		"raised, 1.4 GB/h clears (below 1.5)":                  {20, 1.4, true, false},
		"raised, 1.7 GB/h clears when 6 h ahead is ≥ 12 GB":    {24, 1.7, true, false},
		"raised, 1.7 GB/h at 21.9 GB: 21.9-10.2 = 11.7 stays":  {21.9, 1.7, true, true},
		"raised, 1.7 GB/h at 22.3 GB: 22.3-10.2 = 12.1 clears": {22.3, 1.7, true, false},
	} {
		c := c
		t.Run(name, func(t *testing.T) {
			v, err := DiskFalling{}.Eval(fallingInput(c.free, history(180, c.free+c.rate*3, c.rate), c.raised))
			if err != nil || v.Holds != c.want {
				t.Fatalf("holds = %v (%v), want %v: %+v", v.Holds, err, c.want, v)
			}
		})
	}
}

// A fall hovering around 2 GB/h: once raised, the next pass a little lower does not clear it and raise it again.
func TestDiskFalling_AFallAroundTwoDoesNotFlap(t *testing.T) {
	up, _ := DiskFalling{}.Eval(fallingInput(20, history(180, 20+2.05*3, 2.05), false))
	if !up.Holds {
		t.Fatalf("2.05 GB/h should raise: %+v", up)
	}
	still, _ := DiskFalling{}.Eval(fallingInput(20, history(180, 20+1.9*3, 1.9), true))
	if !still.Holds {
		t.Fatalf("raised, a pass at 1.9 GB/h cleared it: %+v", still)
	}
	if once, _ := (DiskFalling{}).Eval(fallingInput(20, history(180, 20+1.9*3, 1.9), false)); once.Holds {
		t.Fatalf("not raised, 1.9 GB/h raised it: %+v", once)
	}
}

// The free < 14 GB branch is unchanged: it holds with no history, raised or not.
func TestDiskFalling_UnderFourteenHoldsWhateverTheHistory(t *testing.T) {
	for _, raised := range []bool{false, true} {
		if v, _ := (DiskFalling{}).Eval(fallingInput(13.9, nil, raised)); !v.Holds {
			t.Fatalf("raised=%v: %+v", raised, v)
		}
	}
}

// Not enough history is no judgement, raised or not: the rule does not hold up an alert on a slope it cannot measure.
func TestDiskFalling_NotEnoughHistoryIsNoSlopeEvenWhenRaised(t *testing.T) {
	for _, raised := range []bool{false, true} {
		if v, _ := (DiskFalling{}).Eval(fallingInput(20, history(80, 24, 3), raised)); v.Holds {
			t.Fatalf("raised=%v: 80 minutes gave a slope: %+v", raised, v)
		}
	}
}

// 90 minutes and 60 records are the least: a hole that leaves fewer records than that is not a slope, and one that does not is.
func TestDiskFalling_TheSlopeNeedsNinetyMinutesAndSixtyRecords(t *testing.T) {
	long := history(180, 29, 3)
	var every3 []wire.Facts // 61 records over 180 minutes
	for i, f := range long {
		if i%3 == 0 {
			every3 = append(every3, f)
		}
	}
	if len(every3) < slopeMinSamples {
		t.Fatalf("test data has %d records", len(every3))
	}
	if v, _ := (DiskFalling{}).Eval(inputWith(20, every3)); !v.Holds {
		t.Fatalf("%d records over 3 hours is a slope: %+v", len(every3), v)
	}
	var every4 []wire.Facts // 46 records
	for i, f := range long {
		if i%4 == 0 {
			every4 = append(every4, f)
		}
	}
	if v, _ := (DiskFalling{}).Eval(inputWith(20, every4)); v.Holds {
		t.Fatalf("%d records is below the least: %+v", len(every4), v)
	}
}

// One odd record at the edge of the window (a reading from before a big delete) does not make a fall: the line through all the records
// barely moves, where the two ends alone would read 5 GB/h and raise the alert.
func TestDiskFalling_OneOddRecordAtTheEdgeIsNotAFall(t *testing.T) {
	hist := history(180, 20, 0) // flat 20 GB for three hours
	hist[0].Disk[1].FreeGB = 35
	in := fallingInput(20, hist, false)
	if ends := (35.0 - 20.0) / (179.0 / 60); ends < fallingGBPerHour || 20-ends*fallingHorizon.Hours() >= fallingFloorGB {
		t.Fatalf("the two ends (%.1f GB/h) would not have raised it: the case proves nothing", ends)
	}
	if r, ok := fallRate(in, "/Users"); !ok || r > 0.5 {
		t.Fatalf("rate = %.3f (%v), want close to 0", r, ok)
	}
	if v, _ := (DiskFalling{}).Eval(in); v.Holds {
		t.Fatalf("one odd record raised the alert: %+v", v)
	}
}

// The slope is the least-squares one on the real time of the records: a straight line gives its own rate, with or without a little noise, and
// a gap in the middle (the time axis is the clock, not the record number) does not bend it.
func TestFallRate_LeastSquaresOnTheClock(t *testing.T) {
	near := func(got, want, tol float64) bool { return math.Abs(got-want) <= tol }
	line := history(180, 29, 3)
	if r, ok := fallRate(inputWith(20, line), "/Users"); !ok || !near(r, 3, 1e-6) {
		t.Fatalf("a 3 GB/h line gave %.6f (%v)", r, ok)
	}
	noisy := history(180, 29, 3)
	for i := range noisy {
		noisy[i].Disk[1].FreeGB += 0.05 * float64(i%2*2-1) // ±0.05 GB alternating
	}
	if r, ok := fallRate(inputWith(20, noisy), "/Users"); !ok || !near(r, 3, 0.05) {
		t.Fatalf("a noisy 3 GB/h line gave %.4f (%v)", r, ok)
	}
	var gap []wire.Facts // minutes 60..110 of the line are missing: 130 records, still a 3 GB/h line
	for i, f := range line {
		if i < 60 || i > 110 {
			gap = append(gap, f)
		}
	}
	if r, ok := fallRate(inputWith(20, gap), "/Users"); !ok || !near(r, 3, 1e-6) {
		t.Fatalf("a line with a gap gave %.6f (%v)", r, ok)
	}
	rising := history(180, 5, -5)
	if r, ok := fallRate(inputWith(20, rising), "/Users"); !ok || r > -4.9 {
		t.Fatalf("a rising line gave %.4f (%v), want about -5", r, ok)
	}
}
