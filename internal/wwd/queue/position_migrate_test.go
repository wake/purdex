package queue

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A change of the manual place moves the origin's cursor (a client that follows rev sees it), and so do all three kinds of change; a
// request that changes nothing does not.
func TestPositionMovesTheCursor(t *testing.T) {
	f := newFixture(t)
	a := f.distinct(t)
	head := func() int64 {
		h, err := f.q.Head(bg)
		require.NoError(t, err)
		return h
	}
	h0 := head()
	for _, p := range []wire.Position{wire.PositionFront, wire.PositionBack, wire.PositionHold, wire.PositionNormal} {
		before := head()
		_, err := f.q.SetPosition(bg, a, p)
		require.NoError(t, err)
		assert.Greater(t, head(), before, string(p)+" moves the cursor")
	}
	before := head()
	_, err := f.q.SetPosition(bg, a, wire.PositionNormal)
	require.NoError(t, err)
	assert.Equal(t, before, head(), "no change, no cursor move")
	assert.Greater(t, head(), h0)
}

// A version 5 database (before the manual place) gets the two columns and a trigger that watches them; the columns of the old rows are
// "no manual place". The step can run again.
func TestAVersionFiveDatabaseGainsTheManualPlace(t *testing.T) {
	f := newFixture(t)
	a := f.distinct(t)
	for _, stmt := range []string{
		`DROP TRIGGER jobs_rev_upd`,
		`ALTER TABLE jobs DROP COLUMN manual`,
		`ALTER TABLE jobs DROP COLUMN manual_seq`,
		`CREATE TRIGGER jobs_rev_upd AFTER UPDATE OF ` + revColumnsV4 + ` ON jobs BEGIN
			UPDATE job_rev SET n = n + 1;
			UPDATE jobs SET rev = (SELECT n FROM job_rev) WHERE seq = NEW.seq;
		END`,
		`UPDATE queue_schema SET version = 5`,
	} {
		_, err := f.db.Exec(stmt)
		require.NoError(t, err, stmt)
	}

	for i := 0; i < 2; i++ { // and again: each step looks first
		require.NoError(t, Migrate(bg, f.db))
		_, err := f.db.Exec(`UPDATE queue_schema SET version = 5`)
		require.NoError(t, err)
	}
	require.NoError(t, Migrate(bg, f.db))
	var v int
	require.NoError(t, f.db.QueryRow(`SELECT version FROM queue_schema`).Scan(&v))
	assert.Equal(t, 6, v)
	j := f.get(t, a)
	assert.Equal(t, wire.Position(""), j.Manual)
	assert.Zero(t, j.ManualSeq)

	before, err := f.q.Head(bg)
	require.NoError(t, err)
	_, err = f.q.SetPosition(bg, a, wire.PositionFront)
	require.NoError(t, err)
	after, err := f.q.Head(bg)
	require.NoError(t, err)
	assert.Greater(t, after, before, "the new trigger watches the manual place; the old one would not have")
}

// A version 3 database is brought forward step by step: the version 4 trigger is made without columns that do not exist yet, and the
// version 6 step then replaces it.
func TestAVersionThreeDatabaseReachesTheCurrentSchema(t *testing.T) {
	f := newFixture(t)
	a := f.distinct(t)
	for _, stmt := range []string{
		`DROP TRIGGER jobs_rev_upd`,
		`DROP TRIGGER jobs_rev_ins`,
		`DROP INDEX jobs_rev`,
		`DROP TABLE job_rev`,
		`DROP TABLE db_epoch`,
		`ALTER TABLE jobs DROP COLUMN rev`,
		`ALTER TABLE jobs DROP COLUMN manual`,
		`ALTER TABLE jobs DROP COLUMN manual_seq`,
		`UPDATE queue_schema SET version = 3`,
	} {
		_, err := f.db.Exec(stmt)
		require.NoError(t, err, stmt)
	}
	require.NoError(t, Migrate(bg, f.db))
	var v int
	require.NoError(t, f.db.QueryRow(`SELECT version FROM queue_schema`).Scan(&v))
	assert.Equal(t, schemaVersion, v)

	before, err := f.q.Head(bg)
	require.NoError(t, err)
	_, err = f.q.SetPosition(bg, a, wire.PositionHold)
	require.NoError(t, err)
	after, err := f.q.Head(bg)
	require.NoError(t, err)
	assert.Greater(t, after, before)
}
