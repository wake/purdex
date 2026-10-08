package resources

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var t0 = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// lease builds a lease granted age ago whose tree measures `measured`.
func lease(id string, weight int, age time.Duration, measured float64) Lease {
	return Lease{ID: id, Weight: weight, GrantedAt: t0.Add(-age), Measured: measured, Samples: 5}
}

// D-2: a lease is charged its weight until it has run `warmup`, then the
// larger of its measured average and floor x weight.
func TestCharge_WarmupFloorEWMA(t *testing.T) {
	s := DefaultSettings() // warmup 20 s, floor 50 %
	for name, c := range map[string]struct {
		l    Lease
		want float64
	}{
		"just granted charges the weight":          {lease("a", 45, 0, 0), 45},
		"inside warmup ignores a low measurement":  {lease("a", 45, 19*time.Second+999*time.Millisecond, 3), 45},
		"inside warmup ignores a high measurement": {lease("a", 45, 5*time.Second, 90), 45},
		"at warmup the EWMA takes over":            {lease("a", 45, 20*time.Second, 40), 40},
		"after warmup the floor applies":           {lease("a", 60, time.Minute, 5), 30},
		"after warmup, EWMA above the floor":       {lease("a", 60, time.Minute, 41.5), 41.5},
		"after warmup, EWMA above the weight":      {lease("a", 45, time.Minute, 70), 70},
		"floor is exactly floor x weight":          {lease("a", 45, time.Minute, 22.5), 22.5},
		"an unmeasured lease still pays the floor": {Lease{ID: "a", Weight: 40, GrantedAt: t0.Add(-time.Minute)}, 20},
	} {
		assert.InDelta(t, c.want, Charge(c.l, t0, s), 1e-9, name)
	}

	no := Settings{WarmupS: intp(0), FloorPct: intp(0)}
	assert.InDelta(t, 7, Charge(lease("a", 45, 0, 7), t0, no), 1e-9, "warmup 0 and floor 0: the raw measurement from the start")
	long := Settings{WarmupS: intp(300), FloorPct: intp(100)}
	assert.InDelta(t, 45, Charge(lease("a", 45, 299*time.Second, 7), t0, long), 1e-9)
	assert.InDelta(t, 45, Charge(lease("a", 45, 301*time.Second, 7), t0, long), 1e-9, "floor 100 % charges the weight for ever")
}

// One half-life moves the average halfway to the sample; a first sample is
// taken as it is.
func TestUpdateEWMA_HalfLife(t *testing.T) {
	hl := 15 * time.Second
	assert.InDelta(t, 80, UpdateEWMA(0, 80, 5*time.Second, hl, true), 1e-9, "first sample")
	assert.InDelta(t, 50, UpdateEWMA(0, 100, hl, hl, false), 1e-9, "one half-life: halfway")
	assert.InDelta(t, 75, UpdateEWMA(0, 100, 2*hl, hl, false), 1e-9, "two half-lives: three quarters")
	assert.InDelta(t, 30, UpdateEWMA(50, 10, hl, hl, false), 1e-9, "falling works the same way")
	assert.InDelta(t, 42, UpdateEWMA(42, 100, 0, hl, false), 1e-9, "no time passed: no move")
	assert.InDelta(t, 42, UpdateEWMA(42, 100, -time.Second, hl, false), 1e-9, "a clock step back: no move")
	assert.InDelta(t, 100, UpdateEWMA(42, 100, time.Second, 0, false), 1e-9, "no half-life: the sample")
	assert.InDelta(t, 60, UpdateEWMA(60, 60, 3*time.Second, hl, false), 1e-9, "steady state stays put")
	// A shorter step moves it less than half.
	got := UpdateEWMA(0, 100, 5*time.Second, hl, false)
	assert.Greater(t, got, 0.0)
	assert.Less(t, got, 50.0)
}
