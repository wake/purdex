package queue

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// started is the job's started_at, as a client reads it.
func (f *fixture) started(t *testing.T, id string) *time.Time { return f.get(t, id).StartedAt }

// A job has no started_at until its worker says it has the workspace and runs the command: queued and claimed jobs show none (the gap
// between the two is the "preparing" time the App wants to tell from "running").
func TestStartedAtIsNullUntilTheJobStarts(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	assert.Nil(t, f.started(t, id), "queued")
	f.claim(t, id, "air26")
	assert.Nil(t, f.started(t, id), "claimed, the workspace is being made")
}

// Start writes the time on the origin's clock in the same step that makes the job running, so a job that is running has one, and a
// repeated Start with the same claim (a renewal) does not move it.
func TestStartWritesStartedAtAndARepeatedStartKeepsIt(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	c := f.claim(t, id, "air26")
	f.clk.Advance(7 * time.Second) // preparing
	_, err := f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)

	j := f.get(t, id)
	require.Equal(t, wire.StateRunning, j.State)
	require.NotNil(t, j.StartedAt, "running means started")
	assert.Equal(t, f.clk.Now(), *j.StartedAt)
	assert.False(t, j.StartedAt.After(f.clk.Now()), "not later than now")
	assert.Equal(t, f.clk.Now().Add(-7*time.Second), j.Claim.Since, "claimed 7 s before it started")
	require.NoError(t, j.Validate())

	first := *j.StartedAt
	f.clk.Advance(20 * time.Second)
	_, err = f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)
	assert.Equal(t, first, *f.started(t, id), "a repeated start with the same claim is a renewal: the time stays")
}

// The origin's clock stepping back while the worker prepares (NTP, a person) does not make the preparing time negative: a start is never
// written before the claim's own time.
func TestStartedAtIsNeverBeforeTheClaimWhenTheClockStepsBack(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	c := f.claim(t, id, "air26")
	claimedAt := f.get(t, id).Claim.Since
	f.clk.Advance(-90 * time.Second) // the clock stepped back
	_, err := f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)

	j := f.get(t, id)
	require.NotNil(t, j.StartedAt)
	assert.False(t, j.StartedAt.Before(claimedAt), "started_at %v is before the claim %v", j.StartedAt, claimedAt)
	assert.Equal(t, claimedAt, *j.StartedAt, "held at the claim's time")
}

// Every way back to the queue clears it: a worker that hands the claim back, and a lease that ran out. The job then waits for another
// worker, and its next Start writes a new time.
func TestEveryWayBackToQueuedClearsStartedAt(t *testing.T) {
	f := newFixture(t)

	t.Run("release", func(t *testing.T) {
		id := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
		c := f.claim(t, id, "air26")
		_, err := f.q.Start(bg, id, c.ClaimID)
		require.NoError(t, err)
		require.NotNil(t, f.started(t, id))

		require.NoError(t, f.q.Release(bg, id, wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: wire.ReleaseReclaim}))
		require.Equal(t, wire.StateQueued, f.get(t, id).State)
		assert.Nil(t, f.started(t, id), "back in the queue: not started")

		f.clk.Advance(30 * time.Second)
		c2 := f.claim(t, id, "mserv")
		_, err = f.q.Start(bg, id, c2.ClaimID)
		require.NoError(t, err)
		require.NotNil(t, f.started(t, id))
		assert.Equal(t, f.clk.Now(), *f.started(t, id), "the second run has its own start")
	})

	t.Run("lease ran out", func(t *testing.T) {
		id := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
		c := f.claim(t, id, "air26")
		_, err := f.q.Start(bg, id, c.ClaimID)
		require.NoError(t, err)
		require.NotNil(t, f.started(t, id))

		f.clk.Advance(61 * time.Second)
		_, err = f.q.Sweep(bg)
		require.NoError(t, err)
		require.Equal(t, wire.StateQueued, f.get(t, id).State)
		assert.Nil(t, f.started(t, id), "a lapsed lease puts it back, not started")

		f.clk.Advance(5 * time.Second)
		c2 := f.claim(t, id, "mserv")
		_, err = f.q.Start(bg, id, c2.ClaimID)
		require.NoError(t, err)
		assert.Equal(t, f.clk.Now(), *f.started(t, id))
	})
}

// An ended job keeps the time of the last start: how long it ran is finished_at minus this.
func TestAnEndedJobKeepsTheLastStartedAt(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	c := f.claim(t, id, "air26")
	f.clk.Advance(3 * time.Second)
	_, err := f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)
	at := *f.started(t, id)
	f.clk.Advance(10 * time.Second)
	f.postResult(t, id, c.ClaimID)

	j := f.get(t, id)
	require.Equal(t, wire.StateSucceeded, j.State)
	require.NotNil(t, j.StartedAt)
	assert.Equal(t, at, *j.StartedAt)
}

// A fast-lane run is recorded as running at once, with no preparing: it started when it was recorded.
func TestALocalRunStartedWhenItWasRecorded(t *testing.T) {
	f := newFixture(t)
	r := f.recordLocal(t)
	j := f.get(t, r.ID)
	require.NotNil(t, j.StartedAt, "running means started")
	assert.Equal(t, f.clk.Now(), *j.StartedAt)
}

// The change is announced with the job as it is now: the jobs.changed event of the start carries started_at, and so does the cursor read.
func TestTheStartEventAndTheCursorReadCarryStartedAt(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	c := f.claim(t, id, "air26")
	f.ev.reset()
	f.clk.Advance(2 * time.Second)
	_, err := f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)

	evs := f.ev.all()
	require.Len(t, evs, 1)
	require.NotNil(t, evs[0].Job.StartedAt)
	assert.Equal(t, f.clk.Now(), *evs[0].Job.StartedAt)

	jobs, _ := f.after(t, 0)
	var found bool
	for _, j := range jobs {
		if j.ID == id {
			found = true
			require.NotNil(t, j.StartedAt, "the cursor read shows it")
		}
	}
	assert.True(t, found)
}

// Version 8 → 9: the column appears with null for the jobs the database already holds, the version moves, the step can run again, and a start
// works on it afterwards. A wwd that knows only version 8 refuses a version 9 database, so going back is the restore of wwd.db.pre-<version>
// (docs/wwd-deploy.md, Roll back).
func TestAVersionEightDatabaseGainsStartedAt(t *testing.T) {
	f := newFixture(t)
	old := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	_, err := f.db.Exec(`ALTER TABLE jobs DROP COLUMN started_at`)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE queue_schema SET version = 8`)
	require.NoError(t, err)

	require.NoError(t, Migrate(bg, f.db))
	require.NoError(t, Migrate(bg, f.db), "the step can run again")

	var v, n int
	require.NoError(t, f.db.QueryRow(`SELECT version FROM queue_schema`).Scan(&v))
	assert.Equal(t, schemaVersion, v)
	assert.Equal(t, 9, v)
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM pragma_table_info('jobs') WHERE name = 'started_at'`).Scan(&n))
	assert.Equal(t, 1, n)
	assert.Nil(t, f.started(t, old), "the jobs it already held have none")

	c := f.claim(t, old, "air26")
	_, err = f.q.Start(bg, old, c.ClaimID)
	require.NoError(t, err)
	assert.NotNil(t, f.started(t, old))
}

// A database that already has the column (a new one) is not migrated into an error.
func TestAFreshDatabaseHasStartedAtAndMigrationLeavesItAlone(t *testing.T) {
	f := newFixture(t)
	var n int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM pragma_table_info('jobs') WHERE name = 'started_at'`).Scan(&n))
	assert.Equal(t, 1, n)
	_, err := f.db.Exec(`UPDATE queue_schema SET version = 8`)
	require.NoError(t, err)
	require.NoError(t, Migrate(bg, f.db), "the column is looked for first")
}
