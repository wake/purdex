package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func granted(kind string, waited int64, reasons map[string]int64) ReportLease {
	return ReportLease{Kind: kind, GrantedAt: 1, Recorded: true, Path: PathWaited, WaitedMS: waited, Reasons: reasons, V2WaitMS: -1}
}

// Zone shares come from the minutes that carry sampled_s; a row from before
// the columns (sampled_s = 0) is left out, and the zones' coverage says how
// many minutes counted.
func TestReport_ZoneShares(t *testing.T) {
	minutes := []ReportMinute{
		{At: 0, SampledS: 60, NormalS: 45, WarningS: 10, DangerS: 5},
		{At: 60000, SampledS: 60, NormalS: 60},
		{At: 120000}, // a legacy row: not counted
	}
	r := BuildReport(0, 1, nil, minutes)
	assert.Equal(t, 3, r.Coverage.Minutes)
	assert.Equal(t, ReportZones{Minutes: 2, SampledS: 120, Normal: 105.0 / 120, Warning: 10.0 / 120, Danger: 5.0 / 120}, r.Zones)
	assert.Equal(t, ReportZones{}, BuildReport(0, 1, nil, []ReportMinute{{At: 0}}).Zones, "no covered minute: all zero")
}

// Waits by the reason that held the row longest.
func TestReport_WaitByDominantReason(t *testing.T) {
	leases := []ReportLease{
		granted("build", 30000, map[string]int64{"gap": 25000, "rising": 5000}),
		granted("build", 50000, map[string]int64{"gap": 30000, "danger": 20000}),
		granted("build", 90000, map[string]int64{"danger": 80000, "gap": 10000}),
		granted("build", 10000, map[string]int64{"rising": 6000, "behind": 4000}),
		granted("build", 0, nil), // no reasons: in no reason's row
		granted("build", 8000, map[string]int64{"gap": 4000, "behind": 4000}), // a tie goes to the name first in order
	}
	r := BuildReport(0, 1, leases, nil)
	got := map[string]ReportReasonWait{}
	for _, w := range r.WaitByReason {
		got[w.Reason] = w
	}
	assert.Equal(t, ReportReasonWait{Reason: "danger", N: 1, P50: 90000, P90: 90000, Max: 90000}, got["danger"])
	assert.Equal(t, ReportReasonWait{Reason: "gap", N: 2, P50: 30000, P90: 50000, Max: 50000}, got["gap"])
	assert.Equal(t, ReportReasonWait{Reason: "rising", N: 1, P50: 10000, P90: 10000, Max: 10000}, got["rising"])
	assert.Equal(t, ReportReasonWait{Reason: "behind", N: 1, P50: 8000, P90: 8000, Max: 8000}, got["behind"], "a tie goes to the name first in order")
	assert.Len(t, r.WaitByReason, 4)
	// Sorted by reason name.
	for i := 1; i < len(r.WaitByReason); i++ {
		assert.Less(t, r.WaitByReason[i-1].Reason, r.WaitByReason[i].Reason)
	}
}

func TestReport_ParksAndLongestWaits(t *testing.T) {
	var leases []ReportLease
	for i := 0; i < 7; i++ {
		l := granted("test-full", int64(i+1)*1000, map[string]int64{"gap": int64(i+1) * 1000})
		l.CreatedAt = int64(i)
		l.Parks = i % 3
		l.Zone, l.Trend = "warning", "flat"
		leases = append(leases, l)
	}
	leases = append(leases, ReportLease{Kind: "build", Parks: 4}) // never granted, but parked
	r := BuildReport(0, 1, leases, nil)
	assert.Equal(t, 0+1+2+0+1+2+0+4, r.Parks)
	require.Len(t, r.Longest, 5)
	assert.Equal(t, int64(7000), r.Longest[0].WaitedMS)
	assert.Equal(t, int64(3000), r.Longest[4].WaitedMS)
	assert.Equal(t, ReportWaitRow{Kind: "test-full", CreatedAt: 6, WaitedMS: 7000, Dominant: "gap",
		Reasons: map[string]int64{"gap": 7000}, Zone: "warning", Trend: "flat"}, r.Longest[0])
	// Zero waits are not "longest waits".
	assert.Empty(t, BuildReport(0, 1, []ReportLease{granted("build", 0, nil)}, nil).Longest)
}

// The advise shadow: what v2 would have waited, its percentiles, how many are
// only lower bounds, and the longest with their reasons.
func TestReport_ShadowWaits(t *testing.T) {
	mk := func(wait int64, open bool, reasons map[string]int64, created int64) ReportLease {
		l := granted("build", 0, nil)
		l.V2WaitMS, l.V2Open, l.Reasons, l.CreatedAt = wait, open, reasons, created
		return l
	}
	leases := []ReportLease{
		mk(0, false, nil, 1),
		mk(30000, false, map[string]int64{"gap": 30000}, 2),
		mk(61000, false, map[string]int64{"gap": 31000, "behind": 30000}, 3),
		mk(90000, true, map[string]int64{"danger": 90000}, 4),
		granted("build", 5000, nil), // lease mode row: v2_wait_ms = -1, not in the shadow
	}
	r := BuildReport(0, 1, leases, nil)
	assert.Equal(t, 4, r.Shadow.N)
	assert.Equal(t, 1, r.Shadow.Open)
	assert.Equal(t, ReportWait{P50: 30000, P90: 90000, Max: 90000}, r.Shadow.WaitMS)
	require.Len(t, r.Shadow.Longest, 3, "rows that waited at all, up to 5")
	assert.Equal(t, ReportShadowRow{Kind: "build", CreatedAt: 4, WaitMS: 90000, Open: true, Dominant: "danger", Reasons: map[string]int64{"danger": 90000}}, r.Shadow.Longest[0])
	assert.Equal(t, int64(61000), r.Shadow.Longest[1].WaitMS)
	assert.Equal(t, "gap", r.Shadow.Longest[1].Dominant)

	assert.Equal(t, ReportShadow{Longest: []ReportShadowRow{}}, BuildReport(0, 1, []ReportLease{granted("build", 0, nil)}, nil).Shadow)
}

// The same rows give the same report, in the same order.
func TestReport_V2FiguresAreDeterministic(t *testing.T) {
	leases := []ReportLease{
		granted("a", 5000, map[string]int64{"gap": 5000}),
		granted("b", 5000, map[string]int64{"gap": 5000}),
		granted("c", 5000, map[string]int64{"rising": 5000}),
	}
	first := BuildReport(0, 1, leases, nil)
	for i := 0; i < 20; i++ {
		assert.Equal(t, first, BuildReport(0, 1, leases, nil))
	}
}
