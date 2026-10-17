package queue

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Cancelling a queued job ends it at once: nothing ran, so there is no exit (spec 4.2: queued → cancelled by the origin). One event.
func TestCancelOfAQueuedJob(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	f.ev.reset()

	j, err := f.q.Cancel(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, wire.StateCancelled, j.State)
	require.NotNil(t, j.Result)
	assert.Nil(t, j.Result.Exit)
	assert.Equal(t, "cancelled", j.Result.Reason)
	require.NotNil(t, j.Result.FinishedAt)
	assert.Equal(t, f.clk.Now(), *j.Result.FinishedAt)
	assert.Equal(t, wire.StateCancelled, f.get(t, id).State)
	require.NoError(t, j.Validate())

	evs := f.ev.all()
	require.Len(t, evs, 1)
	assert.Equal(t, wire.StateCancelled, evs[0].Job.State)
}

// A job a worker holds (claimed or running) cannot be ended by the origin: it is told to stop at its next heartbeat and posts its own
// result, so the state stays and cancel_requested is set. Asking twice is the same as asking once.
func TestCancelOfAHeldJobAsksTheWorkerToStop(t *testing.T) {
	f := newFixture(t)
	for _, st := range []wire.JobState{wire.StateClaimed, wire.StateRunning} {
		id := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
		f.seed(t, id, st, "")
		f.ev.reset()

		j, err := f.q.Cancel(context.Background(), id)
		require.NoError(t, err, string(st))
		assert.Equal(t, st, j.State, "the worker decides when it ends")
		assert.True(t, f.cancelRequested(t, id), string(st))
		assert.Len(t, f.ev.all(), 1, "one event for the request")

		_, err = f.q.Cancel(context.Background(), id)
		require.NoError(t, err, "a second request is not an error")
		assert.True(t, f.cancelRequested(t, id))
	}
}

// A job that has ended cannot be cancelled: 409 with its state, nothing changes and nothing is published. An unknown id is not found.
func TestCancelOfAFinishedOrUnknownJob(t *testing.T) {
	f := newFixture(t)
	for _, st := range []wire.JobState{wire.StateSucceeded, wire.StateFailed, wire.StateCancelled, wire.StateSuperseded, wire.StateExpired} {
		id := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
		f.seed(t, id, st, "")
		f.ev.reset()

		_, err := f.q.Cancel(context.Background(), id)
		var c *ConflictError
		require.True(t, errors.As(err, &c), "%s: %v", st, err)
		assert.Equal(t, st, c.State)
		assert.Equal(t, st, f.get(t, id).State, "nothing changed")
		assert.False(t, f.cancelRequested(t, id))
		assert.Empty(t, f.ev.all(), "nothing published")
	}
	_, err := f.q.Cancel(context.Background(), "j_nope")
	assert.ErrorIs(t, err, ErrNotFound)
}

// A fast-lane (local) job is the submitter's own run on this host: cancelling it works like any held job. And a peer may not ask the
// origin to cancel what it cannot see: CancelForPeers finds no ad-hoc or local job.
func TestCancelForPeersCannotReachAdHocOrLocalJobs(t *testing.T) {
	f := newFixture(t)
	normal := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	adhoc := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey, s.AdHoc = "", true })
	local := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	_, err := f.db.Exec(`UPDATE jobs SET local = 1 WHERE id = ?`, local)
	require.NoError(t, err)
	ctx := context.Background()

	_, err = f.q.CancelForPeers(ctx, adhoc)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = f.q.CancelForPeers(ctx, local)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, wire.StateQueued, f.get(t, adhoc).State, "untouched")
	assert.Equal(t, wire.StateQueued, f.get(t, local).State, "untouched")

	j, err := f.q.CancelForPeers(ctx, normal)
	require.NoError(t, err)
	assert.Equal(t, wire.StateCancelled, j.State)
}
