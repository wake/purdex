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
	ErrHostNotLocal   = "host_not_local"   // 501: history of another host is not served by this daemon yet
	ErrBusy           = "busy"             // 429: too many wwd calls in flight
)

// maxInFlight bounds the wwd calls the pool routes have under way at once: a paired phone may read these routes, and each
// call holds a socket and up to the client's body cap in memory, so a runaway client is turned away instead of queued.
const maxInFlight = 8

// Module is the pool module.
type Module struct {
	mu    sync.Mutex
	ready bool // Init ran and Stop has not: pool.v1 is announced
	// cl is the local wwd's client; nil makes one for client.DefaultSocket() at Init.
	cl       *client.Client
	now      func() time.Time
	inflight chan struct{} // a semaphore of maxInFlight
}

func New() *Module { return &Module{now: time.Now, inflight: make(chan struct{}, maxInFlight)} }

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
			w.Enabled, w.Slots = peer.Facts.Worker.Enabled, peer.Facts.Worker.Slots
		}
		labels := peer.Info.Labels
		if labels == nil {
			labels = []string{}
		}
		out.Hosts = append(out.Hosts, wire.PoolHost{Info: peer.Info, Facts: peer.Facts, Worker: w, Labels: labels, Reachable: peer.Reachable})
	}
	return out
}

// enter takes an in-flight slot or answers 429; the caller defers the returned release when ok.
func (m *Module) enter(w http.ResponseWriter) (release func(), ok bool) {
	select {
	case m.inflight <- struct{}{}:
		return func() { <-m.inflight }, true
	default:
		writeErr(w, http.StatusTooManyRequests, ErrBusy, "too many pool reads in flight; retry")
		return nil, false
	}
}

func (m *Module) handleHosts(w http.ResponseWriter, r *http.Request) {
	release, ok := m.enter(w)
	if !ok {
		return
	}
	defer release()
	p, err := m.client().Pool(r.Context())
	if err != nil {
		writeWWDErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hostsOf(p))
}

func (m *Module) handleHistory(w http.ResponseWriter, r *http.Request) {
	release, ok := m.enter(w)
	if !ok {
		return
	}
	defer release()
	q := r.URL.Query()
	hq := wire.FactsHistoryQuery{Since: m.now().Add(-historyDefaultSince).UTC(), Step: q.Get("step")}
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
	cl := m.client()
	if host := q.Get("host"); host != "" {
		info, err := cl.Info(r.Context())
		if err != nil {
			writeWWDErr(w, err)
			return
		}
		if host != info.Alias && host != info.HostID {
			writeErr(w, http.StatusNotImplemented, ErrHostNotLocal, "this daemon serves the history of its own host only ("+info.Alias+")")
			return
		}
	}
	h, err := cl.FactsHistory(r.Context(), hq)
	if err != nil {
		writeWWDErr(w, err)
		return
	}
	if h.Records == nil {
		h.Records = []wire.Facts{}
	}
	writeJSON(w, http.StatusOK, h)
}

// writeWWDErr maps a client error to the status the App reads.
func writeWWDErr(w http.ResponseWriter, err error) {
	var se *client.StatusError
	switch {
	case errors.Is(err, client.ErrUnavailable):
		writeErr(w, http.StatusServiceUnavailable, ErrWWDUnavailable, "wwd is not running or did not answer")
	case errors.Is(err, client.ErrBadResponse):
		writeErr(w, http.StatusBadGateway, ErrWWDBadResponse, "wwd answered outside the contract")
	case errors.As(err, &se):
		writeErr(w, http.StatusBadGateway, ErrWWDError, "wwd answered "+http.StatusText(se.Code))
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
