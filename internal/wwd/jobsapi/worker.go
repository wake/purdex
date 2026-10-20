package jobsapi

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
)

const (
	// maxJSONBody bounds the JSON body of a worker call (a claim, a heartbeat, a result with its summary).
	maxJSONBody = 1 << 20
)

// maxLogBody bounds what a log upload may send before the queue's own limit (64 MiB) is reached; a little over it, so the queue is the one
// that says "too large" for a log that is just past its cap. A var so a test can lower it.
var maxLogBody int64 = 65 << 20

// WorkerRoutes registers the routes a worker uses on a job (spec 4.3): claim, start, heartbeat, release, result, and PUT log. The claim
// is the only proof of ownership for all of them; claim is the one that depends on the view: a peer cannot claim the host's ad-hoc and
// fast-lane jobs. The daemon mounts them on TCP behind the pool token, and on the socket for the host's own worker and fast-lane runners.
func (a *API) WorkerRoutes(mux *http.ServeMux, peer bool) {
	mux.HandleFunc("POST /v1/jobs/{id}/claim", a.Claim(peer))
	mux.HandleFunc("POST /v1/jobs/{id}/start", a.Start())
	mux.HandleFunc("POST /v1/jobs/{id}/heartbeat", a.Heartbeat())
	mux.HandleFunc("POST /v1/jobs/{id}/release", a.Release())
	mux.HandleFunc("POST /v1/jobs/{id}/result", a.Result())
	mux.HandleFunc("PUT /v1/jobs/{id}/log", a.PutLog())
}

// readBody decodes the JSON body of a worker call into T (trailing data and anything T's Validate refuses are a
// 400, answered here). Unknown fields are skipped: the caller is a worker of a peer host that may run a later build (DecodeLenient, #3710).
// ok is false when the answer has been given.
func readBody[T any](w http.ResponseWriter, r *http.Request) (v T, ok bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err != nil {
		badRequest(w, "request body too large or unreadable")
		return v, false
	}
	v, err = wire.DecodeLenient[T](raw)
	if err != nil {
		badRequest(w, err.Error())
		return v, false
	}
	return v, true
}

// Claim is POST /v1/jobs/{id}/claim: the first claimer of a queued job wins and gets the job, a claim_id and the lease; anyone else gets
// a 409 with the job's state.
func (a *API) Claim(peer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := jobID(w, r)
		if !ok {
			return
		}
		req, ok := readBody[wire.ClaimRequest](w, r)
		if !ok {
			return
		}
		var resp wire.ClaimResponse
		var err error
		if peer {
			resp, err = a.q.ClaimForPeers(r.Context(), id, req)
		} else {
			resp, err = a.q.Claim(r.Context(), id, req)
		}
		if peer && errors.Is(err, queue.ErrNotFound) {
			// spec 4.4: a peer's claim of an ad-hoc or fast-lane job gets 409. It gets the same for a job that is not there, so the answer
			// does not say which ids are the host's own; "claimed" tells the worker to try the next
			writeJSON(w, http.StatusConflict, wire.Conflict409{State: wire.StateClaimed})
			return
		}
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// Start is POST /v1/jobs/{id}/start.
func (a *API) Start() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := jobID(w, r)
		if !ok {
			return
		}
		req, ok := readBody[wire.StartRequest](w, r)
		if !ok {
			return
		}
		lease, err := a.q.Start(r.Context(), id, req.ClaimID)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, lease)
	}
}

// Heartbeat is POST /v1/jobs/{id}/heartbeat: it renews the lease and says whether the worker is to stop.
func (a *API) Heartbeat() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := jobID(w, r)
		if !ok {
			return
		}
		req, ok := readBody[wire.HeartbeatRequest](w, r)
		if !ok {
			return
		}
		resp, err := a.q.Heartbeat(r.Context(), id, req.ClaimID)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// Release is POST /v1/jobs/{id}/release.
func (a *API) Release() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := jobID(w, r)
		if !ok {
			return
		}
		req, ok := readBody[wire.ReleaseRequest](w, r)
		if !ok {
			return
		}
		if err := a.q.Release(r.Context(), id, req); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, struct{}{})
	}
}

// Result is POST /v1/jobs/{id}/result: it ends the job in the state the worker reports. Posted again with the same claim_id it answers
// the same job and changes nothing.
func (a *API) Result() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := jobID(w, r)
		if !ok {
			return
		}
		req, ok := readBody[wire.ResultRequest](w, r)
		if !ok {
			return
		}
		j, err := a.q.Result(r.Context(), id, req)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, j)
	}
}

// PutLog is PUT /v1/jobs/{id}/log?claim_id=: the body is the log, replacing the one there. A claim that is not the job's is a 409 before
// the body is read.
func (a *API) PutLog() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := jobID(w, r)
		if !ok {
			return
		}
		claimID := r.URL.Query().Get("claim_id")
		if !wire.ValidClaimID(claimID) {
			badRequest(w, "invalid claim_id")
			return
		}
		if a.fault.LogUploadFail() { // test hook H1, off unless configured: the log is refused, the result is not touched
			http.Error(w, "fault: log upload refused", http.StatusServiceUnavailable)
			return
		}
		if err := a.q.PutLog(r.Context(), id, claimID, http.MaxBytesReader(w, r.Body, maxLogBody)); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, struct{}{})
	}
}

// SnapshotSource answers the snapshot route's question from the queue (snapshot.Source): is this the live claim of this job, and what is
// there to serve for it. Anything that is not a live claim (stale, released, ended, unknown, a fast-lane job) is ErrStaleClaim, with the
// job's state when the job is known, for the 409 body.
type SnapshotSource struct{ Q *queue.Queue }

func (s SnapshotSource) Claimed(ctx context.Context, jobID, claimID string) (snapshot.Ref, error) {
	lc, err := s.Q.LiveClaim(ctx, jobID, claimID)
	var ce *queue.ConflictError
	switch {
	case err == nil:
		return snapshot.Ref{RepoDir: lc.GitDir, Commit: lc.Commit, Base: lc.Base, State: lc.State}, nil
	case errors.As(err, &ce):
		return snapshot.Ref{State: ce.State}, snapshot.ErrStaleClaim
	case errors.Is(err, queue.ErrNotFound):
		return snapshot.Ref{}, snapshot.ErrStaleClaim
	default:
		return snapshot.Ref{}, err
	}
}
