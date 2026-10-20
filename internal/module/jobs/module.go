// Package jobsmod is the Purdex daemon's read side of wwd's jobs (wwd spec §10): the routes the App reads the job list, one job and
// its log from, answered from the local wwd's unix socket. A job on another host is read with ?host=<alias>: the local wwd answers
// or forwards to that peer, so this daemon never holds a pool token or a peer's address. It only reads; wwd being absent or broken
// never affects the daemon (the module always initialises and a route answers a clear error status until wwd is there).
package jobsmod

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// Error codes of /api/pool/jobs*.
const (
	ErrWWDUnavailable  = "wwd_unavailable"  // 503: wwd is not running (or did not answer in time)
	ErrWWDBadResponse  = "wwd_bad_response" // 502: wwd answered outside the contract
	ErrWWDError        = "wwd_error"        // 502: wwd answered with an error status
	ErrHostUnreachable = "host_unreachable" // 502: the local wwd could not reach the named host
	ErrBadRequest      = "bad_request"      // 400
	ErrNotFound        = "not_found"        // 404: no such job, or no stored log yet
	ErrBusy            = "busy"             // 429: too many wwd calls in flight
	ErrCursorAhead     = "cursor_ahead"     // 409: the cursor was made by another database; the body is wwd's CursorAhead
)

const (
	// maxInFlight bounds the wwd calls the jobs routes have under way at once: a paired phone may read them, and each call holds a
	// socket and up to the client's body cap in memory, so a runaway client is turned away instead of queued.
	maxInFlight = 8
	// maxLogInFlight bounds the full-log reads among them: each holds a whole log (up to the client's 64 MiB cap) in memory while it
	// is copied out, so eight at once would be half a GiB. The detail's small log is not counted (it reads at most maxDetailLog).
	maxLogInFlight = 2
	// maxControlInFlight bounds the changes (a cancel, a position) under way at once. They have places of their own: a paired phone may
	// read, and its reads must not use up the places an admin's cancel needs.
	maxControlInFlight = 4
	// defaultLimit is the page of a list that names none (wwd's own default is every job it keeps), maxLimit the most one asks for.
	defaultLimit = 200
	maxLimit     = 1000
	// maxDetailLog is the most log text a job's detail carries. A longer log is not in the detail (null, as for a log not stored yet):
	// read it from GET /api/pool/jobs/{id}/log.
	maxDetailLog = 1 << 20
)

// Module is the jobs module.
type Module struct {
	mu    sync.Mutex
	ready bool // Init ran and Stop has not: jobs.v1 is announced
	// cl is the local wwd's client; nil makes one for client.DefaultSocket() at Init.
	cl       *client.Client
	inflight chan struct{} // a semaphore of maxInFlight
	logs     chan struct{} // a semaphore of maxLogInFlight, held by the /log route besides inflight
	control  chan struct{} // a semaphore of maxControlInFlight, for the cancel and position routes only

	// jobs.changed (events.go)
	core   *core.Core
	logf   func(format string, args ...any)
	retry  time.Duration        // the pause before the event stream is opened again
	emit   func(core.HostEvent) // where jobs.changed goes; nil: the core's host events
	src    eventSource          // the local wwd's event stream; nil: cl
	cancel context.CancelFunc   // ends the stream loop
	done   chan struct{}        // closed when that loop has ended
	// where the last catch-up read stopped (memory only: a restarted daemon has no App connection that missed anything)
	cursor, epoch string
	based         bool
}

// eventSource is the part of the wwd client the stream loop reads; *client.Client is one.
type eventSource interface {
	Watch(ctx context.Context, ready chan<- struct{}, fn func(wire.Event) (done bool)) error
	JobsAfter(ctx context.Context, after string, limit int) (wire.JobsListResponse, error)
}

func New() *Module {
	return &Module{inflight: make(chan struct{}, maxInFlight), logs: make(chan struct{}, maxLogInFlight), control: make(chan struct{}, maxControlInFlight), logf: log.Printf, retry: retryEvery}
}

func (m *Module) source() eventSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.src != nil {
		return m.src
	}
	return m.cl
}

func (m *Module) Name() string           { return "jobs" }
func (m *Module) Dependencies() []string { return nil }

// Init never fails: wwd is a separate process that may start later than this daemon.
func (m *Module) Init(c *core.Core) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cl == nil {
		m.cl = client.New(client.DefaultSocket())
	}
	m.core = c
	m.ready = true
	return nil
}

// Start opens the local wwd's event stream in the background; wwd not running is retried and never fails the daemon.
// A second Start while it runs does nothing; a Start after Stop runs it again, but not while the loop of the earlier Start has still
// not ended (a Stop whose ctx ran out leaves it ending): that Start is refused rather than run beside the old loop.
func (m *Module) Start(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		return nil
	}
	if m.done != nil {
		select {
		case <-m.done:
		default:
			return errors.New("jobs: the stream loop of the earlier Start has not ended yet")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.cancel, m.done, m.ready = cancel, done, true
	go func() {
		defer close(done)
		m.watch(ctx)
	}()
	return nil
}

// Stop ends the stream loop and waits for it as long as ctx allows. The loop is remembered until it has ended, so a Stop that ran out
// of time can be repeated and a Start does not begin a second loop beside it. Stopping a module that never ran does nothing.
func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	m.ready = false
	cancel, done := m.cancel, m.done
	m.cancel = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Status is what /api/info reads to announce jobs.v1 (the API exists; whether wwd is up is each call's answer).
func (m *Module) Status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"ready": m.ready}
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pool/jobs", m.handleList)
	mux.HandleFunc("GET /api/pool/jobs/{id}", m.handleJob)
	mux.HandleFunc("GET /api/pool/jobs/{id}/log", m.handleLog)
	m.registerControl(mux)
}

func (m *Module) client() *client.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cl
}

// slot takes an in-flight place; false means the caller was answered 429.
func (m *Module) slot(w http.ResponseWriter) (release func(), ok bool) {
	select {
	case m.inflight <- struct{}{}:
		return func() { <-m.inflight }, true
	default:
		writeErr(w, http.StatusTooManyRequests, ErrBusy, "too many job reads in flight; retry")
		return nil, false
	}
}

// jobID reads and checks {id}: a malformed one is the caller's error, not wwd's.
func jobID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !client.ValidJobID(id) {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "not a job id")
		return "", false
	}
	return id, true
}

// host reads and checks ?host=: "" is the local wwd's own.
func host(w http.ResponseWriter, r *http.Request) (string, bool) {
	h := r.URL.Query().Get("host")
	if h != "" && !wire.ValidAlias(h) {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "host is empty, a dot name (. or ..), too long or holds a control character")
		return "", false
	}
	return h, true
}

// handleList is GET /api/pool/jobs?state=&session=&host=&after=&limit=: wwd's JobsListResponse with each job a PoolJob (oldest change first, the cursor
// to read on from, the database epoch). Without after it is the list as it is now, at most limit jobs.
func (m *Module) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	h, ok := host(w, r)
	if !ok {
		return
	}
	f := client.JobsPageQuery{Host: h, After: q.Get("after"), Limit: defaultLimit}
	f.Session = q.Get("session")
	if s := q.Get("state"); s != "" {
		f.State = wire.JobState(s)
		if !f.State.Valid() {
			writeErr(w, http.StatusBadRequest, ErrBadRequest, "state is not a job state")
			return
		}
	}
	if f.After != "" && !wire.ValidCursor(f.After) {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "after is not a cursor")
		return
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxLimit {
			writeErr(w, http.StatusBadRequest, ErrBadRequest, "limit must be between 1 and "+strconv.Itoa(maxLimit))
			return
		}
		f.Limit = n
	}
	release, ok := m.slot(w)
	if !ok {
		return
	}
	defer release()
	resp, err := m.client().JobsPage(r.Context(), f)
	if err != nil {
		writeWWDError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, poolJobsList(resp))
}

// handleJob is GET /api/pool/jobs/{id}?host=: the contract's JobDetail, the job and its stored log (null until it is stored, and
// when it is over maxDetailLog).
func (m *Module) handleJob(w http.ResponseWriter, r *http.Request) {
	h, ok := host(w, r)
	if !ok {
		return
	}
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	release, ok := m.slot(w)
	if !ok {
		return
	}
	defer release()
	cl := m.client()
	job, err := cl.JobAt(r.Context(), h, id)
	if err != nil {
		writeWWDError(w, err)
		return
	}
	detail := PoolJobDetail{Job: poolJob(job)}
	if job.State.Terminal() {
		// at most maxDetailLog is read: a longer log is never held in memory here, it is told apart from "not stored" by the /log route
		b, tooLarge, err := cl.LogAtMost(r.Context(), h, id, maxDetailLog)
		var se *client.StatusError
		switch {
		case err == nil:
			if !tooLarge {
				s := string(b)
				detail.Log = &s
			}
		case errors.As(err, &se) && se.Code == http.StatusNotFound: // not stored yet
		default:
			writeWWDError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleLog is GET /api/pool/jobs/{id}/log?host=: the stored log as text; 404 until it is stored.
func (m *Module) handleLog(w http.ResponseWriter, r *http.Request) {
	h, ok := host(w, r)
	if !ok {
		return
	}
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	release, ok := m.slot(w)
	if !ok {
		return
	}
	defer release()
	select {
	case m.logs <- struct{}{}:
		defer func() { <-m.logs }()
	default:
		writeErr(w, http.StatusTooManyRequests, ErrBusy, "too many full logs being read; retry")
		return
	}
	b, err := m.client().LogAt(r.Context(), h, id)
	if err != nil {
		writeWWDError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff") // a log is whatever a job printed
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

// writeWWDError answers a failed wwd call with the status the App reads.
func writeWWDError(w http.ResponseWriter, err error) {
	var ahead *client.CursorAheadError
	var se *client.StatusError
	switch {
	case errors.As(err, &ahead):
		writeJSON(w, http.StatusConflict, ahead.CursorAhead)
	case errors.Is(err, client.ErrUnavailable):
		writeErr(w, http.StatusServiceUnavailable, ErrWWDUnavailable, "wwd is not running or did not answer")
	case errors.Is(err, client.ErrBadResponse):
		writeErr(w, http.StatusBadGateway, ErrWWDBadResponse, "wwd answered outside the contract")
	case errors.As(err, &se):
		switch se.Code {
		case http.StatusBadRequest:
			writeErr(w, http.StatusBadRequest, ErrBadRequest, "wwd refused the request")
		case http.StatusNotFound:
			writeErr(w, http.StatusNotFound, ErrNotFound, "no such job, or its log is not stored yet")
		case http.StatusBadGateway:
			var un struct {
				Host   string `json:"host"`
				Reason string `json:"reason"`
			}
			if json.Unmarshal([]byte(se.Body), &un) == nil && un.Host != "" {
				writeErr(w, http.StatusBadGateway, ErrHostUnreachable, "wwd could not reach "+un.Host)
				return
			}
			writeErr(w, http.StatusBadGateway, ErrWWDError, "wwd answered "+http.StatusText(se.Code))
		default:
			writeErr(w, http.StatusBadGateway, ErrWWDError, "wwd answered "+http.StatusText(se.Code))
		}
	case errors.Is(err, context.Canceled):
		// the caller went away
	default:
		writeErr(w, http.StatusBadGateway, ErrWWDError, "wwd call failed")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, ipeers.APIError{Error: code, Detail: detail})
}
