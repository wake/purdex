package resourcesmod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// The v2 columns come out of resources.db into the report: zone seconds from
// the minute rows, waits by reason, parks, the zone at the grant, and the advise
// shadow's verdicts.
func TestReport_V2ColumnsFromTheStore(t *testing.T) {
	s, err := openLeaseStore(":memory:")
	require.NoError(t, err)
	defer s.Close()
	at := reportDay0.UnixMilli()
	ins := func(id string, waited int64, zone, trend, reasons string, parks int, v2 int64, open int) {
		_, err := s.db.Exec(`INSERT INTO resource_leases
			(id, client_id, state, kind, weight, holder_pid, scope, created_at, deadline_at, lease_until, granted_at, ended_at, end_reason,
			 waited_ms, dec_recorded, dec_path, dec_zone, dec_trend, wait_reasons, parks, v2_wait_ms, v2_wait_open)
			VALUES (?, ?, 'ended', 'build', 35, 1, 'process', ?, ?, ?, ?, ?, 'released', ?, 1, 'waited', ?, ?, ?, ?, ?, ?)`,
			id, "c-"+id, at, at+300000, at+30000, at+waited, at+waited+1000, waited, zone, trend, reasons, parks, v2, open)
		require.NoError(t, err)
	}
	ins("a", 40000, "normal", "flat", `{"gap":30000,"behind":10000}`, 0, -1, 0)
	ins("b", 100000, "warning", "falling", `{"danger":70000,"gap":30000}`, 2, -1, 0)
	ins("c", 0, "normal", "flat", "", 0, 61000, 1)
	ins("d", 0, "normal", "flat", "not json", 1, 0, 0)
	require.NoError(t, s.InsertMinute(minuteRow{At: at, NCPU: 10, SampledS: 60, NormalS: 40, WarningS: 15, DangerS: 5}))
	require.NoError(t, s.InsertMinute(minuteRow{At: at + 60000, NCPU: 10})) // a legacy minute

	leases, minutes, err := s.ReportRows(at, at+3600000)
	require.NoError(t, err)
	rep := resources.BuildReport(at, at+3600000, leases, minutes)

	assert.Equal(t, resources.ReportZones{Minutes: 1, SampledS: 60, Normal: 40.0 / 60, Warning: 15.0 / 60, Danger: 5.0 / 60}, rep.Zones)
	assert.Equal(t, 3, rep.Parks)
	assert.Equal(t, []resources.ReportReasonWait{
		{Reason: "danger", N: 1, P50: 100000, P90: 100000, Max: 100000},
		{Reason: "gap", N: 1, P50: 40000, P90: 40000, Max: 40000},
	}, rep.WaitByReason)
	require.Len(t, rep.Longest, 2)
	assert.Equal(t, resources.ReportWaitRow{Kind: "build", CreatedAt: at, WaitedMS: 100000, Dominant: "danger",
		Reasons: map[string]int64{"danger": 70000, "gap": 30000}, Zone: "warning", Trend: "falling"}, rep.Longest[0])
	assert.Equal(t, 2, rep.Shadow.N, "c (61 s, open) and d (0 s); a and b have no verdict")
	assert.Equal(t, 1, rep.Shadow.Open)
	assert.Equal(t, int64(61000), rep.Shadow.WaitMS.Max)
	require.Len(t, rep.Shadow.Longest, 1)
	assert.True(t, rep.Shadow.Longest[0].Open)
}
