package resources

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v2 defaults (spec "Settings"): every new field reads as its default through
// Effective and through the accessors, including from a zero Settings (a
// stored v1 value with none of the new keys).
func TestSettingsV2_Defaults(t *testing.T) {
	var z Settings
	assert.Equal(t, 80, z.MemWarn())
	assert.Equal(t, 90, z.MemDanger())
	assert.Equal(t, 30*time.Second, z.Gap())
	assert.Equal(t, 120*time.Second, z.TrendWindow())
	assert.Equal(t, 2.0, z.TrendMemPPM())
	assert.Equal(t, 0.0, z.TrendCPUPPM())
	assert.Equal(t, 900*time.Second, z.Park())
	assert.Equal(t, 180*time.Second, z.AlertDanger())
	assert.Equal(t, 600*time.Second, z.AlertStuck())
	assert.Equal(t, 20, z.AlertLeaseMem())
	assert.False(t, z.AlertPushOn())

	e := DefaultSettings()
	for name, p := range map[string]*int{
		"mem_warn_pct": e.MemWarnPct, "mem_danger_pct": e.MemDangerPct, "gap_s": e.GapS,
		"trend_window_s": e.TrendWindowS, "trend_mem_dppm": e.TrendMemDPPM, "trend_cpu_dppm": e.TrendCPUDPPM,
		"park_s": e.ParkS, "alert_danger_s": e.AlertDangerS, "alert_stuck_s": e.AlertStuckS,
		"alert_lease_mem_pct": e.AlertLeaseMemPct, "alert_push": e.AlertPush,
	} {
		require.NotNil(t, p, name)
	}
	assert.Equal(t, []int{80, 90, 30, 120, 20, 0, 900, 180, 600, 20, 0},
		[]int{*e.MemWarnPct, *e.MemDangerPct, *e.GapS, *e.TrendWindowS, *e.TrendMemDPPM, *e.TrendCPUDPPM,
			*e.ParkS, *e.AlertDangerS, *e.AlertStuckS, *e.AlertLeaseMemPct, *e.AlertPush})
	// P1a changes no behaviour: the hold stays 300 until P2a.
	assert.Equal(t, 300, *e.DeadlineS)
}

// v2Field is one new field with its range.
type v2Field struct {
	name   string
	set    func(*Settings, *int)
	get    func(Settings) *int
	lo, hi int
}

var v2Fields = []v2Field{
	{"mem_warn_pct", func(s *Settings, v *int) { s.MemWarnPct = v }, func(s Settings) *int { return s.MemWarnPct }, 50, 95},
	{"mem_danger_pct", func(s *Settings, v *int) { s.MemDangerPct = v }, func(s Settings) *int { return s.MemDangerPct }, 81, 99},
	{"gap_s", func(s *Settings, v *int) { s.GapS = v }, func(s Settings) *int { return s.GapS }, 0, 300},
	{"trend_window_s", func(s *Settings, v *int) { s.TrendWindowS = v }, func(s Settings) *int { return s.TrendWindowS }, 20, 300},
	{"trend_mem_dppm", func(s *Settings, v *int) { s.TrendMemDPPM = v }, func(s Settings) *int { return s.TrendMemDPPM }, 1, 100},
	{"trend_cpu_dppm", func(s *Settings, v *int) { s.TrendCPUDPPM = v }, func(s Settings) *int { return s.TrendCPUDPPM }, 0, 500},
	{"park_s", func(s *Settings, v *int) { s.ParkS = v }, func(s Settings) *int { return s.ParkS }, 60, 3600},
	{"alert_danger_s", func(s *Settings, v *int) { s.AlertDangerS = v }, func(s Settings) *int { return s.AlertDangerS }, 30, 3600},
	{"alert_stuck_s", func(s *Settings, v *int) { s.AlertStuckS = v }, func(s Settings) *int { return s.AlertStuckS }, 60, 7200},
	{"alert_lease_mem_pct", func(s *Settings, v *int) { s.AlertLeaseMemPct = v }, func(s Settings) *int { return s.AlertLeaseMemPct }, 5, 90},
	{"alert_push", func(s *Settings, v *int) { s.AlertPush = v }, func(s Settings) *int { return s.AlertPush }, 0, 1},
}

// Every range edge: the bounds validate, one outside is refused naming the
// field, and Effective clamps it back to the bound. mem_danger_pct's lower
// bound is tested with the default warn (80), so 81 is its floor here.
func TestSettingsV2_RangeEdges(t *testing.T) {
	for _, f := range v2Fields {
		t.Run(f.name, func(t *testing.T) {
			// warn's top (95) needs a danger above it to be a valid pair.
			var base Settings
			if f.name == "mem_warn_pct" {
				base.MemDangerPct = intp(99)
			}
			for _, ok := range []int{f.lo, f.hi} {
				s := base
				f.set(&s, intp(ok))
				require.NoError(t, s.Validate(), "%d", ok)
				assert.Equal(t, ok, *f.get(s.Effective()))
			}
			low, high := base, base
			f.set(&low, intp(f.lo-1))
			f.set(&high, intp(f.hi+1))
			for _, bad := range []Settings{low, high} {
				err := bad.Validate()
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrSettings)
				assert.Contains(t, err.Error(), f.name)
			}
			assert.Equal(t, f.lo, *f.get(low.Effective()))
			assert.Equal(t, f.hi, *f.get(high.Effective()))
		})
	}
}

// mem_danger_pct must be above mem_warn_pct: Validate refuses the pair,
// Effective lifts danger to warn+1.
func TestSettingsV2_DangerAboveWarn(t *testing.T) {
	for _, c := range []struct{ warn, danger int }{{85, 85}, {85, 70}, {95, 90}} {
		s := Settings{MemWarnPct: intp(c.warn), MemDangerPct: intp(c.danger)}
		err := s.Validate()
		require.Error(t, err, "%d/%d", c.warn, c.danger)
		assert.Contains(t, err.Error(), "mem_danger_pct")
		e := s.Effective()
		assert.Equal(t, c.warn, *e.MemWarnPct)
		assert.Equal(t, c.warn+1, *e.MemDangerPct)
		assert.Equal(t, c.warn+1, s.MemDanger())
	}
	ok := Settings{MemWarnPct: intp(85), MemDangerPct: intp(86)}
	require.NoError(t, ok.Validate())
	assert.Equal(t, 86, ok.MemDanger())

	// Only warn raised past the default danger: danger follows it up.
	warnOnly := Settings{MemWarnPct: intp(95)}
	require.Error(t, warnOnly.Validate())
	assert.Equal(t, 96, warnOnly.MemDanger())
	// Only danger set at the default warn.
	dangerOnly := Settings{MemDangerPct: intp(80)}
	require.Error(t, dangerOnly.Validate())
	assert.Equal(t, 81, dangerOnly.MemDanger())
	// A hand-edited warn of 99 clamps to 95 first, then danger is 96.
	hand := Settings{MemWarnPct: intp(99)}
	assert.Equal(t, 95, hand.MemWarn())
	assert.Equal(t, 96, hand.MemDanger())
}

// The derived units: dppm is tenths of a point per minute.
func TestSettingsV2_Accessors(t *testing.T) {
	s := Settings{
		GapS: intp(0), TrendWindowS: intp(120), TrendMemDPPM: intp(25), TrendCPUDPPM: intp(80),
		ParkS: intp(60), AlertDangerS: intp(30), AlertStuckS: intp(61), AlertLeaseMemPct: intp(33), AlertPush: intp(1),
	}
	assert.Equal(t, time.Duration(0), s.Gap(), "0 is a real gap")
	assert.Equal(t, 120*time.Second, s.TrendWindow())
	assert.Equal(t, 2.5, s.TrendMemPPM())
	assert.Equal(t, 8.0, s.TrendCPUPPM())
	assert.Equal(t, time.Minute, s.Park())
	assert.Equal(t, 30*time.Second, s.AlertDanger())
	assert.Equal(t, 61*time.Second, s.AlertStuck())
	assert.Equal(t, 33, s.AlertLeaseMem())
	assert.True(t, s.AlertPushOn())
}
