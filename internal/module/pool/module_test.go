package poolmod

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "wwd", "wire", "testdata", name))
	require.NoError(t, err)
	return b
}

// fakeWWD serves h on a unix socket (short path: macOS limits it to ~104 bytes) and returns a client for it.
func fakeWWD(t *testing.T, h http.Handler) *client.Client {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wwd")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "w.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return client.New(sock)
}

func serve(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}

func newModule(t *testing.T, cl *client.Client) *http.ServeMux {
	t.Helper()
	m := New()
	m.cl = cl
	m.now = func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) }
	m.cache = nil // most tests ask the same route twice with different fakes behind it; the cache has its own test
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	return mux
}

func call(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	return rr
}

func errCode(rr *httptest.ResponseRecorder) string {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &e)
	return e.Error
}

// poolOf builds the wwd pool the fake answers: the fixtures' mlab with its facts (reachable) and air19 never reached.
func poolOf(t *testing.T) []byte {
	t.Helper()
	info, err := wire.Decode[wire.Info](fixture(t, "info.json"))
	require.NoError(t, err)
	facts, err := wire.Decode[wire.Facts](fixture(t, "facts_darwin.json"))
	require.NoError(t, err)
	facts.Worker = wire.FactsWorker{Enabled: true, Slots: wire.Slots{Heavy: 2, Light: 3}, OwnerRule: "hid<5m"}
	air19 := wire.Info{HostID: "h-air19", Alias: "air19", Version: "0.1.0", Proto: 1, Platform: "darwin", Arch: "amd64"}
	b, err := json.Marshal(wire.Pool{Peers: []wire.Peer{
		{Info: info, Facts: &facts, Reachable: true},
		{Info: air19, Facts: nil, Reachable: false},
	}})
	require.NoError(t, err)
	return b
}

// The App's hosts answer is the contract's PoolHostsResponse: each peer with its info, facts, worker state and labels (never
// null), in wwd's order; a peer never reached has null facts and reachable false.
func TestHosts_MapsThePoolToTheAppShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/pool", serve(poolOf(t)))
	rr := call(newModule(t, fakeWWD(t, mux)), "/api/pool/hosts")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	got, err := wire.Decode[wire.PoolHostsResponse](rr.Body.Bytes())
	require.NoError(t, err, "the answer must pass the contract's own strict decode")
	require.Len(t, got.Hosts, 2)
	mlab, air := got.Hosts[0], got.Hosts[1]
	assert.Equal(t, "mlab", mlab.Info.Alias)
	assert.True(t, mlab.Reachable)
	require.NotNil(t, mlab.Facts)
	assert.Equal(t, wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 2, Light: 3}, OwnerRule: "hid<5m"}, mlab.Worker, "the worker state comes from the facts")
	assert.Equal(t, []string{"os:darwin", "arch:arm64", "repo:purdex"}, mlab.Labels)
	assert.Equal(t, "air19", air.Info.Alias)
	assert.False(t, air.Reachable)
	assert.Nil(t, air.Facts)
	assert.Equal(t, wire.WorkerSettings{}, air.Worker)
	assert.Contains(t, rr.Body.String(), `"labels":[]`, "labels are [] not null")
}

// wwd not running: 503 wwd_unavailable. wwd answering garbage: 502 wwd_bad_response. wwd answering an error: 502 wwd_error.
func TestHosts_WWDAbsentOrBrokenAnswersAClearStatus(t *testing.T) {
	absent := newModule(t, client.New(filepath.Join(t.TempDir(), "none.sock")))
	rr := call(absent, "/api/pool/hosts")
	assert.Equal(t, 503, rr.Code)
	assert.Equal(t, ErrWWDUnavailable, errCode(rr))

	bad := http.NewServeMux()
	bad.HandleFunc("GET /v1/pool", serve([]byte(`{"peers":[],"surprise":1}`)))
	rr = call(newModule(t, fakeWWD(t, bad)), "/api/pool/hosts")
	assert.Equal(t, 502, rr.Code)
	assert.Equal(t, ErrWWDBadResponse, errCode(rr))

	boom := http.NewServeMux()
	boom.HandleFunc("GET /v1/pool", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 500) })
	rr = call(newModule(t, fakeWWD(t, boom)), "/api/pool/hosts")
	assert.Equal(t, 502, rr.Code)
	assert.Equal(t, ErrWWDError, errCode(rr))
}

// History of the local host: the query is forwarded (since defaults to an hour back, step to 5s) and the answer is wwd's.
func TestHistory_ForwardsTheQueryForTheLocalHost(t *testing.T) {
	var since, step string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/info", serve(fixture(t, "info.json")))
	mux.HandleFunc("GET /v1/facts/history", func(w http.ResponseWriter, r *http.Request) {
		since, step = r.URL.Query().Get("since"), r.URL.Query().Get("step")
		serve(fixture(t, "facts_history_response.json"))(w, r)
	})
	m := newModule(t, fakeWWD(t, mux))

	rr := call(m, "/api/pool/facts/history?host=mlab&since=2026-10-18T01:00:00Z&step=1m")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.Equal(t, "2026-10-18T01:00:00Z", since)
	assert.Equal(t, "1m", step)
	got, err := wire.Decode[wire.FactsHistoryResponse](rr.Body.Bytes())
	require.NoError(t, err)
	assert.Equal(t, "1m", got.Step)
	assert.Contains(t, rr.Body.String(), `"records":[]`)

	// no host, no since, no step: the local host, the last hour, 5 s
	rr = call(m, "/api/pool/facts/history")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.Equal(t, "2026-10-18T11:00:00Z", since)
	assert.Equal(t, "5s", step)

	// the host may be named by its id as well
	assert.Equal(t, 200, call(m, "/api/pool/facts/history?host=h-mlab").Code)
}

func TestHistory_BadQueryAndOtherHost(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/info", serve(fixture(t, "info.json")))
	mux.HandleFunc("GET /v1/facts/history", serve(fixture(t, "facts_history_response.json")))
	m := newModule(t, fakeWWD(t, mux))
	for _, q := range []string{"?since=yesterday", "?step=2m", "?since=2026-10-18"} {
		rr := call(m, "/api/pool/facts/history"+q)
		assert.Equal(t, 400, rr.Code, q)
		assert.Equal(t, ErrBadRequest, errCode(rr), q)
	}
	rr := call(m, "/api/pool/facts/history?host=air19")
	assert.Equal(t, 501, rr.Code)
	assert.Equal(t, ErrHostNotLocal, errCode(rr))
}

func TestHistory_WWDAbsent(t *testing.T) {
	m := newModule(t, client.New(filepath.Join(t.TempDir(), "none.sock")))
	for _, p := range []string{"/api/pool/facts/history", "/api/pool/facts/history?host=mlab"} {
		rr := call(m, p)
		assert.Equal(t, 503, rr.Code, p)
		assert.Equal(t, ErrWWDUnavailable, errCode(rr), p)
	}
}

// The module always initialises, even with no wwd anywhere: the daemon's start and its other routes do not depend on it.
func TestInit_NeverFailsWithoutWWD(t *testing.T) {
	t.Setenv("PDX_WWD_SOCK", filepath.Join(t.TempDir(), "none.sock"))
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}, Registry: core.NewServiceRegistry()})
	require.NoError(t, m.Init(c))
	require.NoError(t, m.Start(context.Background()))
	assert.Equal(t, map[string]any{"ready": true}, m.Status())
}

// The real module registered in a real core, asked through GET /api/info (the lesson of fleet.v1): pool.v1 is declared while
// the module is up and gone after Stop.
func TestInfo_AnnouncesPoolV1ForTheRealModule(t *testing.T) {
	t.Setenv("PDX_WWD_SOCK", filepath.Join(t.TempDir(), "none.sock"))
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}, Registry: core.NewServiceRegistry()})
	c.AddModule(m)
	require.NoError(t, m.Init(c))
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	caps := func() []any {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		list, _ := body["capabilities"].([]any)
		return list
	}
	require.Contains(t, caps(), "pool.v1")
	require.NoError(t, m.Stop(context.Background()))
	require.NotContains(t, caps(), "pool.v1")
}

// A wwd that answers records as null still gives the App an array.
func TestHistory_NullRecordsBecomeAnEmptyArray(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", serve([]byte(`{"step":"5s","records":null}`)))
	rr := call(newModule(t, fakeWWD(t, mux)), "/api/pool/facts/history")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"records":[]`)
}

// However many readers there are, wwd is asked once per key per TTL: the second read inside the TTL is the first's answer, one
// after it asks again, and an error is never kept.
func TestCache_OneWWDCallPerKeyPerTTL(t *testing.T) {
	var calls int
	var fail bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/pool", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if fail {
			http.Error(w, "x", 500)
			return
		}
		w.Write(poolOf(t))
	})
	m := New()
	m.cl = fakeWWD(t, mux)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	srv := http.NewServeMux()
	m.RegisterRoutes(srv)

	first := call(srv, "/api/pool/hosts")
	second := call(srv, "/api/pool/hosts")
	assert.Equal(t, 1, calls, "two reads inside the TTL are one wwd call")
	assert.Equal(t, first.Body.String(), second.Body.String())

	now = now.Add(answerTTL)
	call(srv, "/api/pool/hosts")
	assert.Equal(t, 2, calls, "after the TTL wwd is asked again")

	now = now.Add(answerTTL)
	fail = true
	assert.Equal(t, 502, call(srv, "/api/pool/hosts").Code)
	assert.Equal(t, 502, call(srv, "/api/pool/hosts").Code)
	assert.Equal(t, 4, calls, "an error is not cached")
}

// parkedWWD is a fake wwd whose history/pool answers wait for release, counting the calls that arrived.
type parkedWWD struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func newParkedWWD(t *testing.T) (*parkedWWD, *client.Client) {
	p := &parkedWWD{entered: make(chan struct{}, 64), release: make(chan struct{})}
	mux := http.NewServeMux()
	wait := func(w http.ResponseWriter, r *http.Request, body []byte) {
		p.calls.Add(1)
		p.entered <- struct{}{}
		select {
		case <-p.release:
		case <-r.Context().Done():
		}
		w.Write(body)
	}
	mux.HandleFunc("GET /v1/pool", func(w http.ResponseWriter, r *http.Request) { wait(w, r, poolOf(t)) })
	mux.HandleFunc("GET /v1/facts/history", func(w http.ResponseWriter, r *http.Request) {
		wait(w, r, []byte(`{"step":"5s","records":[]}`))
	})
	cl := fakeWWD(t, mux)
	cl.Timeout = 10 * time.Second
	return p, cl
}

func fixedModule(cl *client.Client) (*Module, *http.ServeMux) {
	m := New()
	m.cl = cl
	m.now = func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) }
	srv := http.NewServeMux()
	m.RegisterRoutes(srv)
	return m, srv
}

// Single flight: with the cache empty and wwd parked, N concurrent reads of one key are ONE wwd call, and every reader gets
// the same answer. Readers that wait for the flight hold no in-flight slot.
func TestSingleFlight_ConcurrentReadsOfOneKeyCallWWDOnce(t *testing.T) {
	p, cl := newParkedWWD(t)
	_, srv := fixedModule(cl)
	const n = 3 * maxInFlight // more readers than slots: the waiters must not need one
	bodies := make(chan string, n)
	codes := make(chan int, n)
	for i := 0; i < n; i++ {
		go func() {
			rr := call(srv, "/api/pool/hosts")
			codes <- rr.Code
			bodies <- rr.Body.String()
		}()
	}
	<-p.entered // the leader is in wwd
	time.Sleep(100 * time.Millisecond)
	close(p.release)
	var first string
	for i := 0; i < n; i++ {
		assert.Equal(t, 200, <-codes)
		b := <-bodies
		if i == 0 {
			first = b
		}
		assert.Equal(t, first, b, "every reader gets the leader's answer")
	}
	assert.EqualValues(t, 1, p.calls.Load(), "wwd was asked once")
}

// A fetch that panics must not strand the flight: the reader gets a 500, the key's flight is gone (the next read starts a new
// one) and the waiters' done channel is closed.
func TestSingleFlight_APanickingFetchAnswers500AndFreesTheFlight(t *testing.T) {
	m := New()
	m.now = func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) }
	rr := httptest.NewRecorder()
	assert.NotPanics(t, func() {
		m.serve(rr, httptest.NewRequest(http.MethodGet, "/x", nil), "k", func(context.Context) (any, *apiError) { panic("boom") })
	})
	assert.Equal(t, 500, rr.Code)
	m.flightMu.Lock()
	assert.Empty(t, m.flights, "the flight is released")
	m.flightMu.Unlock()
	rr = httptest.NewRecorder()
	m.serve(rr, httptest.NewRequest(http.MethodGet, "/x", nil), "k", func(context.Context) (any, *apiError) { return map[string]int{"a": 1}, nil })
	assert.Equal(t, 200, rr.Code, "a later read works")
}

// Different keys are different flights: each is asked once.
func TestSingleFlight_DifferentKeysAreSeparateFlights(t *testing.T) {
	p, cl := newParkedWWD(t)
	_, srv := fixedModule(cl)
	paths := []string{
		"/api/pool/facts/history?since=2026-10-18T01:00:00Z", "/api/pool/facts/history?since=2026-10-18T02:00:00Z",
		"/api/pool/facts/history?since=2026-10-18T01:00:00Z&step=1m",
	}
	done := make(chan int, 6)
	for _, path := range paths {
		for i := 0; i < 2; i++ {
			go func() { done <- call(srv, path).Code }()
		}
	}
	for range paths {
		<-p.entered
	}
	time.Sleep(100 * time.Millisecond)
	close(p.release)
	for i := 0; i < 6; i++ {
		assert.Equal(t, 200, <-done)
	}
	assert.EqualValues(t, 3, p.calls.Load(), "three keys, three wwd calls")
}

// A runaway client asking maxInFlight different things is turned away with 429 at the next different thing, which holds
// no slot and reaches no wwd; the reader of an existing key still shares its flight; a slot is free again when one ends.
func TestInFlight_ExcessDistinctReadsAreTurnedAway(t *testing.T) {
	p, cl := newParkedWWD(t)
	_, srv := fixedModule(cl)
	path := func(i int) string { return fmt.Sprintf("/api/pool/facts/history?since=2026-10-18T%02d:00:00Z", i) }
	done := make(chan int, maxInFlight+1)
	for i := 0; i < maxInFlight; i++ {
		go func() { done <- call(srv, path(i)).Code }()
	}
	for i := 0; i < maxInFlight; i++ {
		<-p.entered
	}
	rr := call(srv, path(maxInFlight))
	assert.Equal(t, 429, rr.Code)
	assert.Equal(t, ErrBusy, errCode(rr))
	go func() { done <- call(srv, path(0)).Code }() // a waiter on an existing flight: no slot needed
	time.Sleep(50 * time.Millisecond)
	assert.EqualValues(t, maxInFlight, p.calls.Load(), "the 429 and the waiter reached no wwd")
	close(p.release)
	for i := 0; i < maxInFlight+1; i++ {
		assert.Equal(t, 200, <-done)
	}
	assert.Equal(t, 200, call(srv, path(maxInFlight)).Code, "a slot is free again")
}
