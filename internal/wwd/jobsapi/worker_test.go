package jobsapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
)

// workerRig is the rig with the worker routes on both views.
func workerRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	a := New(r.q)
	for peer, h := range map[bool]http.Handler{false: r.local, true: r.peer} {
		mux := h.(*http.ServeMux)
		a.WorkerRoutes(mux, peer)
	}
	return r
}

func (r *rig) claim(t *testing.T, h http.Handler, id, worker string) wire.ClaimResponse {
	t.Helper()
	w := call(h, "POST", "/v1/jobs/"+id+"/claim", wire.ClaimRequest{Worker: worker, Labels: []string{"os:darwin"}})
	require.Equal(t, 200, w.Code, w.Body.String())
	return decode[wire.ClaimResponse](t, w)
}

func claimBody(claimID string) wire.StartRequest { return wire.StartRequest{ClaimID: claimID} }

// The whole life of a job through the HTTP routes: claim, start, heartbeat, log, result. Each answer is the wire type the spec names.
func TestAJobsLifeThroughTheWorkerRoutes(t *testing.T) {
	r := workerRig(t)
	id := r.submit(t)

	c := r.claim(t, r.peer, id, "air26")
	assert.Equal(t, wire.StateClaimed, c.Job.State)
	assert.True(t, wire.ValidClaimID(c.ClaimID))
	assert.NotContains(t, call(r.peer, "GET", "/v1/jobs/"+id, nil).Body.String(), c.ClaimID, "the claim_id is never listed")

	w := call(r.peer, "POST", "/v1/jobs/"+id+"/start", claimBody(c.ClaimID))
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, wire.Lease{LeaseS: 60, RenewS: 15}, decode[wire.Lease](t, w))

	w = call(r.peer, "POST", "/v1/jobs/"+id+"/heartbeat", wire.HeartbeatRequest{ClaimID: c.ClaimID})
	require.Equal(t, 200, w.Code)
	assert.False(t, decode[wire.HeartbeatResponse](t, w).Cancel)
	call(r.peer, "POST", "/v1/jobs/"+id+"/cancel", nil)
	w = call(r.peer, "POST", "/v1/jobs/"+id+"/heartbeat", wire.HeartbeatRequest{ClaimID: c.ClaimID})
	assert.True(t, decode[wire.HeartbeatResponse](t, w).Cancel, "the cancel request reaches the worker at its next heartbeat")

	w = call(r.peer, "PUT", "/v1/jobs/"+id+"/log?claim_id="+c.ClaimID, "line 1\nline 2\n")
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Equal(t, "line 1\nline 2\n", call(r.local, "GET", "/v1/jobs/"+id+"/log", nil).Body.String())

	res := wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateCancelled}
	w = call(r.peer, "POST", "/v1/jobs/"+id+"/result", res)
	require.Equal(t, 200, w.Code, w.Body.String())
	j := decode[wire.Job](t, w)
	assert.Equal(t, wire.StateCancelled, j.State)
	assert.Equal(t, id+"/log", j.Result.Log)

	// idempotent: the same result again is the same answer
	again := call(r.peer, "POST", "/v1/jobs/"+id+"/result", res)
	require.Equal(t, 200, again.Code)
	assert.JSONEq(t, w.Body.String(), again.Body.String())
}

// Release hands the claim back; the job is queued again and the old claim is stale.
func TestReleaseThroughTheRoute(t *testing.T) {
	r := workerRig(t)
	id := r.submit(t)
	c := r.claim(t, r.peer, id, "air26")
	w := call(r.peer, "POST", "/v1/jobs/"+id+"/release", wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: wire.ReleaseReclaim, Detail: "owner is back"})
	require.Equal(t, 200, w.Code, w.Body.String())
	j, err := r.q.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, wire.StateQueued, j.State)
	attempts := func(id string) int {
		var n int
		require.NoError(t, r.db.QueryRow(`SELECT attempts FROM jobs WHERE id = ?`, id).Scan(&n))
		return n
	}
	assert.Equal(t, 0, attempts(id), "a reclaim does not count as an attempt")

	w = call(r.peer, "POST", "/v1/jobs/"+id+"/heartbeat", wire.HeartbeatRequest{ClaimID: c.ClaimID})
	require.Equal(t, 409, w.Code)
	assert.Equal(t, wire.StateQueued, decode[wire.Conflict409](t, w).State)

	c2 := r.claim(t, r.peer, id, "mserv")
	w = call(r.peer, "POST", "/v1/jobs/"+id+"/release", wire.ReleaseRequest{ClaimID: c2.ClaimID, Reason: wire.ReleaseInfra})
	require.Equal(t, 200, w.Code)
	assert.Equal(t, 1, attempts(id), "an infrastructure failure does")

	w = call(r.peer, "POST", "/v1/jobs/"+id+"/release", wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: "whatever"})
	assert.Equal(t, 400, w.Code, "a release needs one of the two reasons")
}

// A claim_id that is stale, unknown or of another job is a 409 on every route and changes nothing (spec 4.2).
func TestStaleClaimIsRefusedByEveryRoute(t *testing.T) {
	r := workerRig(t)
	id := r.submit(t)
	other := r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	r.claim(t, r.peer, id, "air26")
	oc := r.claim(t, r.peer, other, "air26")
	before, err := r.q.Get(context.Background(), id)
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		method, path string
		body         any
	}{
		"start":     {"POST", "/start", claimBody(wire.NewClaimID())},
		"heartbeat": {"POST", "/heartbeat", wire.HeartbeatRequest{ClaimID: oc.ClaimID}}, // the other job's claim
		"release":   {"POST", "/release", wire.ReleaseRequest{ClaimID: wire.NewClaimID(), Reason: wire.ReleaseReclaim}},
		"result":    {"POST", "/result", wire.ResultRequest{ClaimID: wire.NewClaimID(), State: wire.StateSucceeded}},
		"log":       {"PUT", "/log?claim_id=" + wire.NewClaimID(), "x"},
	} {
		w := call(r.peer, tc.method, "/v1/jobs/"+id+tc.path, tc.body)
		require.Equal(t, 409, w.Code, name)
		assert.Equal(t, wire.StateClaimed, decode[wire.Conflict409](t, w).State, name)
	}
	after, err := r.q.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, before, after, "nothing changed")
}

// Peers cannot claim the host's ad-hoc jobs (spec 4.4, 8.3, 11); the host's own worker can.
func TestPeerClaimOfAnAdHocJobIsRefused(t *testing.T) {
	r := workerRig(t)
	id := r.submit(t, func(s *wire.JobSubmission) { s.AdHoc = true })
	w := call(r.peer, "POST", "/v1/jobs/"+id+"/claim", wire.ClaimRequest{Worker: "air26"})
	require.Equal(t, 409, w.Code, "spec 4.4: a peer's claim of an ad-hoc job gets 409")
	assert.Equal(t, wire.StateClaimed, decode[wire.Conflict409](t, w).State, "as if someone else had it: the worker tries the next")
	j, err := r.q.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, wire.StateQueued, j.State)
	r.claim(t, r.local, id, "mlab")
}

func TestClaimTakesOnlyAWellFormedRequestOfAQueuedJob(t *testing.T) {
	r := workerRig(t)
	id := r.submit(t)
	for name, body := range map[string]any{
		"no worker": wire.ClaimRequest{},
		"not json":  "{",
		"empty":     "",
	} {
		assert.Equal(t, 400, call(r.peer, "POST", "/v1/jobs/"+id+"/claim", body).Code, name)
	}
	// a worker of a later build may send a field this one does not know (#3710): skipped, and the claim is taken
	other := r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })
	assert.Equal(t, 200, call(r.peer, "POST", "/v1/jobs/"+other+"/claim", `{"worker":"w","bogus":1}`).Code, "an unknown field in a worker's call")
	r.claim(t, r.peer, id, "air26")
	w := call(r.peer, "POST", "/v1/jobs/"+id+"/claim", wire.ClaimRequest{Worker: "mserv"})
	require.Equal(t, 409, w.Code, "first wins")
	assert.Equal(t, wire.StateClaimed, decode[wire.Conflict409](t, w).State)
	assert.Equal(t, 409, call(r.peer, "POST", "/v1/jobs/j_nope/claim", wire.ClaimRequest{Worker: "w"}).Code,
		"a peer is told the same of a job that is hidden from it and of one that is not there: nothing to claim")
	assert.Equal(t, 404, call(r.local, "POST", "/v1/jobs/j_nope/claim", wire.ClaimRequest{Worker: "w"}).Code, "the host's own worker is told the truth")
}

func TestBadClaimIDsAndBodies(t *testing.T) {
	r := workerRig(t)
	id := r.submit(t)
	c := r.claim(t, r.peer, id, "air26")
	for name, tc := range map[string]struct {
		method, path string
		body         any
	}{
		"start bad id":     {"POST", "/start", `{"claim_id":"short"}`},
		"heartbeat empty":  {"POST", "/heartbeat", ""},
		"result bad state": {"POST", "/result", wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateQueued}},
		"result reason":    {"POST", "/result", wire.ResultRequest{ClaimID: c.ClaimID, State: wire.StateSucceeded, Reason: "oom"}},
		"log no claim":     {"PUT", "/log", "x"},
		"log bad claim":    {"PUT", "/log?claim_id=zz", "x"},
	} {
		assert.Equal(t, 400, call(r.peer, tc.method, "/v1/jobs/"+id+tc.path, tc.body).Code, name)
	}
	// a body past the bound is refused as unreadable, not parsed: this claim is well-formed in every way but its size
	huge := wire.ClaimRequest{Worker: "air26", Labels: []string{strings.Repeat("x", 2<<20)}}
	assert.Equal(t, 400, call(r.peer, "POST", "/v1/jobs/"+r.submit(t, func(s *wire.JobSubmission) { s.SupersedeKey = "" })+"/claim", huge).Code)
	assert.Equal(t, 404, call(r.peer, "POST", "/v1/jobs/..%2Fx/start", claimBody(c.ClaimID)).Code)
	// a field of a later build in a worker's call is skipped (#3710), where a bad claim id in it is still refused (above)
	assert.Equal(t, 200, call(r.peer, "POST", "/v1/jobs/"+id+"/start", `{"claim_id":"`+c.ClaimID+`","x":1}`).Code, "start with an unknown key")
	assert.Equal(t, 405, call(r.peer, "GET", "/v1/jobs/"+id+"/claim", nil).Code, "the worker routes take the verb the spec names")
}

// A log the queue refuses as too big is a 413, not a 500.
func TestFailMapsATooLargeLog(t *testing.T) {
	w := httptest.NewRecorder()
	fail(w, queue.ErrLogTooLarge)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	w = httptest.NewRecorder()
	fail(w, queue.ErrNoLogDir)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "directory")
}

// The snapshot route asks the queue whether a claim is live: a live one gets what to serve; stale, released, unknown, and a fast-lane
// job's are all "not live", with the job's state when it is known so the worker's 409 carries it.
func TestSnapshotSource(t *testing.T) {
	r := workerRig(t)
	src := SnapshotSource{Q: r.q}
	ctx := context.Background()
	id := r.submit(t)
	c := r.claim(t, r.peer, id, "air26")

	ref, err := src.Claimed(ctx, id, c.ClaimID)
	require.NoError(t, err)
	assert.Equal(t, snapshot.Ref{RepoDir: "/repos/purdex/.git", Commit: "c0ffee", Base: "ba5e", State: wire.StateClaimed}, ref)

	ref, err = src.Claimed(ctx, id, wire.NewClaimID())
	assert.ErrorIs(t, err, snapshot.ErrStaleClaim)
	assert.Equal(t, wire.StateClaimed, ref.State)

	ref, err = src.Claimed(ctx, "j_nope", c.ClaimID)
	assert.ErrorIs(t, err, snapshot.ErrStaleClaim)
	assert.False(t, ref.State.Valid())

	require.NoError(t, r.q.Release(ctx, id, wire.ReleaseRequest{ClaimID: c.ClaimID, Reason: wire.ReleaseReclaim}))
	ref, err = src.Claimed(ctx, id, c.ClaimID)
	assert.ErrorIs(t, err, snapshot.ErrStaleClaim)
	assert.Equal(t, wire.StateQueued, ref.State)

	// a store that fails is not "stale": the route answers 500 for it
	require.NoError(t, r.db.Close())
	_, err = src.Claimed(ctx, id, c.ClaimID)
	require.Error(t, err)
	assert.False(t, errors.Is(err, snapshot.ErrStaleClaim))
}

func TestPutLogBodyIsBounded(t *testing.T) {
	r := workerRig(t)
	id := r.submit(t)
	c := r.claim(t, r.peer, id, "air26")
	// an upload the route's own bound stops before the queue reads it all (the bound is lowered here so the test does not move 64 MiB)
	old := maxLogBody
	maxLogBody = 1024
	t.Cleanup(func() { maxLogBody = old })
	big := strings.NewReader(strings.Repeat("x", 1025))
	req := httptest.NewRequest("PUT", "/v1/jobs/"+id+"/log?claim_id="+url.QueryEscape(c.ClaimID), big)
	w := httptest.NewRecorder()
	r.peer.ServeHTTP(w, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	_, _, err := r.q.OpenLog(context.Background(), id, false)
	assert.ErrorIs(t, err, queue.ErrNotFound)
}
