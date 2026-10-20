// Package poolmod is the Purdex daemon's side of the resource pool (wwd spec §10): the routes the App reads the pool from,
// answered from the local wwd's unix socket. wwd being absent or broken never affects the daemon: the module always
// initialises, and a route answers a clear error status until wwd is there.
package poolmod

import (
	"context"
	"encoding/json"
	"errors"
	"log"
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

	// pool.changed (events.go); all under mu
	core        *core.Core
	logf        func(format string, args ...any)
	emit        func(core.HostEvent) // where pool.changed goes; nil: the core's host events
	pollEvery   time.Duration        // how often the local wwd's pool is read
	mergeWithin time.Duration        // the least time between two notices for one host
	watching    bool                 // Start ran and Stop has not: pool.events.v1 is announced
	cancel      context.CancelFunc   // ends the watch loop
	done        chan struct{}        // closed when that loop has ended
	sentAt      map[string]time.Time // host alias -> when its last notice went
	pending     map[string]*time.Timer
	// sending counts the notices that have been let through (watching was true, under mu) and are not yet emitted; Stop waits on sendIdle
	// until it is 0, so nothing is emitted once Stop has returned. emit must not call Stop (the core's BroadcastEvent does not).
	sending  int
	sendIdle *sync.Cond
	// self is who the local wwd says it is (the first peer of its pool: wwd lists this host first), read by the watch; nil before the
	// first read and while wwd cannot be read. Under mu.
	self *wire.Info
	// hosts is the aliases of the local wwd's pool as the watch last read it, this host first; the alerts watch asks about each (under mu).
	hosts       *poolHosts
	alertsEvery time.Duration // how often every host's alerts are read (watchAlerts)
	alertKick   chan struct{} // the local wwd's `alert` event: read the local host's alerts now (capacity 1)
}

func New() *Module {
	m := &Module{
		now: time.Now, inflight: make(chan struct{}, maxInFlight), cache: map[string]cachedAnswer{}, flights: map[string]*flight{},
		logf: log.Printf, pollEvery: defaultPollEvery, mergeWithin: defaultMergeWithin,
		sentAt: map[string]time.Time{}, pending: map[string]*time.Timer{},
		alertsEvery: defaultAlertsEvery, alertKick: make(chan struct{}, 1),
	}
	m.sendIdle = sync.NewCond(&m.mu)
	return m
}

func (m *Module) Name() string           { return "pool" }
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

// Start opens the watch that sends pool.changed, in the background; wwd not running is retried and never fails the daemon. A second
// Start while it runs does nothing; a Start after Stop runs it again, but not while the loop of the earlier Start has still not
// ended (a Stop whose ctx ran out leaves it ending): that Start is refused rather than run beside the old loop.
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
			return errors.New("pool: the watch loop of the earlier Start has not ended yet")
		}
	}
	if m.sentAt == nil {
		m.sentAt = map[string]time.Time{}
	}
	if m.pending == nil {
		m.pending = map[string]*time.Timer{}
	}
	if m.logf == nil {
		m.logf = log.Printf
	}
	if m.pollEvery <= 0 {
		m.pollEvery = defaultPollEvery
	}
	if m.mergeWithin <= 0 {
		m.mergeWithin = defaultMergeWithin
	}
	if m.alertsEvery <= 0 {
		m.alertsEvery = defaultAlertsEvery
	}
	if m.alertKick == nil {
		m.alertKick = make(chan struct{}, 1)
	}
	if m.cl == nil {
		m.cl = client.New(client.DefaultSocket())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.cancel, m.done, m.watching = cancel, done, true
	m.self = nil  // the new watch has not read wwd yet; the socket may be another wwd's than the last run's
	m.hosts = nil // nor has it read the pool the alerts watch asks about: the last run's hosts are not this run's
	select {      // an alert event of the last run is not one of this run's
	case <-m.alertKick:
	default:
	}
	if m.core != nil {
		m.ready = true // Init ran, and a Stop before this Start had cleared it: the pool is serving again
	}
	var loops sync.WaitGroup
	for _, loop := range []func(context.Context){m.watch, m.watchAlerts, m.watchAlertEvents} {
		loops.Add(1)
		go func() {
			defer loops.Done()
			loop(ctx)
		}()
	}
	go func() {
		loops.Wait()
		close(done)
	}()
	return nil
}

// Stop ends the watch and drops the notices waiting for their window, and waits for the loop as long as ctx allows. The loop is
// remembered until it has ended, so a Stop that ran out of time can be repeated and a Start does not begin a second loop beside it.
func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	m.ready = false
	m.watching = false
	m.self = nil // nothing reads wwd any more, so nothing keeps this true
	cancel, done := m.cancel, m.done
	m.cancel = nil
	for host, t := range m.pending {
		t.Stop()
		delete(m.pending, host)
	}
	// a notice already let through is emitted before Stop returns; no new one is let through (watching is false), and a timer callback that
	// was waiting for mu finds its entry gone
	for m.sending > 0 {
		m.sendIdle.Wait()
	}
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

// Status is what /api/info reads to announce pool.v1 (the API exists; whether wwd is up is each call's answer) and, while the watch
// runs, pool.events.v1. While the watch knows who the local wwd is, it also gives "wwd" {host_id, alias}: the identity /api/info carries
// (pool.identity.v1); the key is missing, not empty, before the first read and while wwd cannot be read.
func (m *Module) Status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := map[string]any{"ready": m.ready, "events": m.watching, "alerts": m.watching}
	if m.self != nil {
		st["wwd"] = map[string]string{"host_id": m.self.HostID, "alias": m.self.Alias}
	}
	return st
}

// setSelf records who the local wwd is from its pool (nil: it cannot be read, or names no one).
func (m *Module) setSelf(p *wire.Pool) {
	var self *wire.Info
	if p != nil && len(p.Peers) > 0 && p.Peers[0].Info.HostID != "" {
		info := p.Peers[0].Info
		self = &info
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.watching { // a read that comes back after Stop: nothing is watching, so nothing keeps this true
		return
	}
	m.self = self
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/pool/hosts", m.handleHosts)
	mux.HandleFunc("GET /api/pool/hosts/{alias}/exceptions", m.handleExceptions)
	mux.HandleFunc("GET /api/pool/hosts/{alias}/alerts", m.handleAlerts)
	mux.HandleFunc("GET /api/pool/hosts/{alias}/observations", m.handleObservations)
	mux.HandleFunc("GET /api/pool/hosts/{alias}/undeclared", m.handleUndeclared)
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
		h := wire.PoolHost{Info: peer.Info, Facts: peer.Facts, Worker: w, Labels: labels, Reachable: peer.Reachable,
			Liveness: peer.Liveness, LivenessAt: peer.LivenessAt, LivenessSince: peer.LivenessSince}
		assess(&h)
		out.Hosts = append(out.Hosts, h)
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

// forget drops the answer kept for key, so that the next read asks wwd: a notice that something changed must not be followed by a read of
// the answer from before it.
func (m *Module) forget(key string) {
	m.flightMu.Lock()
	delete(m.cache, key)
	m.flightMu.Unlock()
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
		p, err := m.client().PoolWithLiveness(ctx)
		if err != nil {
			return nil, wwdError(err)
		}
		return hostsOf(p), nil
	})
}

// handleExceptions is a host's exceptions with their holds (#3479 G4), asked of the local wwd with that host. A host whose wwd predates
// the route is "not known" (known: false), not an error; the errors are the history read's.
func (m *Module) handleExceptions(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	if !validAlias(alias) {
		writeErr(w, http.StatusBadRequest, ErrBadRequest, "the host alias is empty, a dot name (. or ..), too long or holds a control character")
		return
	}
	m.serve(w, r, "exceptions|"+alias, func(ctx context.Context) (any, *apiError) {
		list, known, err := m.client().Exceptions(ctx, alias)
		if err != nil {
			return nil, historyError(err)
		}
		return wire.PoolHostExceptions{Host: alias, Known: known, Exceptions: list}, nil
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
