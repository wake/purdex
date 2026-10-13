package resources

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var trendT0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

// series builds n samples 5 s apart ending at trendT0 + (n-1)*5s. mem and cpu
// are functions of the minute offset from the first sample; pressure is a
// function of the index.
func series(n int, mem, cpu func(min float64) float64, pressure func(i int) int) []TrendSample {
	out := make([]TrendSample, n)
	for i := range out {
		at := trendT0.Add(time.Duration(i) * 5 * time.Second)
		m := at.Sub(trendT0).Minutes()
		out[i] = TrendSample{At: at, Mem: mem(m), CPU: cpu(m), Pressure: pressure(i)}
	}
	return out
}

func constF(v float64) func(float64) float64 { return func(float64) float64 { return v } }
func lineF(base, perMin float64) func(float64) float64 {
	return func(m float64) float64 { return base + perMin*m }
}
func pressureF(p int) func(int) int    { return func(int) int { return p } }
func lastAt(s []TrendSample) time.Time { return s[len(s)-1].At }

func TestTrend_String(t *testing.T) {
	assert.Equal(t, "unknown", TrendUnknown.String())
	assert.Equal(t, "falling", TrendFalling.String())
	assert.Equal(t, "flat", TrendFlat.String())
	assert.Equal(t, "rising", TrendRising.String())
	assert.Equal(t, "unknown", Trend(99).String())
}

// 13 samples cover the default 60 s window.
const windowN = 13

func TestTrendOf_Directions(t *testing.T) {
	s := DefaultSettings()
	// noisy builds a flat series with +-0.3 alternating noise.
	noisy := func(base float64) func(float64) float64 {
		i := 0
		return func(float64) float64 {
			i++
			if i%2 == 0 {
				return base + 0.3
			}
			return base - 0.3
		}
	}
	for _, c := range []struct {
		name string
		data []TrendSample
		want Trend
	}{
		{"flat noise", series(windowN, noisy(50), noisy(30), pressureF(1)), TrendFlat},
		{"mem +2/min", series(windowN, lineF(50, 2), constF(30), pressureF(1)), TrendRising},
		{"mem -2/min", series(windowN, lineF(50, -2), constF(30), pressureF(1)), TrendFalling},
		{"mem just under the threshold", series(windowN, lineF(50, 0.9), constF(30), pressureF(1)), TrendFlat},
		{"mem just over the threshold", series(windowN, lineF(50, 1.1), constF(30), pressureF(1)), TrendRising},
		{"mem -0.9/min is flat", series(windowN, lineF(50, -0.9), constF(30), pressureF(1)), TrendFlat},
		{"cpu +8/min", series(windowN, constF(50), lineF(30, 8), pressureF(1)), TrendRising},
		{"cpu -8/min", series(windowN, constF(50), lineF(60, -8), pressureF(1)), TrendFalling},
		{"cpu +4/min is flat", series(windowN, constF(50), lineF(30, 4), pressureF(1)), TrendFlat},
		{"mem falling while cpu rising is rising", series(windowN, lineF(60, -2), lineF(30, 8), pressureF(1)), TrendRising},
		{"mem rising while cpu falling is rising", series(windowN, lineF(50, 2), lineF(60, -8), pressureF(1)), TrendRising},
		{"pressure 1 to 2 with flat numbers is rising", series(windowN, constF(50), constF(30),
			func(i int) int {
				if i < windowN/2 {
					return 1
				}
				return 2
			}), TrendRising},
		{"pressure 2 to 1 with flat numbers is falling", series(windowN, constF(50), constF(30),
			func(i int) int {
				if i < windowN/2 {
					return 2
				}
				return 1
			}), TrendFalling},
		{"pressure falls but mem rises is rising", series(windowN, lineF(50, 2), constF(30),
			func(i int) int {
				if i < 3 {
					return 2
				}
				return 1
			}), TrendRising},
		{"constant high pressure is flat", series(windowN, constF(50), constF(30), pressureF(4)), TrendFlat},
	} {
		assert.Equal(t, c.want, TrendOf(c.data, lastAt(c.data), s), c.name)
	}
}

// Pressure compares the first and last sample of the window, not the peak.
func TestTrendOf_PressureIsFirstVersusLast(t *testing.T) {
	data := series(windowN, constF(50), constF(30), func(i int) int {
		if i == 6 { // a spike in the middle only
			return 4
		}
		return 1
	})
	assert.Equal(t, TrendFlat, TrendOf(data, lastAt(data), DefaultSettings()))
}

func TestTrendOf_Unknown(t *testing.T) {
	s := DefaultSettings()
	// Five samples are too few, however clear the slope.
	five := series(5, lineF(50, 30), constF(30), pressureF(1))
	assert.Equal(t, TrendUnknown, TrendOf(five, lastAt(five), s))
	// Six samples in a span shorter than half the window (6 x 5 s = 25 s < 30 s).
	six := series(6, lineF(50, 30), constF(30), pressureF(1))
	assert.Equal(t, TrendUnknown, TrendOf(six, lastAt(six), s))
	// Seven samples span 30 s: exactly half the window is enough.
	seven := series(7, lineF(50, 30), constF(30), pressureF(1))
	assert.Equal(t, TrendRising, TrendOf(seven, lastAt(seven), s))
	assert.Equal(t, TrendUnknown, TrendOf(nil, trendT0, s))
	// Six samples that arrive 10 s apart do span enough.
	wide := make([]TrendSample, 6)
	for i := range wide {
		wide[i] = TrendSample{At: trendT0.Add(time.Duration(i) * 10 * time.Second), Mem: 50 + float64(i)*5, Pressure: 1}
	}
	assert.Equal(t, TrendRising, TrendOf(wide, lastAt(wide), s))
}

// Samples older than the window are ignored: a steep climb that ended before
// the window began does not make a flat window rising, and a quiet start does
// not hide a climb at the end.
func TestTrendOf_IgnoresSamplesOutsideTheWindow(t *testing.T) {
	s := DefaultSettings()
	old := series(20, lineF(10, 20), constF(30), pressureF(1)) // 100 s of climbing
	flat := make([]TrendSample, 0, 40)
	flat = append(flat, old...)
	end := lastAt(old)
	for i := 1; i <= windowN; i++ {
		flat = append(flat, TrendSample{At: end.Add(time.Duration(i) * 5 * time.Second), Mem: 60, CPU: 30, Pressure: 1})
	}
	assert.Equal(t, TrendFlat, TrendOf(flat, lastAt(flat), s))

	quiet := series(30, func(m float64) float64 {
		if m < 1.5 {
			return 40
		}
		return 40 + (m-1.5)*20
	}, constF(30), pressureF(1))
	assert.Equal(t, TrendRising, TrendOf(quiet, lastAt(quiet), s))

	// "now" later than the newest sample shrinks the window to what is left.
	data := series(windowN, lineF(50, 2), constF(30), pressureF(1))
	assert.Equal(t, TrendUnknown, TrendOf(data, lastAt(data).Add(45*time.Second), s), "a stale series has no samples to judge")
}

// The window and the thresholds come from the settings.
func TestTrendOf_UsesSettings(t *testing.T) {
	data := series(windowN, lineF(50, 2), constF(30), pressureF(1))
	loose := Settings{TrendMemDPPM: intp(30)}.Effective() // 3.0/min
	assert.Equal(t, TrendFlat, TrendOf(data, lastAt(data), loose))
	cpu := series(windowN, constF(50), lineF(30, 8), pressureF(1))
	assert.Equal(t, TrendFlat, TrendOf(cpu, lastAt(cpu), Settings{TrendCPUDPPM: intp(100)}.Effective()))

	// A 120 s window needs a 60 s span; 13 samples (60 s) are enough, 12 are not.
	wide := Settings{TrendWindowS: intp(120)}.Effective()
	assert.Equal(t, TrendRising, TrendOf(data, lastAt(data), wide))
	short := series(12, lineF(50, 2), constF(30), pressureF(1))
	assert.Equal(t, TrendUnknown, TrendOf(short, lastAt(short), wide))
}

func TestTrendOf_SlopeIsLeastSquares(t *testing.T) {
	// A single outlier at the end would fool "last minus first" but not the
	// fitted line: first 50, last 52 over 60 s is +2/min by endpoints, yet the
	// fit through a flat series with one high end sample stays under 1.0.
	data := series(windowN, constF(50), constF(30), pressureF(1))
	data[len(data)-1].Mem = 52
	assert.Equal(t, TrendFlat, TrendOf(data, lastAt(data), DefaultSettings()))
}

func TestRing_AddSinceAllAndPrune(t *testing.T) {
	var r Ring
	assert.Empty(t, r.All())
	for i := 0; i < 130; i++ { // 650 s
		r.Add(TrendSample{At: trendT0.Add(time.Duration(i) * 5 * time.Second), Mem: float64(i)})
	}
	all := r.All()
	newest := trendT0.Add(129 * 5 * time.Second)
	assert.Equal(t, newest, all[len(all)-1].At)
	// Nothing older than 10 min before the newest is kept; the boundary sample is.
	assert.False(t, all[0].At.Before(newest.Add(-10*time.Minute)))
	assert.Equal(t, newest.Add(-10*time.Minute), all[0].At)
	assert.Len(t, all, 121)

	since := r.Since(newest.Add(-30 * time.Second))
	assert.Len(t, since, 7)
	assert.Equal(t, newest.Add(-30*time.Second), since[0].At)

	// Readers get copies.
	all[0].Mem = -1
	since[0].Mem = -1
	assert.NotEqual(t, -1.0, r.All()[0].Mem)
	assert.NotEqual(t, -1.0, r.Since(newest.Add(-30 * time.Second))[0].Mem)
}
