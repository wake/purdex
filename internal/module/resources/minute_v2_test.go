package resourcesmod

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

type v2Minute struct {
	SampledS, NormalS, WarningS, DangerS, PressureMax, ZoneMax int
}

func (f *minuteFix) v2Minutes() []v2Minute {
	f.t.Helper()
	rows, err := f.m.store.db.Query(`SELECT sampled_s, normal_s, warning_s, danger_s, pressure_max, zone_max FROM host_minutes ORDER BY at`)
	require.NoError(f.t, err)
	defer rows.Close()
	var out []v2Minute
	for rows.Next() {
		var r v2Minute
		require.NoError(f.t, rows.Scan(&r.SampledS, &r.NormalS, &r.WarningS, &r.DangerS, &r.PressureMax, &r.ZoneMax))
		out = append(out, r)
	}
	return out
}

func zoned(zone string, pressure int) resources.HostUse {
	return resources.HostUse{Load1: 1, Mem: 40, Measured: 40, NCPU: 10, Zone: zone, Pressure: pressure}
}

// A minute with mixed zones: the seconds per zone are the tick interval times
// the ticks, they add up to sampled_s, and the pressure and zone maxima are the
// worst of the minute. (M: count a zone's tick in the wrong bucket and the
// split is red; skip sampled_s and the sum is.)
func TestMinutes_ZoneSecondsAndMaxima(t *testing.T) {
	f := newMinuteFix(t)
	f.ticks(3, zoned("normal", 1))
	f.ticks(2, zoned("warning", 2))
	f.tick(zoned("danger", 4))
	f.ticks(7, zoned("normal", 1)) // 13 ticks: crosses into the next minute
	got := f.v2Minutes()
	require.Len(t, got, 1)
	r := got[0]
	assert.Equal(t, v2Minute{SampledS: 60, NormalS: 45, WarningS: 10, DangerS: 5, PressureMax: 4, ZoneMax: 2}, r)
	assert.Equal(t, r.SampledS, r.NormalS+r.WarningS+r.DangerS, "the zone seconds sum to sampled_s")
}

// An unknown zone still counts as a sampled second but belongs to no zone.
func TestMinutes_UnknownZoneIsSampledButInNoBucket(t *testing.T) {
	f := newMinuteFix(t)
	f.ticks(4, zoned("unknown", 1))
	f.ticks(9, zoned("normal", 1))
	got := f.v2Minutes()
	require.Len(t, got, 1)
	assert.Equal(t, v2Minute{SampledS: 60, NormalS: 40, PressureMax: 1, ZoneMax: 0}, got[0])
}

// A tick that fails adds nothing (the lease table could not be read).
func TestMinutes_FailedTickAddsNothing(t *testing.T) {
	f := newMinuteFix(t)
	f.m.noteMinute(context.Background(), zoned("danger", 4), f.clock.now(), nil, false)
	f.advance(f.m.interval)
	f.ticks(12, zoned("normal", 1)) // 11 in the first minute, the 12th flushes it
	got := f.v2Minutes()
	require.Len(t, got, 1)
	assert.Equal(t, 55, got[0].SampledS)
	assert.Equal(t, 0, got[0].DangerS)
}

// A minute that is written twice (a restart inside it, the sampler off and on)
// keeps the larger maxima and adds the seconds.
func TestMinutes_MergeKeepsMaximaAndSums(t *testing.T) {
	f := newMinuteFix(t)
	at := int64(60000)
	require.NoError(t, f.m.store.InsertMinute(minuteRow{At: at, NCPU: 1, SampledS: 20, NormalS: 15, WarningS: 5, PressureMax: 2, ZoneMax: 2}))
	require.NoError(t, f.m.store.InsertMinute(minuteRow{At: at, NCPU: 1, SampledS: 30, NormalS: 10, DangerS: 20, PressureMax: 1, ZoneMax: 1}))
	got := f.v2Minutes()
	require.Len(t, got, 1)
	assert.Equal(t, v2Minute{SampledS: 50, NormalS: 25, WarningS: 5, DangerS: 20, PressureMax: 2, ZoneMax: 2}, got[0])
}

// A database from before the columns opens, keeps its rows, and reads
// sampled_s = 0 for them.
func TestMinutes_MigratesAPreV2Table(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = old.Exec(hostMinutesSchema + `;
		INSERT INTO host_minutes VALUES (60000, 1, 4, 50, 40, 0, 0, 0, 0, 0, 0, 0, 2, 0);`)
	require.NoError(t, err)
	require.NoError(t, old.Close())
	s, err := openLeaseStore(path)
	require.NoError(t, err)
	defer s.Close()
	var sampled, waiting int
	require.NoError(t, s.db.QueryRow(`SELECT sampled_s, waiting FROM host_minutes WHERE at = 60000`).Scan(&sampled, &waiting))
	assert.Equal(t, 0, sampled)
	assert.Equal(t, 2, waiting)
}
