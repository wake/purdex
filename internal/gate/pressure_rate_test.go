package gate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// RateOver is the arithmetic of the gate's admission rate, shared with the daemon's zone.
func TestRateOver(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	cur := PageSample{At: at(10), Total: 1000}

	// the oldest sample inside the 10 s window, at least a second old, is the baseline
	assert.InDelta(t, 50, RateOver([]PageSample{{At: at(0), Total: 500}, {At: at(5), Total: 800}}, cur), 0.001)
	// nothing a second or more behind, or nothing inside the window: unknown, taken as 0
	assert.Equal(t, 0.0, RateOver(nil, cur))
	assert.Equal(t, 0.0, RateOver([]PageSample{{At: at(9.5), Total: 0}}, cur))
	assert.Equal(t, 0.0, RateOver([]PageSample{{At: at(-5), Total: 0}}, cur))
	// a counter that went back (a reboot between samples) is 0, not a huge unsigned wrap
	assert.Equal(t, 0.0, RateOver([]PageSample{{At: at(2), Total: 5000}}, cur))
	// the sample of the same instant is not a baseline
	assert.Equal(t, 0.0, RateOver([]PageSample{cur}, cur))
}

// pressureOf (the cache's reading) and RateOver agree.
func TestPressureOf_UsesRateOver(t *testing.T) {
	now := time.Unix(2000, 0)
	cur := cachedSample{At: now.UnixMilli(), Level: 2, Pageouts: 600, Swapouts: 100}
	old := cachedSample{At: now.Add(-8 * time.Second).UnixMilli(), Level: 1, Pageouts: 100, Swapouts: 100}
	p := pressureOf([]cachedSample{old, cur}, cur)
	assert.Equal(t, 2, p.Level)
	assert.InDelta(t, 62.5, p.PageRate, 0.001) // (700 - 200) pages over 8 s
}
