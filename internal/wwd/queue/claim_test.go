package queue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

var bg = context.Background()

func creq(worker string) wire.ClaimRequest {
	return wire.ClaimRequest{Worker: worker, Labels: []string{"os:darwin", "repo:purdex"}}
}

// claim claims id as worker and fails the test if that is refused.
func (f *fixture) claim(t *testing.T, id, worker string) wire.ClaimResponse {
	t.Helper()
	r, err := f.q.Claim(bg, id, creq(worker))
	require.NoError(t, err)
	return r
}

func conflictState(t *testing.T, err error) wire.JobState {
	t.Helper()
	var ce *ConflictError
	require.True(t, errors.As(err, &ce), "want a ConflictError, got %v", err)
	return ce.State
}

func (f *fixture) leaseDeadline(t *testing.T, id string) int64 {
	t.Helper()
	var v int64
	require.NoError(t, f.db.QueryRow(`SELECT lease_deadline FROM jobs WHERE id = ?`, id).Scan(&v))
	return v
}

func (f *fixture) attempts(t *testing.T, id string) int {
	t.Helper()
	var v int
	require.NoError(t, f.db.QueryRow(`SELECT attempts FROM jobs WHERE id = ?`, id).Scan(&v))
	return v
}

// A claim moves queued → claimed, hands out a fresh claim_id and the default lease, shows the claim on the job, never shows the claim_id
// on the job, and announces one event.
func TestClaimOfAQueuedJob(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	f.ev.reset()

	r := f.claim(t, id, "air26")
	assert.True(t, wire.ValidClaimID(r.ClaimID))
	assert.Equal(t, 60, r.LeaseS)
	assert.Equal(t, 15, r.RenewS)
	assert.Equal(t, wire.StateClaimed, r.Job.State)
	require.NotNil(t, r.Job.Claim)
	assert.Equal(t, "air26", r.Job.Claim.Worker)
	assert.Equal(t, 1, r.Job.Claim.Attempt)
	assert.Equal(t, f.clk.Now(), r.Job.Claim.Since)
	assert.Equal(t, ms(f.clk.Now().Add(60*time.Second)), f.leaseDeadline(t, id), "the deadline is on the origin's clock")
	require.NoError(t, r.Validate())

	evs := f.ev.all()
	require.Len(t, evs, 1)
	assert.Equal(t, wire.StateClaimed, evs[0].Job.State)
}

// Only a queued job can be claimed: any other state is a 409 with that state, and nothing changes.
func TestClaimRefusedUnlessQueued(t *testing.T) {
	f := newFixture(t)
	for _, st := range []wire.JobState{wire.StateClaimed, wire.StateRunning, wire.StateSucceeded, wire.StateFailed, wire.StateCancelled, wire.StateSuperseded, wire.StateExpired} {
		id := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
		f.seed(t, id, st, "")
		f.ev.reset()
		_, err := f.q.Claim(bg, id, creq("air26"))
		assert.Equal(t, st, conflictState(t, err), string(st))
		assert.Equal(t, st, f.get(t, id).State)
		assert.Empty(t, f.ev.all())
	}
	_, err := f.q.Claim(bg, "j_nope", creq("air26"))
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = f.q.Claim(bg, f.submit(t), wire.ClaimRequest{})
	assert.Error(t, err, "a claim without a worker is refused")
}

// Two hosts racing for one job: exactly one wins, the others get the 409 for claimed (spec 4.2: first wins).
func TestConcurrentClaimsHaveOneWinner(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	const n = 16
	var wins, losses atomic.Int32
	ids := make(chan string, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := f.q.Claim(bg, id, creq("w"))
			var ce *ConflictError
			switch {
			case err == nil:
				wins.Add(1)
				ids <- r.ClaimID
			case errors.As(err, &ce) && ce.State == wire.StateClaimed:
				losses.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	assert.EqualValues(t, 1, wins.Load())
	assert.EqualValues(t, n-1, losses.Load())
	var stored string
	require.NoError(t, f.db.QueryRow(`SELECT claim_id FROM jobs WHERE id = ?`, id).Scan(&stored))
	assert.Equal(t, <-ids, stored, "the winner holds the stored claim_id")
}

// Ad-hoc and fast-lane jobs are never offered to a peer: a peer's claim of one is a 409 (spec 4.4, 11), and nothing changes.
func TestClaimForPeersRefusesAdHocAndLocal(t *testing.T) {
	f := newFixture(t)
	adhoc := f.submit(t, func(s *wire.JobSubmission) { s.AdHoc = true; s.SupersedeKey = "" })
	local := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	_, err := f.db.Exec(`UPDATE jobs SET local = 1 WHERE id = ?`, local)
	require.NoError(t, err)
	for _, id := range []string{adhoc, local} {
		_, err := f.q.ClaimForPeers(bg, id, creq("air26"))
		assert.ErrorIs(t, err, ErrNotFound, id)
		assert.Equal(t, wire.StateQueued, f.get(t, id).State)
	}
	// the host's own worker may take an ad-hoc job
	_, err = f.q.Claim(bg, adhoc, creq("mlab"))
	require.NoError(t, err)
	// and an ordinary job is claimable by a peer
	ok := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	_, err = f.q.ClaimForPeers(bg, ok, creq("air26"))
	require.NoError(t, err)
}

// claimed → running by start, with the claim; it renews the lease. A repeated start with the same claim is the same answer.
func TestStartMovesToRunningAndRenews(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	c := f.claim(t, id, "air26")
	f.clk.Advance(20 * time.Second)
	f.ev.reset()

	l, err := f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)
	assert.Equal(t, wire.Lease{LeaseS: 60, RenewS: 15}, l)
	assert.Equal(t, wire.StateRunning, f.get(t, id).State)
	assert.Equal(t, ms(f.clk.Now().Add(60*time.Second)), f.leaseDeadline(t, id))
	require.Len(t, f.ev.all(), 1)

	_, err = f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err, "a retried start is not an error")
	assert.Len(t, f.ev.all(), 1, "and announces nothing more")
	assert.Equal(t, wire.StateRunning, f.get(t, id).State)
}

// Heartbeat renews the lease from the origin's clock and answers cancel only once the person asked for it.
func TestHeartbeatRenewsAndReportsCancel(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	c := f.claim(t, id, "air26")
	_, err := f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)

	f.clk.Advance(15 * time.Second)
	f.ev.reset()
	h, err := f.q.Heartbeat(bg, id, c.ClaimID)
	require.NoError(t, err)
	assert.Equal(t, wire.HeartbeatResponse{LeaseS: 60, RenewS: 15, Cancel: false}, h)
	assert.Equal(t, ms(f.clk.Now().Add(60*time.Second)), f.leaseDeadline(t, id))
	assert.Empty(t, f.ev.all(), "a heartbeat is not a state change")

	_, err = f.q.Cancel(bg, id)
	require.NoError(t, err)
	h, err = f.q.Heartbeat(bg, id, c.ClaimID)
	require.NoError(t, err)
	assert.True(t, h.Cancel)
}

// A claim_id that is stale, unknown or for another job gets a 409 on start, heartbeat, release and result, and the job is not touched
// (spec 4.2: an old execution can never renew, release or post after its lease was given to someone else).
func TestStaleClaimIDChangesNothing(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	old := f.claim(t, id, "air26")
	_, err := f.q.Start(bg, id, old.ClaimID)
	require.NoError(t, err)
	// the lease lapses and the job is claimed again by someone else
	f.clk.Advance(61 * time.Second)
	_, err = f.q.Sweep(bg)
	require.NoError(t, err)
	fresh := f.claim(t, id, "mserv")
	require.NotEqual(t, old.ClaimID, fresh.ClaimID)

	before := f.get(t, id)
	deadline := f.leaseDeadline(t, id)
	f.ev.reset()
	exit := 0
	check := func(name string, err error) {
		t.Helper()
		assert.Equal(t, wire.StateClaimed, conflictState(t, err), name)
	}
	_, err = f.q.Start(bg, id, old.ClaimID)
	check("start", err)
	_, err = f.q.Heartbeat(bg, id, old.ClaimID)
	check("heartbeat", err)
	err = f.q.Release(bg, id, wire.ReleaseRequest{ClaimID: old.ClaimID, Reason: wire.ReleaseReclaim})
	check("release", err)
	_, err = f.q.Result(bg, id, wire.ResultRequest{ClaimID: old.ClaimID, State: wire.StateSucceeded, Exit: &exit})
	check("result", err)
	_, err = f.q.Heartbeat(bg, id, wire.NewClaimID())
	check("heartbeat unknown", err)

	assert.Equal(t, before, f.get(t, id))
	assert.Equal(t, deadline, f.leaseDeadline(t, id))
	assert.Empty(t, f.ev.all())
	// the id of a job nobody has
	_, err = f.q.Heartbeat(bg, "j_nope", fresh.ClaimID)
	assert.ErrorIs(t, err, ErrNotFound)
}

// Release with reclaim puts the job back in the queue without counting the attempt; with infra it counts one. The claim is gone, so the
// next claim is a new one and the old claim_id is stale.
func TestReleaseRequeues(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		reason   wire.ReleaseReason
		attempts int
	}{{wire.ReleaseReclaim, 0}, {wire.ReleaseInfra, 1}} {
		id := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
		c := f.claim(t, id, "air26")
		_, err := f.q.Start(bg, id, c.ClaimID)
		require.NoError(t, err)
		f.ev.reset()

		require.NoError(t, f.q.Release(bg, id, wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: tc.reason, Detail: "x"}))
		j := f.get(t, id)
		assert.Equal(t, wire.StateQueued, j.State, string(tc.reason))
		assert.Nil(t, j.Claim)
		assert.Equal(t, tc.attempts, f.attempts(t, id), string(tc.reason))
		require.Len(t, f.ev.all(), 1)

		_, err = f.q.Heartbeat(bg, id, c.ClaimID)
		assert.Equal(t, wire.StateQueued, conflictState(t, err))
		again := f.claim(t, id, "mserv")
		assert.Equal(t, tc.attempts+1, again.Job.Claim.Attempt, "the attempt number of the new claim")
	}
}

// A job somebody asked to cancel must not come back to life when its worker hands the claim back: it ends cancelled (superseded when a
// newer job asked for it), because nothing is running to post a result.
func TestReleaseOfACancelledJobEndsIt(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	c := f.claim(t, id, "air26")
	_, err := f.q.Cancel(bg, id)
	require.NoError(t, err)
	require.NoError(t, f.q.Release(bg, id, wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: wire.ReleaseReclaim}))
	j := f.get(t, id)
	assert.Equal(t, wire.StateCancelled, j.State)
	require.NotNil(t, j.Result)
	assert.Equal(t, "cancelled", j.Result.Reason)
}

// A running job that a newer job superseded (its task is not interruptible, so it ran on) ends as superseded when its worker hands the
// claim back or its lease lapses: the newer job is the one that counts, so the old one is not queued to run again.
func TestReleaseAndLapseOfASupersededJobEndItAsSuperseded(t *testing.T) {
	f := newFixture(t)
	for _, how := range []string{"release", "lapse"} {
		key := "k-" + how
		old := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = key; s.Interruptible = false })
		c := f.claim(t, old, "air26")
		_, err := f.q.Start(bg, old, c.ClaimID)
		require.NoError(t, err)
		newer := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = key })

		if how == "release" {
			require.NoError(t, f.q.Release(bg, old, wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: wire.ReleaseReclaim}))
		} else {
			f.clk.Advance(61 * time.Second)
			_, err := f.q.Sweep(bg)
			require.NoError(t, err)
		}
		j := f.get(t, old)
		assert.Equal(t, wire.StateSuperseded, j.State, how)
		require.NotNil(t, j.Result, how)
		assert.Equal(t, "superseded_by:"+newer, j.Result.Reason, how)
		require.NotNil(t, j.Result.SupersededBy, how)
		assert.Equal(t, newer, *j.Result.SupersededBy, how)
	}
}

func ptr[T any](v T) *T { return &v }

// A result ends the job in the state the worker posted, with the worker's exit and summary; the origin records its own finish time and
// the claimant's host. Posting it twice with the same claim_id returns the same job and announces once (spec 4.3: idempotent).
func TestResultEndsTheJobAndIsIdempotent(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	c := f.claim(t, id, "air26")
	_, err := f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)
	f.clk.Advance(90 * time.Second)
	f.ev.reset()

	started := time.Date(2026, 10, 18, 4, 59, 0, 0, time.UTC) // the worker's clock, an hour of nothing the origin trusts
	req := wire.ResultRequest{
		ClaimID: c.ClaimID, State: wire.StateFailed, Exit: ptr(1),
		Summary:   &wire.Summary{Passed: 3, Failed: 1},
		StartedAt: &started, FinishedAt: &started,
	}
	j, err := f.q.Result(bg, id, req)
	require.NoError(t, err)
	assert.Equal(t, wire.StateFailed, j.State)
	require.NotNil(t, j.Result)
	assert.Equal(t, ptr(1), j.Result.Exit)
	assert.Equal(t, 1, j.Result.Summary.Failed)
	assert.Equal(t, "air26", j.Result.Host)
	assert.Equal(t, "", j.Result.Reason)
	assert.Nil(t, j.Claim)
	var fin int64
	require.NoError(t, f.db.QueryRow(`SELECT finished_at FROM jobs WHERE id = ?`, id).Scan(&fin))
	assert.Equal(t, ms(f.clk.Now()), fin, "ordering uses the origin's clock")
	require.NoError(t, j.Validate())
	require.Len(t, f.ev.all(), 1)

	f.clk.Advance(time.Minute)
	again, err := f.q.Result(bg, id, req)
	require.NoError(t, err)
	assert.Equal(t, j, again)
	assert.Len(t, f.ev.all(), 1, "the repeat announces nothing")

	// another claim_id on an ended job is a plain conflict
	_, err = f.q.Result(bg, id, wire.ResultRequest{ClaimID: wire.NewClaimID(), State: wire.StateSucceeded})
	assert.Equal(t, wire.StateFailed, conflictState(t, err))
}

// A result may come from a claimed job that never started (the person cancelled first), and the worker's reason is kept.
func TestResultFromClaimedKeepsTheWorkersReason(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	c := f.claim(t, id, "air26")
	j, err := f.q.Result(bg, id, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateFailed, Reason: "timeout"})
	require.NoError(t, err)
	assert.Equal(t, "timeout", j.Result.Reason)
}

func TestResultReasonIsAWhitelist(t *testing.T) {
	ok := wire.ResultRequest{ClaimID: wire.NewClaimID(), State: wire.StateFailed}
	for _, r := range []string{"", "oom", "timeout", "infra"} {
		ok.Reason = r
		assert.NoError(t, ok.Validate(), r)
	}
	for _, r := range []string{"lost", "superseded_by:j_1", "queue_timeout", "whatever"} {
		ok.Reason = r
		assert.Error(t, ok.Validate(), "%q is the origin's to write, not a worker's", r)
	}
}

// A reason explains a failure: a success has none (spec 4.1), and oom, timeout and infra go only with failed. Mutation gate: no
// cross-check → succeeded with oom is stored as it came.
func TestResultReasonGoesOnlyWithFailed(t *testing.T) {
	for _, st := range []wire.JobState{wire.StateSucceeded, wire.StateCancelled, wire.StateSuperseded} {
		for _, r := range wire.ResultReasons {
			assert.Error(t, wire.ResultRequest{ClaimID: wire.NewClaimID(), State: st, Reason: r}.Validate(), "%s with %s", st, r)
		}
		assert.NoError(t, wire.ResultRequest{ClaimID: wire.NewClaimID(), State: st}.Validate(), string(st))
	}
	for _, r := range wire.ResultReasons {
		assert.NoError(t, wire.ResultRequest{ClaimID: wire.NewClaimID(), State: wire.StateFailed, Reason: r}.Validate(), r)
	}
}

// A job that a newer submission superseded while it ran keeps the state its worker posted, with superseded_by and the reason that says so
// (spec 4.2: a non-interruptible older job runs to its end).
func TestResultOfASupersededRunningJobCarriesSupersededBy(t *testing.T) {
	f := newFixture(t)
	old := f.submit(t, func(s *wire.JobSubmission) { s.Interruptible = false })
	c := f.claim(t, old, "air26")
	_, err := f.q.Start(bg, old, c.ClaimID)
	require.NoError(t, err)
	newer := f.submit(t)

	j, err := f.q.Result(bg, old, wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Exit: ptr(0)})
	require.NoError(t, err)
	assert.Equal(t, wire.StateSucceeded, j.State)
	require.NotNil(t, j.Result.SupersededBy)
	assert.Equal(t, newer, *j.Result.SupersededBy)
	assert.Equal(t, "superseded_by:"+newer, j.Result.Reason)
}

// The lease runs out on the origin's clock: after 60 s without a renewal a claimed or running job goes back to the queue with the
// attempt counted; the second time it fails with reason lost (spec 4.2). A worker clock that is an hour off changes nothing.
func TestLeaseLapseRequeuesThenFailsAsLost(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	c := f.claim(t, id, "air26")
	_, err := f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)

	f.clk.Advance(59 * time.Second)
	r, err := f.q.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, 0, r.Lapsed)
	assert.Equal(t, wire.StateRunning, f.get(t, id).State)

	// a heartbeat in time moves the deadline
	_, err = f.q.Heartbeat(bg, id, c.ClaimID)
	require.NoError(t, err)
	f.clk.Advance(59 * time.Second)
	r, err = f.q.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, 0, r.Lapsed)

	f.clk.Advance(2 * time.Second)
	f.ev.reset()
	r, err = f.q.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, 1, r.Lapsed)
	j := f.get(t, id)
	assert.Equal(t, wire.StateQueued, j.State)
	assert.Equal(t, 1, f.attempts(t, id))
	assert.Nil(t, j.Claim)
	assert.Len(t, f.ev.all(), 1)

	// the second attempt lapses too
	c2 := f.claim(t, id, "mserv")
	f.clk.Advance(61 * time.Second)
	r, err = f.q.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, 1, r.Lapsed)
	j = f.get(t, id)
	assert.Equal(t, wire.StateFailed, j.State)
	require.NotNil(t, j.Result)
	assert.Equal(t, "lost", j.Result.Reason)
	assert.Equal(t, "mserv", j.Result.Host)
	assert.Nil(t, j.Result.Exit)
	require.NoError(t, j.Validate())

	// a worker that comes back after that is told no
	_, err = f.q.Heartbeat(bg, id, c2.ClaimID)
	assert.Equal(t, wire.StateFailed, conflictState(t, err))
	_, err = f.q.Result(bg, id, wire.ResultRequest{ClaimID: c2.ClaimID, State: wire.StateSucceeded})
	assert.Equal(t, wire.StateFailed, conflictState(t, err))
}

// Only lapses count towards lost: two infrastructure releases (they count as attempts, and the claim shows them) followed by one lapse
// requeue the job, and the second lapse fails it. Mutation gate: deciding by attempts fails the job at the first lapse.
func TestInfraReleasesDoNotBringLostCloser(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	for i := 0; i < 2; i++ {
		c := f.claim(t, id, "air26")
		require.NoError(t, f.q.Release(bg, id, wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: wire.ReleaseInfra}))
	}
	assert.Equal(t, 2, f.attempts(t, id))

	c := f.claim(t, id, "air26")
	assert.Equal(t, 3, c.Job.Claim.Attempt, "attempts are shown as counted")
	f.clk.Advance(61 * time.Second)
	_, err := f.q.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, wire.StateQueued, f.get(t, id).State, "the first lapse requeues")
	assert.Equal(t, 3, f.attempts(t, id))

	f.claim(t, id, "air26")
	f.clk.Advance(61 * time.Second)
	_, err = f.q.Sweep(bg)
	require.NoError(t, err)
	j := f.get(t, id)
	assert.Equal(t, wire.StateFailed, j.State, "the second lapse fails it")
	assert.Equal(t, "lost", j.Result.Reason)
	var lapses int
	require.NoError(t, f.db.QueryRow(`SELECT lapses FROM jobs WHERE id = ?`, id).Scan(&lapses))
	assert.Equal(t, 2, lapses, "the lapse that ended it is counted too")
	assert.Equal(t, 4, f.attempts(t, id))
}

// A fast-lane job is never requeued: its lease lapsing fails it as lost at once, and a release ends it with the release's reason.
func TestLocalJobIsNeverRequeued(t *testing.T) {
	f := newFixture(t)
	lapsed := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	released := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	var cl [2]wire.ClaimResponse
	for i, id := range []string{lapsed, released} {
		_, err := f.db.Exec(`UPDATE jobs SET local = 1 WHERE id = ?`, id)
		require.NoError(t, err)
		cl[i] = f.claim(t, id, "mlab")
	}
	require.NoError(t, f.q.Release(bg, released, wire.ReleaseRequest{ClaimID: cl[1].ClaimID, Reason: wire.ReleaseInfra}))
	j := f.get(t, released)
	assert.Equal(t, wire.StateFailed, j.State)
	assert.Equal(t, "infra", j.Result.Reason)

	f.clk.Advance(61 * time.Second)
	_, err := f.q.Sweep(bg)
	require.NoError(t, err)
	j = f.get(t, lapsed)
	assert.Equal(t, wire.StateFailed, j.State)
	assert.Equal(t, "lost", j.Result.Reason)
}

// A lapsed job that somebody asked to cancel is ended, not requeued: nothing is running and nobody wants it.
func TestLeaseLapseOfACancelledJobEndsIt(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	f.claim(t, id, "air26")
	_, err := f.q.Cancel(bg, id)
	require.NoError(t, err)
	f.clk.Advance(61 * time.Second)
	_, err = f.q.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, wire.StateCancelled, f.get(t, id).State)
}

// The origin restarting gives every held job a full lease before anything is expired, so workers that kept running while it was down
// can still renew (spec 4.2).
func TestOriginRestartGivesHeldJobsAFullLease(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	c := f.claim(t, id, "air26")
	_, err := f.q.Start(bg, id, c.ClaimID)
	require.NoError(t, err)
	queued := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })

	f.clk.Advance(10 * time.Minute) // the origin was down; the lease is long past
	q2, err := New(f.db, Options{Origin: "mlab", Now: f.clk.Now, Pinner: f.pins})
	require.NoError(t, err)
	assert.Equal(t, ms(f.clk.Now().Add(60*time.Second)), f.leaseDeadline(t, id))

	r, err := q2.Sweep(bg)
	require.NoError(t, err)
	assert.Equal(t, 0, r.Lapsed)
	h, err := q2.Heartbeat(bg, id, c.ClaimID)
	require.NoError(t, err)
	assert.False(t, h.Cancel)
	assert.Equal(t, wire.StateQueued, f.get(t, queued).State)
}
