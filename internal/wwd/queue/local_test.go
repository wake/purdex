package queue

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

func (f *fixture) localReq(edit ...func(*wire.JobSubmission)) wire.LocalRunRequest {
	edit = append([]func(*wire.JobSubmission){func(s *wire.JobSubmission) {
		s.GitDir = f.gitDir
		s.Cacheable = false
		s.SupersedeKey = ""
	}}, edit...)
	return wire.LocalRunRequest{JobSubmission: sub(edit...)}
}

func (f *fixture) recordLocal(t *testing.T, edit ...func(*wire.JobSubmission)) wire.LocalRunResponse {
	t.Helper()
	r, err := f.q.RecordLocal(bg, f.localReq(edit...), "mlab")
	require.NoError(t, err)
	return r
}

// A fast-lane run is recorded as a running job with local set, the claim already made: no snapshot is pinned (the run uses the
// submitter's own tree), one event is announced, and the answer carries the claim and the lease.
func TestRecordLocalCreatesARunningLocalJob(t *testing.T) {
	f := newFixture(t)
	f.ev.reset()
	r := f.recordLocal(t)
	require.NoError(t, r.Validate())
	assert.Equal(t, 60, r.LeaseS)
	assert.Equal(t, 15, r.RenewS)

	j := f.get(t, r.ID)
	assert.Equal(t, wire.StateRunning, j.State)
	assert.True(t, j.Local)
	assert.False(t, j.Cacheable)
	require.NotNil(t, j.Claim)
	assert.Equal(t, "mlab", j.Claim.Worker)
	assert.Equal(t, 1, j.Claim.Attempt)
	assert.Equal(t, f.clk.Now(), j.Claim.Since)
	assert.Equal(t, ms(f.clk.Now().Add(60*time.Second)), f.leaseDeadline(t, r.ID))
	require.NoError(t, j.Validate())
	assert.False(t, f.pins.has(f.gitDir, r.ID), "nothing is pinned for a fast-lane run")
	var pinned int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM git_pins WHERE id = ?`, r.ID).Scan(&pinned))
	assert.Equal(t, 0, pinned, "and nothing is recorded to release")

	evs := f.ev.all()
	require.Len(t, evs, 1)
	assert.Equal(t, wire.StateRunning, evs[0].Job.State)
	assert.True(t, evs[0].Job.Local)
}

// A fast-lane run stays on its host: peers neither list it, read it nor claim it. It supersedes nothing, whatever key it carries.
func TestLocalJobsAreInvisibleToPeersAndSupersedeNothing(t *testing.T) {
	f := newFixture(t)
	queued := f.submit(t) // key wt1:spa-related
	r := f.recordLocal(t, func(s *wire.JobSubmission) { s.SupersedeKey = "wt1:spa-related" })

	assert.Equal(t, wire.StateQueued, f.get(t, queued).State, "a fast-lane run does not supersede a queued job")
	_, err := f.q.GetForPeers(bg, r.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	list, err := f.q.List(bg, Filter{ForPeers: true})
	require.NoError(t, err)
	for _, j := range list {
		assert.NotEqual(t, r.ID, j.ID)
	}
	_, err = f.q.CancelForPeers(bg, r.ID)
	assert.ErrorIs(t, err, ErrNotFound)
}

// The run goes through the ordinary claim operations with its claim_id: heartbeat answers cancel after a Cancel, and the result ends it.
func TestLocalJobFollowsTheOrdinaryClaimOperations(t *testing.T) {
	f := newFixture(t)
	r := f.recordLocal(t)
	f.clk.Advance(15 * time.Second)
	h, err := f.q.Heartbeat(bg, r.ID, r.ClaimID)
	require.NoError(t, err)
	assert.False(t, h.Cancel)
	_, err = f.q.Cancel(bg, r.ID)
	require.NoError(t, err)
	h, err = f.q.Heartbeat(bg, r.ID, r.ClaimID)
	require.NoError(t, err)
	assert.True(t, h.Cancel)

	j, err := f.q.Result(bg, r.ID, wire.ResultRequest{ClaimID: r.ClaimID, State: wire.StateCancelled})
	require.NoError(t, err)
	assert.Equal(t, wire.StateCancelled, j.State)
	assert.True(t, j.Local)
}

// A run whose runner disappears is failed as lost when its lease runs out, and not put back in the queue: nobody else could run it.
func TestLocalJobWhoseLeaseRunsOutFailsAsLost(t *testing.T) {
	f := newFixture(t)
	r := f.recordLocal(t)
	f.clk.Advance(61 * time.Second)
	_, err := f.q.Sweep(bg)
	require.NoError(t, err)
	j := f.get(t, r.ID)
	assert.Equal(t, wire.StateFailed, j.State)
	assert.Equal(t, "lost", j.Result.Reason)
}

func TestRecordLocalRefusesAnInvalidRequest(t *testing.T) {
	f := newFixture(t)
	_, err := f.q.RecordLocal(bg, f.localReq(func(s *wire.JobSubmission) { s.Cacheable = true }), "mlab")
	assert.Error(t, err, "a fast-lane run is never cacheable")
	_, err = f.q.RecordLocal(bg, f.localReq(func(s *wire.JobSubmission) { s.GitDir = "relative" }), "mlab")
	assert.Error(t, err)
	_, err = f.q.RecordLocal(bg, f.localReq(), "")
	assert.Error(t, err, "the run needs a worker name")
	var n int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&n))
	assert.Equal(t, 0, n)
}

// Ended says which ids are over: a final state, or a job the queue no longer has (kept 7 days, then deleted). A live one is not.
func TestEnded(t *testing.T) {
	f := newFixture(t)
	live := f.recordLocal(t)
	done := f.recordLocal(t)
	_, err := f.q.Result(bg, done.ID, wire.ResultRequest{ClaimID: done.ClaimID, State: wire.StateSucceeded})
	require.NoError(t, err)
	queued := f.submit(t)

	got, err := f.q.Ended(bg, []string{live.ID, done.ID, queued, "j_gone"})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{live.ID: false, done.ID: true, queued: false, "j_gone": true}, got)

	got, err = f.q.Ended(bg, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// More ids than one SQL statement takes are answered in pieces.
func TestEndedWithManyIDs(t *testing.T) {
	f := newFixture(t)
	// a job that is not over sits first, last and on each side of the piece boundaries, so a piece that is skipped or cut short shows
	var live []string
	for i := 0; i < 5; i++ {
		live = append(live, f.recordLocal(t).ID)
	}
	var ids []string
	for i := 0; i < 1500; i++ {
		switch i {
		case 0:
			ids = append(ids, live[0])
		case endedChunk - 1:
			ids = append(ids, live[1])
		case endedChunk:
			ids = append(ids, live[2])
		case 2*endedChunk + 1:
			ids = append(ids, live[3])
		case 1499:
			ids = append(ids, live[4])
		default:
			ids = append(ids, fmt.Sprintf("j_gone%d", i))
		}
	}
	got, err := f.q.Ended(bg, ids)
	require.NoError(t, err)
	require.Len(t, got, 1500)
	for _, id := range live {
		assert.False(t, got[id], id)
	}
	assert.True(t, got["j_gone7"])
}

// LiveClaim is the snapshot route's question: it answers for the live claim only, with what the route needs, and a stale or unknown claim
// is the 409 with the job's state. A fast-lane job has no snapshot to serve, so it is not found.
func TestLiveClaim(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	c := f.claim(t, id, "air26")

	lc, err := f.q.LiveClaim(bg, id, c.ClaimID)
	require.NoError(t, err)
	assert.Equal(t, LiveClaim{State: wire.StateClaimed, GitDir: f.gitDir, Commit: "c0ffee", Base: "ba5e"}, lc)

	_, err = f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)
	lc, err = f.q.LiveClaim(bg, id, c.ClaimID)
	require.NoError(t, err)
	assert.Equal(t, wire.StateRunning, lc.State)

	_, err = f.q.LiveClaim(bg, id, wire.NewClaimID())
	assert.Equal(t, wire.StateRunning, conflictState(t, err))
	_, err = f.q.LiveClaim(bg, "j_nope", c.ClaimID)
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, f.q.Release(bg, id, wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: wire.ReleaseReclaim}))
	_, err = f.q.LiveClaim(bg, id, c.ClaimID)
	assert.Equal(t, wire.StateQueued, conflictState(t, err), "a released claim is stale")

	local := f.recordLocal(t)
	_, err = f.q.LiveClaim(bg, local.ID, local.ClaimID)
	assert.ErrorIs(t, err, ErrNotFound)
}
