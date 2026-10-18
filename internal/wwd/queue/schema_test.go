package queue

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Migrate creates the queue's tables, and running it again changes nothing: wwd.db is opened by wwd on every start, so it runs on every
// start. Mutation gates: no IF NOT EXISTS → the second run errors; the version row inserted again → two rows.
func TestMigrateIsIdempotent(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	require.NoError(t, Migrate(ctx, db))
	require.NoError(t, Migrate(ctx, db))
	require.NoError(t, Migrate(ctx, db))

	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM queue_schema`).Scan(&n))
	assert.Equal(t, 1, n, "one version row, however many times Migrate ran")
	var v int
	require.NoError(t, db.QueryRow(`SELECT version FROM queue_schema`).Scan(&v))
	assert.Equal(t, schemaVersion, v)

	for _, table := range []string{"jobs", "git_pins"} {
		var name string
		require.NoError(t, db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name), table)
	}
}

// The table carries the columns the claim, lease and result transitions of the next PR need, so no migration is due when they land
// (the data layer is created at the first deploy; after that a schema change must migrate).
func TestJobsTableHasEveryColumnOfTheStateMachine(t *testing.T) {
	db := openDB(t)
	require.NoError(t, Migrate(context.Background(), db))
	rows, err := db.Query(`SELECT name FROM pragma_table_info('jobs')`)
	require.NoError(t, err)
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		have[n] = true
	}
	for _, c := range []string{
		"id", "origin", "repo_name", "snap_commit", "task", "args", "class", "labels", "locks", "cacheable", "timeout_s",
		"supersede_key", "interruptible", "queue_timeout_s", "adhoc", "sub_session", "created_at", "state", "attempts", "lapses",
		"claim_worker", "claim_id", "claim_since", "lease_deadline", "lease_s", "cancel_requested", "superseded_by",
		"result", "result_claim_id", "finished_at", "log_size", "log_at", "local", "git_dir", "pinned", "updated_at",
	} {
		assert.True(t, have[c], "jobs has no column %s", c)
	}
}

// A database that version 1 created (3b-2a, on main) is upgraded in place: git_pins appears, the pins its jobs already hold get a
// record (so they are still released when those jobs end), the version moves to 2, and a submission works on it afterwards. Mutation
// gates: no migration step → git_pins is missing and Submit fails; no backfill → the old job's pin is never released.
func TestAVersionOneDatabaseIsUpgraded(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	old := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	// what version 1 left: no git_pins table, version 1; the job holds a pin
	_, err := f.db.Exec(`DROP TABLE git_pins`)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE queue_schema SET version = 1`)
	require.NoError(t, err)

	require.NoError(t, Migrate(ctx, f.db))

	var v int
	require.NoError(t, f.db.QueryRow(`SELECT version FROM queue_schema`).Scan(&v))
	assert.Equal(t, schemaVersion, v)
	var dir string
	require.NoError(t, f.db.QueryRow(`SELECT git_dir FROM git_pins WHERE id = ?`, old).Scan(&dir))
	assert.Equal(t, f.gitDir, dir, "the pin the old job holds is recorded")

	id := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "other" })
	assert.NotEmpty(t, id, "a submission works on the upgraded database")
	_, err = f.q.Cancel(ctx, old)
	require.NoError(t, err)
	assert.False(t, f.pins.has(f.gitDir, old), "and the old job's pin is released when it ends")
}

// A version 2 database (3b-2b, on main) gets jobs.lapses, with 0 for the jobs it already holds, and a lapse works on it afterwards. A
// database that already has the column (version 3 code made it) is not altered twice. Mutation gate: no step → the claim query fails.
func TestAVersionTwoDatabaseGainsLapses(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	id := f.submit(t)
	_, err := f.db.Exec(`ALTER TABLE jobs DROP COLUMN lapses`)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE queue_schema SET version = 2`)
	require.NoError(t, err)

	require.NoError(t, Migrate(ctx, f.db))
	require.NoError(t, Migrate(ctx, f.db))
	var lapses, v int
	require.NoError(t, f.db.QueryRow(`SELECT lapses FROM jobs WHERE id = ?`, id).Scan(&lapses))
	assert.Equal(t, 0, lapses)
	require.NoError(t, f.db.QueryRow(`SELECT version FROM queue_schema`).Scan(&v))
	assert.Equal(t, schemaVersion, v)

	f.claim(t, id, "air26")
	f.clk.Advance(61 * time.Second)
	_, err = f.q.Sweep(ctx)
	require.NoError(t, err)
	require.NoError(t, f.db.QueryRow(`SELECT lapses FROM jobs WHERE id = ?`, id).Scan(&lapses))
	assert.Equal(t, 1, lapses)

	// version 2 again, the column still there: the step does not add it a second time
	_, err = f.db.Exec(`UPDATE queue_schema SET version = 2`)
	require.NoError(t, err)
	require.NoError(t, Migrate(ctx, f.db))
}

// A database written by a newer wwd is not opened by an older one: the older code would misread it.
func TestMigrateRefusesANewerSchema(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	require.NoError(t, Migrate(ctx, db))
	_, err := db.Exec(`UPDATE queue_schema SET version = ?`, schemaVersion+1)
	require.NoError(t, err)
	err = Migrate(ctx, db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "newer")
}

// Rows survive a restart: a second Queue on the same database sees what the first one stored.
func TestJobsSurviveARestart(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	q2, err := New(f.db, Options{Origin: "mlab", Now: f.clk.Now})
	require.NoError(t, err)
	j, err := q2.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, id, j.ID)
	assert.Equal(t, "queued", string(j.State))
}
