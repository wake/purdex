// Package jobsapi is the HTTP face of the job queue's reads and submission (spec 4.4): the handlers the daemon mounts. A handler is made
// for a surface: the local socket (the host's own person and App, who see everything) or TCP (a peer, who never sees an ad-hoc or
// fast-lane job). The package decides nothing about who may call; the daemon puts the pool token in front of the TCP routes and mounts
// submission on the socket only.
package jobsapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/wire"
)

const (
	// maxBody bounds a submission (the biggest part is the argument list).
	maxBody = 1 << 20
	// maxLimit bounds ?limit=: a list is read in pieces, not all at once.
	maxLimit = 10000
)

// API serves one queue.
type API struct{ q *queue.Queue }

// New returns the API over q (the daemon's one queue).
func New(q *queue.Queue) *API { return &API{q: q} }

var jobIDRe = regexp.MustCompile(`^j_[0-9A-Za-z_-]{1,64}$`)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

// fail answers an error from the queue: not found, a refused transition (the job's state, as the wire's 409 body), a snapshot that cannot
// be pinned. Anything else is the host's own trouble: it is 500 and says only that, because the caller cannot act on a SQL error and must
// not be shown a path.
func fail(w http.ResponseWriter, err error) {
	var ce *queue.ConflictError
	switch {
	case errors.Is(err, queue.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such job"})
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, wire.Conflict409{State: ce.State})
	case errors.Is(err, queue.ErrSnapshotRefused):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "the snapshot cannot be pinned"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

// jobID reads {id}; a path that is not a job id is not a job, so it is not found (it never reaches the queue or a file name).
func jobID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !jobIDRe.MatchString(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such job"})
		return "", false
	}
	return id, true
}

// Submit is POST /v1/jobs: the body is a wire.JobSubmission (with the git directory), decoded strictly; the answer is {id}. Mount it on
// the socket only: a job is always submitted on its own host.
func (a *API) Submit() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			badRequest(w, "request body too large or unreadable")
			return
		}
		s, err := wire.Decode[wire.JobSubmission](body)
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		resp, err := a.q.Submit(r.Context(), s)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// List is GET /v1/jobs with ?state=, ?submitter.session=, ?after=<cursor> and ?limit=. The answer always has the origin's cursor: the
// number of the last change in it, or the one asked with when there is nothing new. Jobs come oldest change first, each once, in the
// state it is in now. For a peer the ad-hoc and fast-lane jobs are left out.
func (a *API) List(peer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f := queue.Filter{SubmitterSession: q.Get("submitter.session"), ForPeers: peer}
		if s := q.Get("state"); s != "" {
			st := wire.JobState(s)
			if !st.Valid() {
				badRequest(w, fmt.Sprintf("invalid state %q", s))
				return
			}
			f.States = []wire.JobState{st}
		}
		if s := q.Get("limit"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || n > maxLimit {
				badRequest(w, fmt.Sprintf("limit must be between 1 and %d", maxLimit))
				return
			}
			f.Limit = n
		}
		after, err := queue.ParseCursor(q.Get("after"))
		if err != nil {
			badRequest(w, "invalid cursor")
			return
		}
		epoch, err := a.q.Epoch(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		jobs, cursor, err := a.q.ListAfter(r.Context(), after, f)
		if err != nil {
			fail(w, err)
			return
		}
		// A cursor past the last change cannot be one this database made. It is asked after the read: the head is then at least as far as
		// anything the read could have shown, so a cursor that was valid when the request came in is never refused for a race.
		head, err := a.q.Head(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		if after > head {
			writeJSON(w, http.StatusConflict, wire.CursorAhead{Reason: wire.CursorAheadReason, Head: queue.CursorString(head), Epoch: epoch})
			return
		}
		writeJSON(w, http.StatusOK, wire.JobsListResponse{Jobs: jobs, Cursor: queue.CursorString(cursor), Epoch: epoch})
	}
}

// Get is GET /v1/jobs/{id}.
func (a *API) Get(peer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := jobID(w, r)
		if !ok {
			return
		}
		var j wire.Job
		var err error
		if peer {
			j, err = a.q.GetForPeers(r.Context(), id)
		} else {
			j, err = a.q.Get(r.Context(), id)
		}
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, j)
	}
}

// Log is GET /v1/jobs/{id}/log: the stored log as plain text. It is the output of a command, so it is never to be taken for markup.
func (a *API) Log(peer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := jobID(w, r)
		if !ok {
			return
		}
		rc, size, err := a.q.OpenLog(r.Context(), id, peer)
		if err != nil {
			fail(w, err)
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, io.LimitReader(rc, size))
	}
}

// Cancel is POST /v1/jobs/{id}/cancel: a queued job ends at once, a held one is asked to stop at its next heartbeat. The answer is the job
// as it is then; a job that has ended is a 409 with its state.
func (a *API) Cancel(peer bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := jobID(w, r)
		if !ok {
			return
		}
		var j wire.Job
		var err error
		if peer {
			j, err = a.q.CancelForPeers(r.Context(), id)
		} else {
			j, err = a.q.Cancel(r.Context(), id)
		}
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, j)
	}
}
