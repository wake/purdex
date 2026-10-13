package resources

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The defaults after G1 (lead's decision of 2026-10-13): a 120 s window, memory
// slope 2.0 points per minute, cpu out of the direction. (M: put the old
// defaults back and the 1.9 case is rising.)
func TestTrendOf_DefaultsAfterG1(t *testing.T) {
	d := DefaultSettings()
	assert.Equal(t, 120, int(d.TrendWindow().Seconds()))
	assert.Equal(t, 2.0, d.TrendMemPPM())
	assert.Equal(t, 0.0, d.TrendCPUPPM())
	const n = 25 // 120 s of 5 s samples
	for _, c := range []struct {
		name string
		data []TrendSample
		want Trend
	}{
		{"mem +1.9/min is flat", series(n, lineF(50, 1.9), constF(30), pressureF(1)), TrendFlat},
		{"mem +2.2/min is rising", series(n, lineF(50, 2.2), constF(30), pressureF(1)), TrendRising},
		{"mem -2.2/min is falling", series(n, lineF(60, -2.2), constF(30), pressureF(1)), TrendFalling},
		{"cpu +60/min is flat", series(n, constF(50), lineF(30, 60), pressureF(1)), TrendFlat},
		{"cpu -60/min is flat", series(n, constF(50), lineF(400, -60), pressureF(1)), TrendFlat},
	} {
		assert.Equal(t, c.want, TrendOf(c.data, lastAt(c.data), d), c.name)
	}
	// A 60 s span is half the window: 13 samples are enough, 12 are not.
	assert.Equal(t, TrendRising, TrendOf(series(13, lineF(50, 4), constF(30), pressureF(1)), trendT0.Add(60*time.Second), d))
	assert.Equal(t, TrendUnknown, TrendOf(series(12, lineF(50, 4), constF(30), pressureF(1)), trendT0.Add(55*time.Second), d))
}
