package queue

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A submission becomes a queued job with every field kept, an id and a created_at of the origin's, and one event.
func TestSubmitStoresTheJobAndPublishesOneEvent(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t, func(s *wire.JobSubmission) { s.Interruptible, s.QueueTimeoutS = true, 3600 })

	assert.True(t, strings.HasPrefix(id, "j_"), id)
	j := f.get(t, id)
	assert.Equal(t, id, j.ID)
	assert.Equal(t, "mlab", j.Origin)
	assert.Equal(t, wire.StateQueued, j.State)
	assert.Equal(t, f.clk.Now(), j.CreatedAt)
	assert.Nil(t, j.Claim)
	assert.Nil(t, j.Result)
	assert.Equal(t, "purdex", j.Repo.Name)
	assert.Equal(t, "git@github.com:wake/purdex.git", j.Repo.Remote)
	assert.Equal(t, wire.Snapshot{Commit: "c0ffee", Tree: "7ee", Base: "ba5e", RelCwd: "spa"}, j.Snapshot)
	assert.Equal(t, "spa-related", j.Task)
	assert.Equal(t, []string{"src/a.ts"}, j.Args)
	assert.Equal(t, wire.ClassMerge, j.Class)
	assert.Equal(t, []string{"os:darwin", "repo:purdex"}, j.Labels)
	assert.True(t, j.Cacheable)
	assert.Equal(t, 1800, j.TimeoutS)
	assert.Equal(t, "wt1:spa-related", j.SupersedeKey)
	assert.True(t, j.Interruptible)
	assert.Equal(t, 3600, j.QueueTimeoutS)
	assert.False(t, j.AdHoc)
	assert.Equal(t, wire.Submitter{Session: "s1", Ref: "_abc123", Team: "wwd", Host: "mlab"}, j.Submitter)
	require.NoError(t, j.Validate())

	evs := f.ev.all()
	require.Len(t, evs, 1)
	assert.Equal(t, "mlab", evs[0].Host)
	assert.Equal(t, id, evs[0].Job.ID)
}

// Empty lists come back as [], never null (the wire's "absent is null" rule is for values, a list is always a list).
func TestSubmitKeepsEmptyListsAsLists(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t, func(s *wire.JobSubmission) { s.Args, s.Labels, s.Locks = nil, nil, nil })
	b, err := json.Marshal(f.get(t, id))
	require.NoError(t, err)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))
	for _, k := range []string{"args", "labels", "locks"} {
		assert.Equal(t, "[]", string(m[k]), k)
	}
}

// A submission that fails validation stores nothing and publishes nothing; the origin owns id and state, so the type has no place to
// carry them (the wire rejects them at decode).
func TestSubmitRejectsAnInvalidSubmissionWithoutAnEvent(t *testing.T) {
	f := newFixture(t)
	for _, edit := range []func(*wire.JobSubmission){
		func(s *wire.JobSubmission) { s.Class = "urgent" },
		func(s *wire.JobSubmission) { s.Task = "" },
		func(s *wire.JobSubmission) { s.QueueTimeoutS = -1 },
	} {
		_, err := f.q.Submit(context.Background(), sub(edit))
		require.Error(t, err)
	}
	var n int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&n))
	assert.Zero(t, n)
	assert.Empty(t, f.ev.all())
}

// Ids are unique and sort in submission order (a ULID: time first), so a listing by id is a listing by age.
func TestSubmitIDsAreUniqueAndSortByTime(t *testing.T) {
	f := newFixture(t)
	var ids []string
	for i := 0; i < 20; i++ {
		f.clk.Advance(time.Millisecond)
		ids = append(ids, f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" }))
	}
	seen := map[string]bool{}
	for i, id := range ids {
		assert.False(t, seen[id], "duplicate id %s", id)
		seen[id] = true
		assert.Len(t, id, len("j_")+26)
		if i > 0 {
			assert.Less(t, ids[i-1], id, "ids sort by submission time")
		}
	}
}

// A newer job with the same supersede_key supersedes a queued older one at once; another key is left alone; a job with no key never
// supersedes or is superseded.
func TestSupersedeOfQueuedJobs(t *testing.T) {
	f := newFixture(t)
	old1 := f.submit(t)
	old2 := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "elsewhere" })
	f.seed(t, old2, wire.StateQueued, "supersede_key = ?", "wt1:spa-related") // two queued jobs under one key cannot arise through Submit alone
	other := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "wt2:spa-related" })
	nokey1 := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	f.ev.reset()

	newID := f.submit(t)

	for _, id := range []string{old1, old2} {
		j := f.get(t, id)
		assert.Equal(t, wire.StateSuperseded, j.State, id)
		require.NotNil(t, j.Result, id)
		require.NotNil(t, j.Result.SupersededBy, id)
		assert.Equal(t, newID, *j.Result.SupersededBy)
		assert.Equal(t, "superseded_by:"+newID, j.Result.Reason)
		assert.Nil(t, j.Result.Exit, "nothing ran")
		require.NoError(t, j.Validate())
	}
	assert.Equal(t, wire.StateQueued, f.get(t, newID).State)
	assert.Equal(t, wire.StateQueued, f.get(t, other).State, "another key")
	assert.Equal(t, wire.StateQueued, f.get(t, nokey1).State, "no key")

	f.ev.reset()
	nokey2 := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	assert.Equal(t, wire.StateQueued, f.get(t, nokey1).State, "a keyless newcomer supersedes nobody")
	assert.Equal(t, wire.StateQueued, f.get(t, nokey2).State)
}

// One event per changed job: the new job and each one it superseded.
func TestSupersedePublishesAnEventPerChangedJob(t *testing.T) {
	f := newFixture(t)
	old1 := f.submit(t)
	old2 := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "elsewhere" })
	f.seed(t, old2, wire.StateQueued, "supersede_key = ?", "wt1:spa-related") // two queued jobs under one key cannot arise through Submit alone
	f.ev.reset()
	newID := f.submit(t)
	got := map[string]string{}
	for _, e := range f.ev.all() {
		got[e.Job.ID] = string(e.Job.State)
	}
	assert.Equal(t, map[string]string{newID: "queued", old1: "superseded", old2: "superseded"}, got)
}

// A running older job: interruptible → the worker is told to stop at its next heartbeat (cancel_requested) and the job stays running
// until it posts; not interruptible → it runs to its end, and only superseded_by is recorded (spec 4.2). A terminal older job is not
// touched, and neither is one that is claimed by someone with another key.
func TestSupersedeOfRunningJobs(t *testing.T) {
	f := newFixture(t)
	intr := f.submit(t, func(s *wire.JobSubmission) { s.Interruptible = true })
	f.seed(t, intr, wire.StateRunning, "")
	solid := f.submit(t, func(s *wire.JobSubmission) { s.Interruptible = false })
	f.seed(t, solid, wire.StateRunning, "")
	claimed := f.submit(t, func(s *wire.JobSubmission) { s.Interruptible = true })
	f.seed(t, claimed, wire.StateClaimed, "")
	done := f.submit(t)
	f.seed(t, done, wire.StateSucceeded, "")
	f.ev.reset()

	newID := f.submit(t)

	assert.True(t, f.cancelRequested(t, intr), "interruptible running job is told to stop")
	assert.Equal(t, wire.StateRunning, f.get(t, intr).State, "it stays running until the worker posts")
	assert.True(t, f.cancelRequested(t, claimed), "a claimed interruptible job is told too")
	assert.False(t, f.cancelRequested(t, solid), "a non-interruptible job runs to its end")
	assert.Equal(t, wire.StateRunning, f.get(t, solid).State)
	assert.False(t, f.cancelRequested(t, done))
	assert.Equal(t, wire.StateSucceeded, f.get(t, done).State)

	for _, id := range []string{intr, solid, claimed} {
		var by string
		require.NoError(t, f.db.QueryRow(`SELECT coalesce(superseded_by,'') FROM jobs WHERE id=?`, id).Scan(&by))
		assert.Equal(t, newID, by, "%s records who superseded it", id)
	}
	var by string
	require.NoError(t, f.db.QueryRow(`SELECT coalesce(superseded_by,'') FROM jobs WHERE id=?`, done).Scan(&by))
	assert.Empty(t, by, "a finished job is never marked")
}

// Many submissions with the same key at once: exactly one stays queued (the last to commit), the others are superseded, ids are
// unique, and no event is lost. Run under -race.
func TestConcurrentSubmitsWithOneKeyLeaveOneQueued(t *testing.T) {
	f := newFixture(t)
	const n = 16
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := f.q.Submit(context.Background(), sub())
			if assert.NoError(t, err) {
				ids[i] = r.ID
			}
		}(i)
	}
	wg.Wait()

	uniq := map[string]bool{}
	for _, id := range ids {
		uniq[id] = true
	}
	assert.Len(t, uniq, n, "ids are unique")
	var queued, superseded int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM jobs WHERE state='queued'`).Scan(&queued))
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM jobs WHERE state='superseded'`).Scan(&superseded))
	assert.Equal(t, 1, queued)
	assert.Equal(t, n-1, superseded)
	assert.GreaterOrEqual(t, len(f.ev.all()), n, "at least one event per submission")
}
