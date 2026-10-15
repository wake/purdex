package gate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// pressureOf (the cache's reading) and RateOver agree.
func TestPressureOf_UsesRateOver(t *testing.T) {
	now := time.Unix(2000, 0)
	cur := cachedSample{At: now.UnixMilli(), Level: 2, Pageouts: 600, Swapouts: 100}
	old := cachedSample{At: now.Add(-8 * time.Second).UnixMilli(), Level: 1, Pageouts: 100, Swapouts: 100}
	p := pressureOf([]cachedSample{old, cur}, cur)
	assert.Equal(t, 2, p.Level)
	assert.InDelta(t, 62.5, p.PageRate, 0.001) // (700 - 200) pages over 8 s
}
