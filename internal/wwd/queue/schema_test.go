package queue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	for _, table := range []string{"jobs"} {
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
		"supersede_key", "interruptible", "queue_timeout_s", "adhoc", "sub_session", "created_at", "state", "attempts",
		"claim_worker", "claim_id", "claim_since", "lease_deadline", "lease_s", "cancel_requested", "superseded_by",
		"result", "result_claim_id", "finished_at", "log_size", "log_at", "git_dir", "pinned", "updated_at",
	} {
		assert.True(t, have[c], "jobs has no column %s", c)
	}
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
