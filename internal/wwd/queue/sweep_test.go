package queue

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A queued job that has waited longer than its queue timeout (the task's, copied in at submission; 2 hours when it is 0) expires: it
// never ran, the submitter is told by the event. Strictly longer: at the timeout itself it is still waiting. Mutation gates:
// the default changed → the 2 h row; the comparison made >= → the boundary row.
func TestSweepExpiresQueuedJobsPastTheirQueueTimeout(t *testing.T) {
	f := newFixture(t)
	short := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey, s.QueueTimeoutS = "", 60 })
	dflt := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	ctx := context.Background()

	f.clk.Advance(60 * time.Second)
	res, err := f.q.Sweep(ctx)
	require.NoError(t, err)
	assert.Zero(t, res.Expired, "at exactly the timeout the job still waits")
	assert.Equal(t, wire.StateQueued, f.get(t, short).State)

	f.clk.Advance(time.Second)
	f.ev.reset()
	res, err = f.q.Sweep(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Expired)
	j := f.get(t, short)
	assert.Equal(t, wire.StateExpired, j.State)
	require.NotNil(t, j.Result)
	assert.Equal(t, "queue_timeout", j.Result.Reason)
	assert.Nil(t, j.Result.Exit, "nothing ran")
	require.NoError(t, j.Validate())
	assert.Equal(t, wire.StateQueued, f.get(t, dflt).State, "the default is 2 hours")
	evs := f.ev.all()
	require.Len(t, evs, 1)
	assert.Equal(t, short, evs[0].Job.ID)
	assert.Equal(t, wire.StateExpired, evs[0].Job.State)

	f.clk.Advance(2 * time.Hour)
	res, err = f.q.Sweep(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Expired)
	assert.Equal(t, wire.StateExpired, f.get(t, dflt).State)
	assert.False(t, f.pins.has(f.gitDir, short), "an expired job's snapshot is released")
}

// Only a queued job expires: one a worker holds is its worker's business, and one that ended is not touched again.
func TestSweepExpiresOnlyQueuedJobs(t *testing.T) {
	f := newFixture(t)
	var ids []string
	for _, st := range []wire.JobState{wire.StateClaimed, wire.StateRunning, wire.StateSucceeded, wire.StateFailed, wire.StateCancelled} {
		id := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey, s.QueueTimeoutS = "", 1 })
		f.seed(t, id, st, "")
		ids = append(ids, id)
	}
	f.clk.Advance(time.Hour)
	res, err := f.q.Sweep(context.Background())
	require.NoError(t, err)
	assert.Zero(t, res.Expired)
	for i, st := range []wire.JobState{wire.StateClaimed, wire.StateRunning, wire.StateSucceeded, wire.StateFailed, wire.StateCancelled} {
		assert.Equal(t, st, f.get(t, ids[i]).State)
	}
}

// Jobs and results are kept 7 days after they ended, then deleted; a job that has not ended is never deleted, however old; a job whose
// snapshot is still pinned is kept until the pin is released (the sweep tries again). Mutation gates: 7 days changed; the state
// condition dropped; the pinned condition dropped.
func TestSweepDeletesEndedJobsAfterSevenDays(t *testing.T) {
	f := newFixture(t)
	ended := func(age time.Duration) string {
		id := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
		f.seed(t, id, wire.StateSucceeded, "finished_at = ?, pinned = 0", ms(f.clk.Now().Add(-age)))
		return id
	}
	young := ended(7*24*time.Hour - time.Minute)
	old := ended(7*24*time.Hour + time.Minute)
	live := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	f.seed(t, live, wire.StateRunning, "created_at = ?", ms(f.clk.Now().Add(-30*24*time.Hour)))
	stuck := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	f.seed(t, stuck, wire.StateFailed, "finished_at = ?", ms(f.clk.Now().Add(-30*24*time.Hour)))
	f.pins.unErr = assert.AnError

	res, err := f.q.Sweep(context.Background())
	require.NoError(t, err, "a failed unpin is retried, not an error of the sweep")
	assert.Equal(t, 1, res.Deleted)
	_, err = f.q.Get(context.Background(), old)
	assert.ErrorIs(t, err, ErrNotFound)
	for _, id := range []string{young, live, stuck} {
		_, err := f.q.Get(context.Background(), id)
		assert.NoError(t, err, id)
	}

	f.pins.unErr = nil
	res, err = f.q.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Deleted, "the pin came off, so the old row can go")
	_, err = f.q.Get(context.Background(), stuck)
	assert.ErrorIs(t, err, ErrNotFound)
}

// A sweep with nothing to do changes and announces nothing; running it again and again is the normal case.
func TestSweepWithNothingToDoDoesNothing(t *testing.T) {
	f := newFixture(t)
	f.submit(t)
	f.ev.reset()
	for i := 0; i < 3; i++ {
		res, err := f.q.Sweep(context.Background())
		require.NoError(t, err)
		assert.Equal(t, SweepResult{}, res)
	}
	assert.Empty(t, f.ev.all())
}

// A sweep and submissions at once, under -race: nothing is lost, every expired job is announced once.
func TestSweepRacesSubmitsSafely(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 5; i++ {
		f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey, s.QueueTimeoutS = "", 1 })
	}
	f.clk.Advance(time.Minute)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5; i++ {
			_, err := f.q.Sweep(context.Background())
			assert.NoError(t, err)
		}
	}()
	for i := 0; i < 10; i++ {
		_, err := f.q.Submit(context.Background(), sub(func(s *wire.JobSubmission) { s.SupersedeKey = "" }))
		require.NoError(t, err)
	}
	<-done
	expired := map[string]int{}
	for _, e := range f.ev.all() {
		if e.Job.State == wire.StateExpired {
			expired[e.Job.ID]++
		}
	}
	assert.Len(t, expired, 5)
	for id, n := range expired {
		assert.Equal(t, 1, n, "%s announced once", id)
	}
}
