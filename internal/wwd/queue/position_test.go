package queue

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

func (f *fixture) distinct(t *testing.T) string {
	t.Helper()
	return f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
}

func manualOf(t *testing.T, f *fixture, id string) (wire.Position, int64) {
	t.Helper()
	j := f.get(t, id)
	return j.Manual, j.ManualSeq
}

// A new job has no manual position. Front puts a job ahead of every job that is already in front (a smaller manual_seq), back behind
// every one already in back (a bigger one); normal puts it back among the scored jobs. The change is one event with the new fields.
func TestPositionFrontBackNormal(t *testing.T) {
	f := newFixture(t)
	a, b, c := f.distinct(t), f.distinct(t), f.distinct(t)
	m, s := manualOf(t, f, a)
	assert.Equal(t, wire.Position(""), m)
	assert.Zero(t, s)

	f.ev.reset()
	j, err := f.q.SetPosition(bg, a, wire.PositionFront)
	require.NoError(t, err)
	assert.Equal(t, wire.PositionFront, j.Manual)
	require.Len(t, f.ev.all(), 1)
	assert.Equal(t, wire.PositionFront, f.ev.all()[0].Job.Manual)
	first := j.ManualSeq

	j, err = f.q.SetPosition(bg, b, wire.PositionFront)
	require.NoError(t, err)
	assert.Less(t, j.ManualSeq, first, "a later front goes ahead of the earlier one")

	j, err = f.q.SetPosition(bg, c, wire.PositionBack)
	require.NoError(t, err)
	assert.Equal(t, wire.PositionBack, j.Manual)
	lastBack := j.ManualSeq
	j, err = f.q.SetPosition(bg, a, wire.PositionBack)
	require.NoError(t, err)
	assert.Greater(t, j.ManualSeq, lastBack, "a later back goes behind the earlier one")

	f.ev.reset()
	j, err = f.q.SetPosition(bg, a, wire.PositionNormal)
	require.NoError(t, err)
	assert.Equal(t, wire.Position(""), j.Manual, "normal is no manual position")
	assert.Zero(t, j.ManualSeq)
	assert.Len(t, f.ev.all(), 1)
}

// Asking for the position a job already has changes nothing and announces nothing.
func TestPositionRepeatIsQuiet(t *testing.T) {
	f := newFixture(t)
	a := f.distinct(t)
	for _, p := range []wire.Position{wire.PositionHold, wire.PositionFront, wire.PositionBack} {
		_, err := f.q.SetPosition(bg, a, p)
		require.NoError(t, err)
		before := f.get(t, a)
		f.ev.reset()
		_, err = f.q.SetPosition(bg, a, p)
		require.NoError(t, err, string(p))
		assert.Empty(t, f.ev.all(), string(p)+": nothing announced")
		assert.Equal(t, before.ManualSeq, f.get(t, a).ManualSeq, string(p)+": the place is kept")
	}
	_, err := f.q.SetPosition(bg, a, wire.PositionNormal)
	require.NoError(t, err)
	f.ev.reset()
	_, err = f.q.SetPosition(bg, a, wire.PositionNormal)
	require.NoError(t, err)
	assert.Empty(t, f.ev.all(), "normal on a normal job")
}

// A held job is not handed to a worker: its claim is a conflict naming queued, and it is claimable
// again once released. The hold is on the job, not on a worker.
func TestHeldJobCannotBeClaimed(t *testing.T) {
	f := newFixture(t)
	a := f.distinct(t)
	_, err := f.q.SetPosition(bg, a, wire.PositionHold)
	require.NoError(t, err)

	_, err = f.q.Claim(bg, a, creq("w1"))
	assert.Equal(t, wire.StateQueued, conflictState(t, err), "a conflict that names queued: the job is there, not for the taking")
	_, err = f.q.ClaimForPeers(bg, a, creq("w1"))
	assert.Equal(t, wire.StateQueued, conflictState(t, err))
	assert.Equal(t, wire.StateQueued, f.get(t, a).State, "still queued")

	_, err = f.q.SetPosition(bg, a, wire.PositionNormal)
	require.NoError(t, err)
	f.claim(t, a, "w1")
}

// A front or back job is claimable (only hold stops a claim); and putting a held job in front or back lets it go.
func TestFrontAndBackJobsAreClaimable(t *testing.T) {
	f := newFixture(t)
	a, b := f.distinct(t), f.distinct(t)
	_, err := f.q.SetPosition(bg, a, wire.PositionFront)
	require.NoError(t, err)
	f.claim(t, a, "w1")

	_, err = f.q.SetPosition(bg, b, wire.PositionHold)
	require.NoError(t, err)
	j, err := f.q.SetPosition(bg, b, wire.PositionBack)
	require.NoError(t, err)
	assert.Equal(t, wire.PositionBack, j.Manual)
	f.claim(t, b, "w2")
}

// Only a queued job has a place in the queue: any other state is a conflict naming it, an unknown id is not found, and a bad
// position is refused before anything is looked up.
func TestPositionOnlyForQueuedJobs(t *testing.T) {
	f := newFixture(t)
	for _, st := range []wire.JobState{wire.StateClaimed, wire.StateRunning, wire.StateSucceeded, wire.StateCancelled} {
		id := f.distinct(t)
		f.seed(t, id, st, "")
		f.ev.reset()
		_, err := f.q.SetPosition(bg, id, wire.PositionFront)
		assert.Equal(t, st, conflictState(t, err))
		assert.Empty(t, f.ev.all())
	}
	_, err := f.q.SetPosition(bg, "j_nope", wire.PositionFront)
	assert.ErrorIs(t, err, ErrNotFound)
	id := f.distinct(t)
	_, err = f.q.SetPosition(bg, id, wire.Position("sideways"))
	assert.ErrorIs(t, err, ErrInvalidPosition)
}

// A job that goes back to the queue (a worker released it) keeps the place a person gave it, and the position survives a restart:
// it is in the table.
func TestPositionSurvivesReleaseAndReopen(t *testing.T) {
	f := newFixture(t)
	a := f.distinct(t)
	_, err := f.q.SetPosition(bg, a, wire.PositionFront)
	require.NoError(t, err)
	r := f.claim(t, a, "w1")
	require.NoError(t, f.q.Release(bg, a, wire.ReleaseRequest{ClaimID: r.ClaimID, Reason: wire.ReleaseReclaim}))
	j := f.get(t, a)
	assert.Equal(t, wire.StateQueued, j.State)
	assert.Equal(t, wire.PositionFront, j.Manual)
}
