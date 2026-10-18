package queue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Every database has an epoch, made when its tables are: it has the wire's form, it does not change when the queue is opened again or
// migrated again, and another database has another one. Mutation gates: one fixed value → two databases agree; made at every open → the
// reopened queue disagrees.
func TestEveryDatabaseHasItsOwnStableEpoch(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	ea, err := a.q.Epoch(context.Background())
	require.NoError(t, err)
	eb, err := b.q.Epoch(context.Background())
	require.NoError(t, err)
	assert.True(t, wire.ValidEpoch(ea), ea)
	assert.True(t, wire.ValidEpoch(eb), eb)
	assert.NotEqual(t, ea, eb, "a database made again is another database")

	require.NoError(t, Migrate(context.Background(), a.db))
	q2, err := New(a.db, Options{Origin: "mlab", Now: a.clk.Now})
	require.NoError(t, err)
	again, err := q2.Epoch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, ea, again, "the same database, opened again")

	var rows int
	require.NoError(t, a.db.QueryRow(`SELECT count(*) FROM db_epoch`).Scan(&rows))
	assert.Equal(t, 1, rows)
}

// A version 4 database (3b-4a, on main) gets an epoch when it is upgraded, and keeps it afterwards (running the migration again must not
// make a second one: the clients that have stored the first would start over for nothing).
func TestAVersionFourDatabaseGainsAnEpoch(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	_, err := f.db.Exec(`DROP TABLE db_epoch`)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE queue_schema SET version = 4`)
	require.NoError(t, err)

	require.NoError(t, Migrate(ctx, f.db))
	var v int
	require.NoError(t, f.db.QueryRow(`SELECT version FROM queue_schema`).Scan(&v))
	assert.Equal(t, 5, v)
	var epoch string
	require.NoError(t, f.db.QueryRow(`SELECT id FROM db_epoch`).Scan(&epoch))
	assert.True(t, wire.ValidEpoch(epoch), epoch)

	// the migration run again for a database that is at 4 and already has one: it is kept
	_, err = f.db.Exec(`UPDATE queue_schema SET version = 4`)
	require.NoError(t, err)
	require.NoError(t, Migrate(ctx, f.db))
	var kept string
	require.NoError(t, f.db.QueryRow(`SELECT id FROM db_epoch`).Scan(&kept))
	assert.Equal(t, epoch, kept)
	var rows int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM db_epoch`).Scan(&rows))
	assert.Equal(t, 1, rows)
}

func TestNewReadsTheEpochOnce(t *testing.T) {
	f := newFixture(t)
	first, err := f.q.Epoch(context.Background())
	require.NoError(t, err)
	// the value is the queue's for its whole life: reading it needs no database
	require.NoError(t, f.db.Close())
	second, err := f.q.Epoch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, first, second)
}
