// Package poolmod is the Purdex daemon's side of the resource pool (wwd spec §10): the routes the App reads the pool from,
// answered from the local wwd's unix socket. wwd being absent or broken never affects the daemon: the module always
// initialises, and a route answers a clear error status until wwd is there.
package poolmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

// historyDefaultSince is how far back facts history reaches when the request names no since.
const historyDefaultSince = time.Hour

// Error codes of /api/pool/*.
const (
	ErrWWDUnavailable = "wwd_unavailable"  // 503: wwd is not running (or did not answer in time)
	ErrWWDBadResponse = "wwd_bad_response" // 502: wwd answered outside the contract
	ErrWWDError       = "wwd_error"        // 502: wwd answered with an error status
	ErrBadRequest     = "bad_request"      // 400
	ErrBusy           = "busy"             // 429: too many wwd calls in flight
)

// maxInFlight bounds the wwd calls the pool routes have under way at once: a paired phone may read these routes, and each
// call holds a socket and up to the client's body cap in memory, so a runaway client is turned away instead of queued.
const maxInFlight = 8

// answerTTL is how long a pool answer is reused. wwd's facts change at most once per 5 s, so a reader polling faster than this
// gets the same answer; however many readers there are, wwd is asked at most once per key per TTL. maxCached bounds the keys.
const (
	answerTTL = time.Second
	maxCached = 16
	// maxCachedBody is the largest answer that is kept: a raw history can be megabytes, and 16 of those held until the next store would be
	// memory for nothing (a series is a few hundred KB).
	maxCachedBody = 2 << 20
)

type cachedAnswer struct {
	at   time.Time
	body []byte
}

// Module is the pool module.
type Module struct {
	mu    sync.Mutex
	ready bool // Init ran and Stop has not: pool.v1 is announced
	// cl is the local wwd's client; nil makes one for client.DefaultSocket() at Init.
	cl       *client.Client
	now      func() time.Time
	inflight chan struct{} // a semaphore of maxInFlight
	// flightMu guards cache and flights (see serve).
	flightMu sync.Mutex
	cache    map[string]cachedAnswer
	flights  map[string]*flight
}

func New() *Module {
	return &Module{now: time.Now, inflight: make(chan struct{}, maxInFlight), cache: map[string]cachedAnswer{}, flights: map[string]*flight{}}
}

func (m *Module) Name() string           { return "pool" }
func (m *Module) Dependencies() []string { return nil }

// Init never fails: wwd is a separate process that may start later than this daemon.
func (m *Module) Init(*core.Core) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cl == nil {
		m.cl = client.New(client.DefaultSocket())
	}
	m.ready = true
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error {
	m.mu.Lock()
	m.ready = false
	m.mu.Unlock()
	return nil
}

// Status is what /api/info reads to announce pool.v1 (the API exists; whether wwd is up is each call's answer).
func (m *Module) Status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"ready": m.ready}
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pool/hosts", m.handleHosts)
	mux.HandleFunc("GET /api/pool/facts/history", m.handleHistory)
	mux.HandleFunc("GET /api/pool/facts/series", m.handleSeries)
	m.registerControl(mux)
}

func (m *Module) client() *client.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cl
}

// hostsOf is the App's view of wwd's pool: each peer with its worker state (from its facts when it has them; the info's
// enabled flag alone for a peer never reached) and its labels (never null).
func hostsOf(p wire.Pool) wire.PoolHostsResponse {
	out := wire.PoolHostsResponse{Hosts: make([]wire.PoolHost, 0, len(p.Peers))}
	for _, peer := range p.Peers {
		w := wire.WorkerSettings{Enabled: peer.Info.Worker.Enabled}
		if peer.Facts != nil {
			w.Enabled, w.Slots, w.OwnerRule = peer.Facts.Worker.Enabled, peer.Facts.Worker.Slots, peer.Facts.Worker.OwnerRule
		}
		labels := peer.Info.Labels
		if labels == nil {
			labels = []string{}
		}
		out.Hosts = append(out.Hosts, wire.PoolHost{Info: peer.Info, Facts: peer.Facts, Worker: w, Labels: labels, Reachable: peer.Reachable})
	}
	return out
}

// apiError is a refused read: the status, code and detail the App is answered with. Every waiter on one flight gets the same.
type apiError struct {
	status       int
	code, detail string
}

// flight is one wwd call under way for a key. Readers that miss the cache while it runs wait on done and share its result
// instead of calling wwd themselves; only the leader (the reader that started it) writes the cache.
type flight struct {
	done chan struct{}
	body []byte
	err  *apiError
}

// serve answers the read named by key: a fresh cached answer if there is one; else the result of the flight already under way
// for key; else it leads a new one, taking an in-flight slot first (429 when all maxInFlight are taken - waiters do not hold
// a slot). Because a key has at most one flight and only its leader writes the cache, a slow old call can never overwrite a
// newer answer, and wwd is asked once per key however many readers there are.
func (m *Module) serve(w http.ResponseWriter, r *http.Request, key string, fetch func(ctx context.Context) (any, *apiError)) {
	if body, ok := m.cached(key); ok {
		writeBody(w, body)
		return
	}
	m.flightMu.Lock()
	if body, ok := m.cachedLocked(key); ok { // filled between the first look and the lock
		m.flightMu.Unlock()
		writeBody(w, body)
		return
	}
	if f, ok := m.flights[key]; ok {
		m.flightMu.Unlock()
		select {
		case <-f.done:
			writeFlight(w, f)
		case <-r.Context().Done():
		}
		return
	}
	f := &flight{done: make(chan struct{})}
	m.flights[key] = f
	m.flightMu.Unlock()

	f.body, f.err = m.lead(r, key, fetch)
	m.flightMu.Lock()
	delete(m.flights, key)
	m.flightMu.Unlock()
	close(f.done)
	writeFlight(w, f)
}

// lead is the leader's work: take a slot, call wwd (cut loose from the leader's request, so a leader that goes away does not
// fail the readers waiting on its result), cache a success.
func (m *Module) lead(r *http.Request, key string, fetch func(ctx context.Context) (any, *apiError)) (body []byte, aerr *apiError) {
	defer func() {
		// A panic in the fetch becomes a 500 here, so serve still frees the flight and closes done for the waiters.
		if rec := recover(); rec != nil {
			body, aerr = nil, &apiError{http.StatusInternalServerError, ErrWWDError, "reading the pool failed"}
		}
	}()
	select {
	case m.inflight <- struct{}{}:
		defer func() { <-m.inflight }()
	default:
		return nil, &apiError{http.StatusTooManyRequests, ErrBusy, "too many pool reads in flight; retry"}
	}
	v, aerr := fetch(context.WithoutCancel(r.Context()))
	if aerr != nil {
		return nil, aerr // an error is never cached
	}
	body, err := json.Marshal(v)
	if err != nil {
		return nil, &apiError{http.StatusInternalServerError, ErrWWDError, "encoding the answer failed"}
	}
	body = append(body, '\n')
	m.store(key, body)
	return body, nil
}

func writeFlight(w http.ResponseWriter, f *flight) {
	if f.err != nil {
		writeErr(w, f.err.status, f.err.code, f.err.detail)
		return
	}
	writeBody(w, f.body)
}

func writeBody(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// cached returns a fresh cached answer for key.
func (m *Module) cached(key string) ([]byte, bool) {
	m.flightMu.Lock()
	defer m.flightMu.Unlock()
	return m.cachedLocked(key)
}

func (m *Module) cachedLocked(key string) ([]byte, bool) {
	a, ok := m.cache[key]
	if !ok || m.now().Sub(a.at) >= answerTTL {
		return nil, false
	}
	return a.body, true
}

// store keeps a success for answerTTL. A module built without a cache map (a test's) keeps nothing.
func (m *Module) store(key string, body []byte) {
	m.flightMu.Lock()
	defer m.flightMu.Unlock()
	if m.cache == nil || len(body) > maxCachedBody {
		return
	}
	if len(m.cache) >= maxCached {
		now := m.now()
		for k, a := range m.cache { // drop what is stale; if all is fresh, start over rather than grow
			if now.Sub(a.at) >= answerTTL {
				delete(m.cache, k)
			}
		}
		if len(m.cache) >= maxCached {
			m.cache = map[string]cachedAnswer{}
		}
	}
	m.cache[key] = cachedAnswer{at: m.now(), body: body}
}

func (m *Module) handleHosts(w http.ResponseWriter, r *http.Request) {
	m.serve(w, r, "hosts", func(ctx context.Context) (any, *apiError) {
		p, err := m.client().Pool(ctx)
		if err != nil {
			return nil, wwdError(err)
		}
		return hostsOf(p), nil
	})
}

func (m *Module) handleHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// the default since is cut to the minute so that readers asking in the same minute share one key
	hq := wire.FactsHistoryQuery{Since: m.now().Add(-historyDefaultSince).UTC().Truncate(time.Minute), Step: q.Get("step")}
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, ErrBadRequest, "since must be RFC 3339")
			return
		}
		hq.Since = t
	}
	if hq.Step == "" {
		hq.Step = wire.Step5s
	}
	if hq.Step != wire.Step1m && hq.Step != wire.Step5s {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "step must be 1m or 5s")
		return
	}
	// another host's history goes through the local wwd (?host=<alias>): it answers for itself, or forwards to that peer (spec §10)
	hq.Host = q.Get("host")
	key := "history|" + hq.Host + "|" + hq.Since.Format(time.RFC3339) + "|" + hq.Step
	m.serve(w, r, key, func(ctx context.Context) (any, *apiError) {
		h, err := m.client().FactsHistory(ctx, hq)
		if err != nil {
			return nil, historyError(err)
		}
		if h.Records == nil {
			h.Records = []wire.Facts{}
		}
		return h, nil
	})
}

// historyError is wwdError for a read that may have been forwarded to a peer: a 4xx from wwd (an alias it does not know is 404, a bad
// query 400) is passed on as it is with the code wwd_rejected, so that the App can tell its own mistake from a broken host; a peer that
// could not be reached is wwd's 502, which stays 502 wwd_error.
func historyError(err error) *apiError {
	var se *client.StatusError
	if errors.As(err, &se) && se.Code >= 400 && se.Code < 500 {
		return &apiError{se.Code, ErrWWDRejected, "wwd answered " + http.StatusText(se.Code)}
	}
	return wwdError(err)
}

// wwdError maps a client error to the status the App reads.
func wwdError(err error) *apiError {
	var se *client.StatusError
	switch {
	case errors.Is(err, client.ErrUnavailable):
		return &apiError{http.StatusServiceUnavailable, ErrWWDUnavailable, "wwd is not running or did not answer"}
	case errors.Is(err, client.ErrBadResponse):
		return &apiError{http.StatusBadGateway, ErrWWDBadResponse, "wwd answered outside the contract"}
	case errors.As(err, &se):
		return &apiError{http.StatusBadGateway, ErrWWDError, "wwd answered " + http.StatusText(se.Code)}
	}
	return &apiError{http.StatusBadGateway, ErrWWDError, "wwd call failed"}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, ipeers.APIError{Error: code, Detail: detail})
}
