package fleet

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

const (
	zU = resources.ZoneUnknown
	zN = resources.ZoneNormal
	zW = resources.ZoneWarning
	zD = resources.ZoneDanger
)

func runDisk(t *testing.T, readings []float64, want []resources.Zone) {
	t.Helper()
	var l DiskLatch
	for i, v := range readings {
		if got := l.Update(v, true); got != want[i] {
			t.Errorf("step %d (%v): got %v, want %v", i, v, got, want[i])
		}
	}
}

func TestDiskLatch_Hysteresis(t *testing.T) {
	runDisk(t, []float64{84, 85, 84, 83, 82}, []resources.Zone{zN, zW, zW, zW, zN})
	runDisk(t, []float64{94, 95, 93, 92, 83, 82.9}, []resources.Zone{zW, zD, zD, zW, zW, zN})
	// Danger can be entered straight from normal, and left straight to normal when the warning line is also clear.
	runDisk(t, []float64{50, 96, 70}, []resources.Zone{zN, zD, zN})
}

func TestDiskLatch_UnknownResets(t *testing.T) {
	var l DiskLatch
	l.Update(90, true)
	if l.Update(0, false) != zU {
		t.Fatal("no reading must be unknown")
	}
	if got := l.Update(84, true); got != zN { // starts again from normal, so 84 does not hold a warning
		t.Fatalf("got %v", got)
	}
}

func TestCombine(t *testing.T) {
	cases := []struct{ res, disk, want resources.Zone }{
		{zN, zN, zN},
		{zN, zW, zW}, {zW, zN, zW}, {zW, zD, zD}, {zD, zW, zD}, {zD, zD, zD},
		{zU, zW, zW}, {zW, zU, zW}, {zU, zD, zD}, {zD, zU, zD}, // a known bad part beats an unknown one
		{zU, zN, zU}, {zN, zU, zU}, {zU, zU, zU},
	}
	for _, c := range cases {
		if got := Combine(c.res, c.disk); got != c.want {
			t.Errorf("Combine(%v, %v) = %v, want %v", c.res, c.disk, got, c.want)
		}
	}
}

func TestProbeLatch(t *testing.T) {
	var p ProbeLatch
	s := resources.Settings{}.Effective()
	warn := float64(s.MemWarn())
	if got := p.Update(Reading{Mem: 10, Load1: 0.1, NCPU: 4}, s); got != zN {
		t.Fatalf("idle: %v", got)
	}
	if got := p.Update(Reading{Mem: warn, Load1: 0.1, NCPU: 4}, s); got != zW {
		t.Fatalf("mem at warn line: %v", got)
	}
	if got := p.Update(Reading{Mem: warn - 1, Load1: 0.1, NCPU: 4}, s); got != zW { // inside the 2-point leave margin
		t.Fatalf("hysteresis: %v", got)
	}
	if got := p.Update(Reading{Mem: 10, Load1: 4, NCPU: 4}, s); got != zW {
		t.Fatalf("load at ncpu: %v", got)
	}
	if got := p.Reset(); got != zU {
		t.Fatalf("reset: %v", got)
	}
	if got := p.Update(Reading{Mem: 10, Load1: 0.1}, s); got != zU { // no cpu count
		t.Fatalf("no ncpu: %v", got)
	}
}

// samples builds n readings spaced step apart, ending at now, memory moving by dMem points per sample.
func samples(now time.Time, n int, step time.Duration, mem0, dMem float64) *resources.Ring {
	var r resources.Ring
	for i := 0; i < n; i++ {
		at := now.Add(-time.Duration(n-1-i) * step)
		r.Add(Sample(Reading{Mem: mem0 + dMem*float64(i), CPU: 10}, at))
	}
	return &r
}

func TestProbeTrend_ThirtySecondSamples(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := resources.Settings{}.Effective()
	// 10 samples over 270 s (>= half the 300 s window), memory +1.5 points per 30 s = +3 points/min (the default limit is 2).
	if got := ProbeTrend(samples(now, 10, 30*time.Second, 40, 1.5), now, s); got != resources.TrendRising {
		t.Errorf("rising: %v", got)
	}
	if got := ProbeTrend(samples(now, 10, 30*time.Second, 60, -1.5), now, s); got != resources.TrendFalling {
		t.Errorf("falling: %v", got)
	}
	if got := ProbeTrend(samples(now, 10, 30*time.Second, 50, 0), now, s); got != resources.TrendFlat {
		t.Errorf("flat: %v", got)
	}
}

func TestProbeTrend_SixtySecondSamplesAreUnknown(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := resources.Settings{}.Effective()
	// Samples 65 s apart put only 5 inside the 300 s window, fewer than the 6 the rule needs. (At exactly 60 s the window's
	// closed start edge catches a sixth, so only a prober that is a little late reads unknown.)
	if got := ProbeTrend(samples(now, 10, 65*time.Second, 40, 3), now, s); got != resources.TrendUnknown {
		t.Errorf("got %v", got)
	}
}

func TestProbeTrend_WindowForcedWhateverTheSettingSays(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	short := 60
	s := resources.Settings{TrendWindowS: &short}.Effective()
	// Under a 60 s window these ten samples would be too few; the forced 300 s window sees all of them.
	if got := ProbeTrend(samples(now, 10, 30*time.Second, 40, 1.5), now, s); got != resources.TrendRising {
		t.Errorf("got %v", got)
	}
	if *s.TrendWindowS != 60 {
		t.Error("the caller's settings must not be modified")
	}
}
