package resources

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The load ceiling (load_cap_pct, user 2026-10-15): with a lease running and load1 at or above pct/100 x ncpu, the
// head waits with ReasonLoad in the normal and warning zones, whatever the trend. Nothing running still grants one.

func admitLoad(zone Zone, trend Trend, running bool, load Load, s Settings) (string, V2Reason) {
	grant, _, reasons := AdmitV2(zone, trend, running, load, longAgo, []V2Waiter{v2w("a", 5)}, v2Now, s)
	return grant, reasons["a"]
}

func TestAdmitLoad_HoldsAtTheCeilingWhileALeaseRuns(t *testing.T) {
	s := DefaultSettings() // load_cap_pct 100: 10 on a 10 core host
	for _, zone := range []Zone{ZoneNormal, ZoneWarning} {
		for _, trend := range []Trend{TrendFalling, TrendFlat} { // the trend does not matter: these two were granted before
			g, r := admitLoad(zone, trend, true, Load{Load1: 10, NCPU: 10}, s)
			assert.Empty(t, g, "%v %v at the ceiling", zone, trend)
			assert.Equal(t, ReasonLoad, r)
		}
	}
	g, r := admitLoad(ZoneWarning, TrendFlat, true, Load{Load1: 9.99, NCPU: 10}, s)
	assert.Equal(t, "a", g, "just under the ceiling")
	assert.Empty(t, r)
}

// (M: apply the ceiling when nothing is running.)
func TestAdmitLoad_NothingRunningStillGrantsOne(t *testing.T) {
	g, r := admitLoad(ZoneNormal, TrendFlat, false, Load{Load1: 40, NCPU: 10}, DefaultSettings())
	assert.Equal(t, "a", g)
	assert.Empty(t, r)
}

func TestAdmitLoad_ThePercentScalesTheCeilingAndZeroTurnsItOff(t *testing.T) {
	s := DefaultSettings()
	s.LoadCapPct = intp(200)
	g, _ := admitLoad(ZoneNormal, TrendFlat, true, Load{Load1: 19.9, NCPU: 10}, s)
	assert.Equal(t, "a", g, "200 % of 10 cores is 20")
	g, r := admitLoad(ZoneNormal, TrendFlat, true, Load{Load1: 20, NCPU: 10}, s)
	assert.Empty(t, g)
	assert.Equal(t, ReasonLoad, r)
	s.LoadCapPct = intp(0)
	g, _ = admitLoad(ZoneNormal, TrendFlat, true, Load{Load1: 90, NCPU: 10}, s)
	assert.Equal(t, "a", g, "0 turns the ceiling off")
}

// An unknown core count is no ceiling (the zone reads it the same way); danger keeps its own reason; the unknown zone
// still fails open; the gap is read first.
func TestAdmitLoad_DoesNotChangeTheOtherReasons(t *testing.T) {
	s := DefaultSettings()
	g, _ := admitLoad(ZoneNormal, TrendFlat, true, Load{Load1: 90, NCPU: 0}, s)
	assert.Equal(t, "a", g, "ncpu unknown")
	g, r := admitLoad(ZoneDanger, TrendFlat, true, Load{Load1: 90, NCPU: 10}, s)
	assert.Empty(t, g)
	assert.Equal(t, ReasonDanger, r)
	g, _ = admitLoad(ZoneUnknown, TrendUnknown, true, Load{Load1: 90, NCPU: 10}, s)
	assert.Equal(t, "a", g, "unknown zone fails open")
	grant, gapLeft, reasons := AdmitV2(ZoneNormal, TrendFlat, true, Load{Load1: 90, NCPU: 10}, v2Now.Add(-5*time.Second),
		[]V2Waiter{v2w("a", 5)}, v2Now, s)
	assert.Empty(t, grant)
	assert.Positive(t, gapLeft)
	assert.Equal(t, ReasonGap, reasons["a"])
}

// Only the head is judged; the ones behind it are behind.
func TestAdmitLoad_TheRestAreBehind(t *testing.T) {
	_, _, reasons := AdmitV2(ZoneNormal, TrendFlat, true, Load{Load1: 90, NCPU: 10}, longAgo,
		[]V2Waiter{v2w("a", 20), v2w("b", 10)}, v2Now, DefaultSettings())
	assert.Equal(t, ReasonLoad, reasons["a"])
	assert.Equal(t, ReasonBehind, reasons["b"])
}
