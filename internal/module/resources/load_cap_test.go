package resourcesmod

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// setLoad publishes a host sample with the given load1 on 10 cores (the zone is normal, the trend flat).
func (f *passFix) setLoad(load1 float64) {
	f.setHost(resources.HostUse{Measured: 10, NCPU: 10, Load1: load1, Zone: "normal", Trend: "flat"})
}

// The load ceiling end to end (user 2026-10-15): with a lease running, load1 at the ceiling holds the next request in
// the normal zone with a flat trend, and the time is charged to the reason `load`; below it the request is granted.
// (M: drop the ceiling and the first assertion is red; charge another reason and the last one is.)
func TestPassLoadCap_HoldsTheNextRequestWhileALeaseRuns(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.heldLease("h", resources.ScopeProcess, 100) // a lease is running
	f.queue("a", 35, 5*time.Minute)
	f.setLoad(10) // the default ceiling: 1.0 x 10 cores
	f.pass()
	assert.Equal(t, "waiting", f.state("a"), "load1 at the ceiling")
	f.advance(10 * time.Second)
	f.m.store.RenewLease("a", f.nowMS()+30000)
	f.pass()
	assert.Equal(t, "waiting", f.state("a"))
	f.setLoad(9.5)
	f.pass()
	require.Equal(t, "held", f.state("a"), "below the ceiling")
	assert.Equal(t, map[string]int64{"load": 10000}, f.waitReasons("a"))
}

// Nothing running: one is let in even when the base load is above the ceiling, or a host that always sits above it
// would never run a heavy command. (M: apply the ceiling when nothing runs.)
func TestPassLoadCap_NothingRunningStillGrantsOne(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.queue("a", 35, 5*time.Minute)
	f.setLoad(36)
	f.pass()
	assert.Equal(t, "held", f.state("a"))
	f.queue("b", 35, 5*time.Minute)
	f.pass()
	assert.Equal(t, "waiting", f.state("b"), "the second one is held by the ceiling")
}

// A second command of a running position that the ceiling holds has its wait counted under `load` (codex R2: the
// verdict of that path was dropped, so wait_reasons and the report missed it). (M: drop the reasons again.)
func TestPosition_SecondCommandHeldByTheCeilingCountsLoad(t *testing.T) {
	f := newPosFix(t)
	a1 := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go build"))
	require.Equal(t, resources.StateHeld, a1.State)
	f.setHost(resources.HostUse{Measured: 10, NCPU: 10, Load1: 12, Zone: "normal", Trend: "flat"})
	a2 := decodeLease(t, f.postAs(newCID(), pidA, resources.CallQueue, "go vet"))
	require.Equal(t, resources.StateQueued, a2.State, "held by the ceiling")
	f.advance(7 * time.Second)
	assert.JSONEq(t, `{"load":7000}`, f.m.reasons.peek(a2.ID, f.m.now()))
}

// The zone, the trend and the load of a pass come from one sample: an unavailable one reads as unknown with an
// all-zero host (fail open), an available one gives its own figures together.
func TestAdmitSample_OneSampleForZoneTrendAndLoad(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.setHost(resources.HostUse{Measured: 10, NCPU: 10, Load1: 12, Zone: "warning", Trend: "falling"})
	zone, trend, host := f.m.admitSample()
	assert.Equal(t, resources.ZoneWarning, zone)
	assert.Equal(t, resources.TrendFalling, trend)
	assert.Equal(t, 12.0, host.Load1)
	f.m.publish(&resources.Snapshot{Available: false, Capacity: resources.Capacity, Host: resources.HostUse{NCPU: 10, Load1: 12, Zone: "danger"}, Mode: resources.ModeLease})
	zone, trend, host = f.m.admitSample()
	assert.Equal(t, resources.ZoneUnknown, zone)
	assert.Equal(t, resources.TrendUnknown, trend)
	assert.Equal(t, resources.HostUse{}, host)
}

// load_cap_pct 0 turns it off.
func TestPassLoadCap_ZeroIsOff(t *testing.T) {
	f := newPassFix(t, resources.ModeLease)
	f.set.set(resources.Settings{Mode: resources.ModeLease, GapS: intp(0), LoadCapPct: intp(0)})
	f.heldLease("h", resources.ScopeProcess, 100)
	f.queue("a", 35, 5*time.Minute)
	f.setLoad(40)
	f.pass()
	assert.Equal(t, "held", f.state("a"))
}
