package queue

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

func (f *fixture) after(t *testing.T, cursor int64, flt ...Filter) ([]wire.Job, int64) {
	t.Helper()
	var fl Filter
	if len(flt) > 0 {
		fl = flt[0]
	}
	js, next, err := f.q.ListAfter(bg, cursor, fl)
	require.NoError(t, err)
	return js, next
}

func (f *fixture) head(t *testing.T) int64 {
	t.Helper()
	h, err := f.q.Head(bg)
	require.NoError(t, err)
	return h
}

func (f *fixture) noKey() func(*wire.JobSubmission) {
	return func(s *wire.JobSubmission) { s.SupersedeKey = "" }
}

// The cursor is the origin's own sequence of changes: every new job gets the next number, a read from 0 returns everything in that order,
// and the cursor it answers is the number of the last job in the answer. Asked again with it, nothing changed, so nothing comes back and
// the cursor stays.
func TestListAfterReturnsJobsInTheOrderOfChange(t *testing.T) {
	f := newFixture(t)
	a, b, c := f.submit(t, f.noKey()), f.submit(t, f.noKey()), f.submit(t, f.noKey())

	js, cur := f.after(t, 0)
	assert.Equal(t, []string{a, b, c}, ids(js))
	assert.Equal(t, f.head(t), cur)
	assert.Greater(t, cur, int64(0))

	js, again := f.after(t, cur)
	assert.Empty(t, js)
	assert.NotNil(t, js, "an empty answer is a list, not null")
	assert.Equal(t, cur, again, "nothing new: the cursor stays where it was")
}

// A job that changes moves to the end of the sequence and is returned once, in the state it is in now, however many times it changed
// since the cursor (claim and start are two changes, one entry). Mutation gate: no trigger → the claim is not seen.
func TestAChangedJobIsReturnedOnceInItsLatestState(t *testing.T) {
	f := newFixture(t)
	a, b := f.submit(t, f.noKey()), f.submit(t, f.noKey())
	_, cur := f.after(t, 0)

	c := f.claim(t, a, "air26")
	_, err := f.q.Start(bg, a, c.ClaimID)
	require.NoError(t, err)

	js, next := f.after(t, cur)
	require.Len(t, js, 1)
	assert.Equal(t, a, js[0].ID)
	assert.Equal(t, wire.StateRunning, js[0].State)
	assert.Greater(t, next, cur)

	// the whole sequence now ends with a: b first, in the order of last change
	js, _ = f.after(t, 0)
	assert.Equal(t, []string{b, a}, ids(js))
}

// What does not change a job's state, result, cancellation, supersession, log or claim does not move the cursor: a heartbeat only renews
// the lease, so a client reading with ?after= is not woken by it. The things that do change the job each move it.
func TestOnlyChangesAClientCanSeeMoveTheCursor(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t, f.noKey())
	c := f.claim(t, id, "air26")
	_, err := f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)
	before := f.head(t)

	f.clk.Advance(15 * time.Second)
	_, err = f.q.Heartbeat(bg, id, c.ClaimID)
	require.NoError(t, err)
	assert.Equal(t, before, f.head(t), "a heartbeat is not a change")

	moved := func(what string, do func()) {
		t.Helper()
		h := f.head(t)
		do()
		assert.Greater(t, f.head(t), h, what)
	}
	moved("a start (state alone changes: the claim is already there)", func() {
		s := f.submit(t, f.noKey())
		sc := f.claim(t, s, "air26")
		h := f.head(t)
		_, err := f.q.Start(bg, s, sc.ClaimID)
		require.NoError(t, err)
		assert.Greater(t, f.head(t), h)
	})
	moved("a cancel request", func() { _, err := f.q.Cancel(bg, id); require.NoError(t, err) })
	moved("a log upload", func() { require.NoError(t, f.putLog(t, id, c.ClaimID, "x")) })
	moved("the result", func() {
		_, err := f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateCancelled})
		require.NoError(t, err)
	})
	moved("a supersede of a running job", func() {
		old := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "k"; s.Interruptible = false })
		oc := f.claim(t, old, "air26")
		_, err := f.q.Start(bg, old, oc.ClaimID)
		require.NoError(t, err)
		h := f.head(t)
		f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "k" })
		assert.Greater(t, f.head(t), h)
	})
	moved("a lapsed lease", func() {
		l := f.submit(t, f.noKey())
		f.claim(t, l, "air26")
		h := f.head(t)
		f.clk.Advance(61 * time.Second)
		_, err := f.q.Sweep(bg)
		require.NoError(t, err)
		assert.Greater(t, f.head(t), h)
	})
}

// The cursor is kept by the origin and is never reused: a job deleted at its 7 days (the one with the highest number) does not let the
// next job take its number, so a client holding that cursor still sees the next job. Mutation gate: rev = max(rev)+1 reuses it.
func TestACursorIsNeverReused(t *testing.T) {
	f := newFixture(t)
	f.submit(t, f.noKey())
	last := f.submit(t, f.noKey())
	_, cur := f.after(t, 0)

	_, err := f.db.Exec(`DELETE FROM jobs WHERE id = ?`, last)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, f.head(t), cur, "the head does not go back when the newest job is deleted")
	next := f.submit(t, f.noKey())

	js, nc := f.after(t, cur)
	assert.Equal(t, []string{next}, ids(js))
	assert.Greater(t, nc, cur)
}

// A limit takes the oldest changes first and the cursor is the last one taken, so reading in pieces misses nothing and repeats nothing.
func TestListAfterInPieces(t *testing.T) {
	f := newFixture(t)
	var all []string
	for i := 0; i < 5; i++ {
		all = append(all, f.submit(t, f.noKey()))
	}
	var got []string
	cur := int64(0)
	for round := 0; round < 10; round++ {
		js, next := f.after(t, cur, Filter{Limit: 2})
		if len(js) == 0 {
			break
		}
		assert.LessOrEqual(t, len(js), 2)
		got = append(got, ids(js)...)
		cur = next
	}
	assert.Equal(t, all, got)
}

// The state filter and the peers' view apply to a cursor read as to a list; the cursor moves only over what is returned, so a job a peer
// cannot see is not skipped for the day it becomes visible, and the cursor does not leak that it exists.
func TestListAfterAppliesTheFilters(t *testing.T) {
	f := newFixture(t)
	plain := f.submit(t, f.noKey())
	adhoc := f.submit(t, func(s *wire.JobSubmission) { s.AdHoc = true; s.SupersedeKey = "" })
	local := f.recordLocal(t)

	js, cur := f.after(t, 0, Filter{ForPeers: true})
	assert.Equal(t, []string{plain}, ids(js))

	js, _ = f.after(t, 0)
	assert.ElementsMatch(t, []string{plain, adhoc, local.ID}, ids(js))

	js, _ = f.after(t, 0, Filter{States: []wire.JobState{wire.StateRunning}})
	assert.Equal(t, []string{local.ID}, ids(js))

	js, same := f.after(t, cur, Filter{ForPeers: true})
	assert.Empty(t, js, "only hidden jobs changed")
	assert.Equal(t, cur, same)
}

func TestCursorFormat(t *testing.T) {
	assert.Equal(t, "0", CursorString(0))
	assert.Equal(t, "1234", CursorString(1234))
	n, err := ParseCursor("1234")
	require.NoError(t, err)
	assert.EqualValues(t, 1234, n)
	n, err = ParseCursor("")
	require.NoError(t, err)
	assert.EqualValues(t, 0, n, "no cursor reads from the start")
	for _, bad := range []string{"-1", "x", "1.5", " 2", "99999999999999999999"} {
		_, err := ParseCursor(bad)
		assert.Error(t, err, bad)
	}
}

// A version 3 database (3b-3a, on main) is upgraded in place: every job gets a number in the order it was made, the counter starts above
// the highest, a job made afterwards is after all of them, and running the migration again changes nothing. Mutation gates: no backfill →
// the old jobs all read as 0; counter at 0 → the next job collides with an old one.
func TestAVersionThreeDatabaseGainsTheCursor(t *testing.T) {
	ctx := bg
	f := newFixture(t)
	a, b := f.submit(t, f.noKey()), f.submit(t, f.noKey())
	// what version 3 had: no rev column, no counter, no triggers
	for _, stmt := range []string{
		`DROP TRIGGER jobs_rev_ins`, `DROP TRIGGER jobs_rev_upd`, `DROP TABLE job_rev`,
		`DROP INDEX jobs_rev`, `ALTER TABLE jobs DROP COLUMN rev`, `UPDATE queue_schema SET version = 3`,
	} {
		_, err := f.db.Exec(stmt)
		require.NoError(t, err, stmt)
	}

	require.NoError(t, Migrate(ctx, f.db))
	require.NoError(t, Migrate(ctx, f.db))
	var v int
	require.NoError(t, f.db.QueryRow(`SELECT version FROM queue_schema`).Scan(&v))
	assert.Equal(t, schemaVersion, v)

	js, cur := f.after(t, 0)
	assert.Equal(t, []string{a, b}, ids(js), "in the order they were made")
	assert.Equal(t, f.head(t), cur)

	c := f.submit(t, f.noKey())
	js, next := f.after(t, cur)
	assert.Equal(t, []string{c}, ids(js))
	assert.Greater(t, next, cur)

	cl := f.claim(t, a, "air26")
	_, err := f.q.Start(bg, a, cl.ClaimID)
	require.NoError(t, err)
	js, _ = f.after(t, next)
	assert.Equal(t, []string{a}, ids(js), "and the triggers work on it")
}
