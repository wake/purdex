package resourcesmod

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// host_minutes exactly as the deployed binaries (741: mlab, air26) made it: the D-8.2 table plus the lease v2 zone columns, and
// none of the gate's.
const deployedHostMinutes = `CREATE TABLE host_minutes (
		at             INTEGER PRIMARY KEY,
		load1          REAL    NOT NULL,
		ncpu           INTEGER NOT NULL,
		mem            REAL    NOT NULL,
		measured       INTEGER NOT NULL,
		full           INTEGER NOT NULL,
		full_ticks     INTEGER NOT NULL,
		full_starts    INTEGER NOT NULL,
		full_longest_s INTEGER NOT NULL,
		held           INTEGER NOT NULL,
		heavy_held     INTEGER NOT NULL,
		sum_charge     REAL    NOT NULL,
		waiting        INTEGER NOT NULL,
		unleased       REAL    NOT NULL
	, sampled_s INTEGER NOT NULL DEFAULT 0, normal_s INTEGER NOT NULL DEFAULT 0, warning_s INTEGER NOT NULL DEFAULT 0, danger_s INTEGER NOT NULL DEFAULT 0, pressure_max INTEGER NOT NULL DEFAULT 0, zone_max INTEGER NOT NULL DEFAULT 0)`

// The table of the deployed schema gains the gate's columns on open, keeps its rows (they read 0), a second open changes
// nothing, and a binary of before (which names its columns in the INSERT) still writes it: a rollback needs no step.
func TestMinutes_MigratesTheDeployedTableAndAnOlderBinaryStillWritesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = old.Exec(deployedHostMinutes + `;
		INSERT INTO host_minutes (at, load1, ncpu, mem, measured, full, full_ticks, full_starts, full_longest_s, held, heavy_held, sum_charge, waiting, unleased, sampled_s)
		VALUES (60000, 1, 4, 50, 40, 0, 0, 0, 0, 2, 1, 35, 3, 4, 60)`)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	for pass := 0; pass < 2; pass++ {
		s, err := openLeaseStore(path)
		require.NoError(t, err, "pass %d", pass)
		have, err := columnSet(s.db, "host_minutes")
		require.NoError(t, err)
		for _, c := range []string{"heavy_running", "light_running", "gate_waiting"} {
			assert.True(t, have[c], "pass %d: %s", pass, c)
		}
		var held, heavyHeld, heavy, light, waiting int
		require.NoError(t, s.db.QueryRow(`SELECT held, heavy_held, heavy_running, light_running, gate_waiting FROM host_minutes WHERE at = 60000`).
			Scan(&held, &heavyHeld, &heavy, &light, &waiting))
		assert.Equal(t, [5]int{2, 1, 0, 0, 0}, [5]int{held, heavyHeld, heavy, light, waiting}, "the old row keeps its lease figures and reads 0 for the gate's")

		require.NoError(t, s.InsertMinute(minuteRow{At: 120000, NCPU: 4, HeavyRunning: 1, LightRunning: 2, GateWaiting: 3}))
		require.NoError(t, s.InsertMinute(minuteRow{At: 120000, NCPU: 4, HeavyRunning: 0, LightRunning: 3, GateWaiting: 1})) // merged: peaks
		require.NoError(t, s.db.QueryRow(`SELECT heavy_running, light_running, gate_waiting FROM host_minutes WHERE at = 120000`).Scan(&heavy, &light, &waiting))
		assert.Equal(t, [3]int{1, 3, 3}, [3]int{heavy, light, waiting}, "a minute written twice keeps the larger peak of each")
		require.NoError(t, s.Close())
	}

	// the binary of before: its INSERT names the columns it knows; the new ones take their DEFAULT 0
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`INSERT INTO host_minutes (at, load1, ncpu, mem, measured, full, full_ticks, full_starts, full_longest_s, held, heavy_held, sum_charge, waiting, unleased,
		sampled_s, normal_s, warning_s, danger_s, pressure_max, zone_max) VALUES (180000, 1, 4, 50, 40, 0, 0, 0, 0, 0, 0, 0, 0, 0, 5, 5, 0, 0, 1, 0)`)
	require.NoError(t, err)
	var heavy int
	require.NoError(t, db.QueryRow(`SELECT heavy_running FROM host_minutes WHERE at = 180000`).Scan(&heavy))
	assert.Equal(t, 0, heavy)
}
