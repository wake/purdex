package queue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/wire"
)

func ids(js []wire.Job) []string {
	var out []string
	for _, j := range js {
		out = append(out, j.ID)
	}
	return out
}

func TestGetUnknownIsNotFound(t *testing.T) {
	f := newFixture(t)
	_, err := f.q.Get(context.Background(), "j_nope")
	assert.True(t, errors.Is(err, ErrNotFound), "%v", err)
}

// List answers oldest first, filters by state and by submitter session, and a limit cuts the newest.
func TestListFiltersAndOrder(t *testing.T) {
	f := newFixture(t)
	mk := func(session, key string) string {
		f.clk.Advance(time.Second)
		return f.submit(t, func(s *wire.JobSubmission) { s.Submitter.Session, s.SupersedeKey = session, key })
	}
	a := mk("s1", "k1")
	b := mk("s2", "k2")
	c := mk("s1", "k3")
	f.seed(t, b, wire.StateRunning, "")
	ctx := context.Background()

	all, err := f.q.List(ctx, Filter{})
	require.NoError(t, err)
	assert.Equal(t, []string{a, b, c}, ids(all), "oldest first")

	queued, err := f.q.List(ctx, Filter{States: []wire.JobState{wire.StateQueued}})
	require.NoError(t, err)
	assert.Equal(t, []string{a, c}, ids(queued))

	two, err := f.q.List(ctx, Filter{States: []wire.JobState{wire.StateQueued, wire.StateRunning}})
	require.NoError(t, err)
	assert.Equal(t, []string{a, b, c}, ids(two))

	s1, err := f.q.List(ctx, Filter{SubmitterSession: "s1"})
	require.NoError(t, err)
	assert.Equal(t, []string{a, c}, ids(s1))

	none, err := f.q.List(ctx, Filter{SubmitterSession: "nobody"})
	require.NoError(t, err)
	assert.Empty(t, none)

	lim, err := f.q.List(ctx, Filter{Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []string{a, b}, ids(lim))
	require.NoError(t, wire.JobsListResponse{Jobs: all}.Validate())
}

// Ad-hoc jobs are not listed to peers at all (spec 4.4, 8.3, 11): the whole job, not its args. A local listing sees them. Mutation
// gate: the ForPeers condition dropped → the peer listing shows the ad-hoc job.
func TestListForPeersNeverShowsAdHocJobs(t *testing.T) {
	f := newFixture(t)
	normal := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	adhoc := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey, s.AdHoc, s.Task = "", true, "undeclared" })

	local, err := f.q.List(context.Background(), Filter{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{normal, adhoc}, ids(local))

	peers, err := f.q.List(context.Background(), Filter{ForPeers: true})
	require.NoError(t, err)
	assert.Equal(t, []string{normal}, ids(peers))

	// not through a state filter or the submitter filter either
	peers, err = f.q.List(context.Background(), Filter{ForPeers: true, States: []wire.JobState{wire.StateQueued}, SubmitterSession: "s1"})
	require.NoError(t, err)
	assert.Equal(t, []string{normal}, ids(peers))
}

// A fast-lane job (local: it runs in the submitter's own working tree and never leaves the host) is not listed to peers either, and a
// peer asking for it by id finds nothing, exactly like an ad-hoc job. The columns exist from the first deploy (the 3b-3 recorder fills
// local); here a row is marked by hand. Mutation gate: the local condition dropped from either read → the peer sees the job.
func TestPeersNeverSeeALocalJob(t *testing.T) {
	f := newFixture(t)
	normal := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	local := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	_, err := f.db.Exec(`UPDATE jobs SET local = 1 WHERE id = ?`, local)
	require.NoError(t, err)
	ctx := context.Background()

	all, err := f.q.List(ctx, Filter{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{normal, local}, ids(all), "the local view lists it")
	assert.True(t, f.get(t, local).Local, "and says it is a fast-lane job")
	assert.False(t, f.get(t, normal).Local)
	for _, j := range all {
		assert.Equal(t, j.ID == local, j.Local, "List says the same")
	}

	peers, err := f.q.List(ctx, Filter{ForPeers: true})
	require.NoError(t, err)
	assert.Equal(t, []string{normal}, ids(peers))
	_, err = f.q.GetForPeers(ctx, local)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = f.q.GetForPeers(ctx, normal)
	assert.NoError(t, err)
}

// A claim is shown while someone holds it (claimed, running), not after the job ended or went back to the queue: the columns stay for
// the result's idempotence, but the job no longer says it is claimed. Mutation gate: the state condition dropped → this fails.
func TestClaimIsShownOnlyWhileHeld(t *testing.T) {
	f := newFixture(t)
	for _, c := range []struct {
		state wire.JobState
		shown bool
	}{
		{wire.StateClaimed, true}, {wire.StateRunning, true},
		{wire.StateQueued, false}, {wire.StateSucceeded, false}, {wire.StateFailed, false}, {wire.StateCancelled, false},
	} {
		id := f.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
		f.seed(t, id, c.state, "claim_worker = 'air26', claim_since = ?", ms(f.clk.Now()))
		assert.Equal(t, c.shown, f.get(t, id).Claim != nil, string(c.state))
	}
}

// The claim_id is a capability: no read path ever carries it (spec 4.2). Mutation gate: a Job built with the claim_id → this fails.
func TestNoReadCarriesTheClaimID(t *testing.T) {
	f := newFixture(t)
	id := f.submit(t)
	secret := wire.NewClaimID()
	f.seed(t, id, wire.StateRunning, "claim_worker = 'air26', claim_id = ?, claim_since = ?, lease_deadline = ?, lease_s = 60", secret, ms(f.clk.Now()), ms(f.clk.Now().Add(time.Minute)))

	j := f.get(t, id)
	require.NotNil(t, j.Claim)
	assert.Equal(t, "air26", j.Claim.Worker)
	assert.Equal(t, 1, j.Claim.Attempt)
	b, err := json.Marshal(j)
	require.NoError(t, err)
	assert.NotContains(t, string(b), secret)

	list, err := f.q.List(context.Background(), Filter{})
	require.NoError(t, err)
	b, err = json.Marshal(wire.JobsListResponse{Jobs: list})
	require.NoError(t, err)
	assert.NotContains(t, string(b), secret)
}
