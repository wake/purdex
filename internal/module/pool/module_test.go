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

// The hosts answer asks wwd for the liveness of the peers and passes it on; a wwd that does not know the query answers without it, and the
// hosts then have none (the App sees nothing it did not see before).
func TestHosts_AsksForLivenessAndPassesItOn(t *testing.T) {
	info, err := wire.Decode[wire.Info](fixture(t, "info.json"))
	require.NoError(t, err)
	air19 := wire.Info{HostID: "h-air19", Alias: "air19", Version: "0.1.0", Proto: 3, Platform: "darwin", Arch: "amd64"}
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	since := at.Add(-12 * time.Minute)
	withLiveness, err := json.Marshal(wire.Pool{Peers: []wire.Peer{{Info: info, Reachable: true}, {Info: air19, Liveness: wire.LivenessHostDown, LivenessAt: &at, LivenessSince: &since}}})
	require.NoError(t, err)

	var asked atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/pool", func(w http.ResponseWriter, r *http.Request) {
		asked.Store(r.URL.RawQuery)
		serve(withLiveness)(w, r)
	})
	rr := call(newModule(t, fakeWWD(t, mux)), "/api/pool/hosts")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.Equal(t, wire.PoolLivenessQuery, asked.Load(), "the module asks for the fields")
	got, err := wire.Decode[wire.PoolHostsResponse](rr.Body.Bytes())
	require.NoError(t, err)
	require.Len(t, got.Hosts, 2)
	assert.Equal(t, wire.LivenessHostDown, got.Hosts[1].Liveness)
	require.NotNil(t, got.Hosts[1].LivenessAt)
	assert.True(t, got.Hosts[1].LivenessAt.Equal(at))
	require.NotNil(t, got.Hosts[1].LivenessSince)
	assert.True(t, got.Hosts[1].LivenessSince.Equal(since), "the App shows how long it has been down")

	// an older wwd ignores the query
	mux2 := http.NewServeMux()
	mux2.HandleFunc("GET /v1/pool", serve(poolOf(t)))
	rr = call(newModule(t, fakeWWD(t, mux2)), "/api/pool/hosts")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.NotContains(t, rr.Body.String(), "liveness")
}

// A host with facts is judged in Go, once: the codes that say why its headroom is 0, and its composite load with the reading that
// drives it. A host without facts has neither (null).
func TestHosts_ReasonsAndLoad(t *testing.T) {
	info, err := wire.Decode[wire.Info](fixture(t, "info.json"))
	require.NoError(t, err)
	f, err := wire.Decode[wire.Facts](fixture(t, "facts_darwin.json"))
	require.NoError(t, err)
	f.Worker = wire.FactsWorker{Enabled: true, Slots: wire.Slots{Heavy: 2, Light: 3}, Running: wire.Slots{Heavy: 2}}
	f.Derived = wire.Derived{Zone: wire.ZoneWarning, Headroom: wire.Slots{Light: 3}}
	f.Exception = []wire.FactExc{}
	f.Owner = nil
	f.CPU = wire.CPU{NCPU: 10, IdlePct: 20}
	f.Mem = wire.Mem{TotalMB: 16000, AvailableMB: 4000, Pressure: 1}
	f.Disk = []wire.Disk{{Mount: "/", UsedPct: 40}, {Mount: "/data", UsedPct: 91}}
	air19 := wire.Info{HostID: "h-air19", Alias: "air19", Version: "0.1.0", Proto: 1, Platform: "darwin", Arch: "amd64"}
	body, err := json.Marshal(wire.Pool{Peers: []wire.Peer{{Info: info, Facts: &f, Reachable: true}, {Info: air19, Reachable: false}}})
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/pool", serve(body))
	rr := call(newModule(t, fakeWWD(t, mux)), "/api/pool/hosts")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	got, err := wire.Decode[wire.PoolHostsResponse](rr.Body.Bytes())
	require.NoError(t, err)
	mlab, air := got.Hosts[0], got.Hosts[1]
	require.NotNil(t, mlab.HeadroomReasons)
	assert.Equal(t, []string{"zone_warning", "slots_full"}, mlab.HeadroomReasons.Heavy)
	assert.Equal(t, []string{}, mlab.HeadroomReasons.Light)
	require.NotNil(t, mlab.Load)
	assert.InDelta(t, 91, mlab.Load.Score, 0.01, "the fullest disk, over 85, is the largest of: cpu 80, mem 75, level 30")
	assert.Equal(t, wire.LoadDriverDisk, mlab.Load.Driver)
	assert.Nil(t, air.HeadroomReasons)
	assert.Nil(t, air.Load)
	assert.Contains(t, rr.Body.String(), `"headroom_reasons":null`)
}

// How the load reads a record: cpu from idle (not at all where no cpu count was read), memory from total and available, paging from
// page-out plus swap-out, the disk from the fullest mount.
func TestLoadOf(t *testing.T) {
	f64 := func(v float64) *float64 { return &v }
	var f wire.Facts
	f.CPU = wire.CPU{NCPU: 8, IdlePct: 70}
	f.Mem = wire.Mem{TotalMB: 1000, AvailableMB: 900, Pressure: 1, PageOutRate: f64(30), SwapOutRate: f64(40)}
	l := loadOf(f)
	assert.Equal(t, wire.LoadDriverPaging, l.Driver, "30 + 40 pages/s")
	assert.InDelta(t, 70, l.Score, 0.01)
	f.CPU = wire.CPU{} // the cpu was not read: idle 0 is not "fully busy"
	f.Mem.PageOutRate, f.Mem.SwapOutRate = nil, nil
	l = loadOf(f)
	assert.Equal(t, wire.LoadDriverLevel, l.Driver)
	assert.InDelta(t, 30, l.Score, 0.01)
	f.Mem.TotalMB = 0 // no total: memory is not judged
	assert.Equal(t, wire.LoadDriverLevel, loadOf(f).Driver)
	// two valid, finite readings must not add up to +Inf (JSON cannot carry it, and one peer would fail the whole answer)
	f.Mem.PageOutRate, f.Mem.SwapOutRate = f64(1e308), f64(1e308)
	l = loadOf(f)
	assert.Equal(t, wire.LoadDriverPaging, l.Driver)
	assert.Equal(t, 100.0, l.Score)
	_, err := json.Marshal(l)
	assert.NoError(t, err)
	f.Mem.PageOutRate, f.Mem.SwapOutRate = f64(-5), f64(-1e308) // negative: it counts for nothing
	assert.Equal(t, wire.LoadDriverLevel, loadOf(f).Driver)
}

// wwd not running: 503 wwd_unavailable. wwd answering garbage: 502 wwd_bad_response. wwd answering an error: 502 wwd_error.
func TestHosts_WWDAbsentOrBrokenAnswersAClearStatus(t *testing.T) {
	absent := newModule(t, client.New(filepath.Join(t.TempDir(), "none.sock")))
	rr := call(absent, "/api/pool/hosts")
	assert.Equal(t, 503, rr.Code)
	assert.Equal(t, ErrWWDUnavailable, errCode(rr))

	bad := http.NewServeMux()
	bad.HandleFunc("GET /v1/pool", serve([]byte(`{"peers":"not a list","surprise":1}`))) // a field of a later build is skipped; a wrong value is not
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
	var since, step, host string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", func(w http.ResponseWriter, r *http.Request) {
		since, step, host = r.URL.Query().Get("since"), r.URL.Query().Get("step"), r.URL.Query().Get("host")
		serve(fixture(t, "facts_history_response.json"))(w, r)
	})
	m := newModule(t, fakeWWD(t, mux))

	rr := call(m, "/api/pool/facts/history?host=mlab&since=2026-10-18T01:00:00Z&step=1m")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.Equal(t, "2026-10-18T01:00:00Z", since)
	assert.Equal(t, "1m", step)
	assert.Equal(t, "mlab", host, "the host goes to the local wwd, which answers for itself or forwards")
	got, err := wire.Decode[wire.FactsHistoryResponse](rr.Body.Bytes())
	require.NoError(t, err)
	assert.Equal(t, "1m", got.Step)
	assert.Contains(t, rr.Body.String(), `"records":[]`)

	// no host, no since, no step: the local host, the last hour, 5 s
	rr = call(m, "/api/pool/facts/history")
	assert.Equal(t, "", host, "no host asked, none sent")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.Equal(t, "2026-10-18T11:00:00Z", since)
	assert.Equal(t, "5s", step)

	// the host may be named by its id as well
	assert.Equal(t, 200, call(m, "/api/pool/facts/history?host=h-mlab").Code)
}

func TestHistory_BadQuery(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", serve(fixture(t, "facts_history_response.json")))
	m := newModule(t, fakeWWD(t, mux))
	for _, q := range []string{"?since=yesterday", "?step=2m", "?since=2026-10-18"} {
		rr := call(m, "/api/pool/facts/history"+q)
		assert.Equal(t, 400, rr.Code, q)
		assert.Equal(t, ErrBadRequest, errCode(rr), q)
	}
}

// The history of another host goes through the local wwd: its answer is the App's, a 4xx of wwd's (an alias it does not know is 404) is
// passed on as wwd_rejected with its status, a peer that cannot be reached is wwd's 502 and stays 502 wwd_error.
func TestHistory_AnotherHostThroughTheLocalWWD(t *testing.T) {
	var asked []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/facts/history", func(w http.ResponseWriter, r *http.Request) {
		h := r.URL.Query().Get("host")
		asked = append(asked, h)
		switch h {
		case "nowhere":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"unknown host","host":"nowhere"}`))
		case "air19":
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte(`{"host":"air19","reason":"dial tcp: refused"}`))
		default:
			serve([]byte(`{"step":"1m","records":[]}`))(w, r)
		}
	})
	m := newModule(t, fakeWWD(t, mux))

	rr := call(m, "/api/pool/facts/history?host=mserv&step=1m")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"step":"1m"`)

	rr = call(m, "/api/pool/facts/history?host=nowhere")
	assert.Equal(t, 404, rr.Code)
	assert.Equal(t, ErrWWDRejected, errCode(rr))

	rr = call(m, "/api/pool/facts/history?host=air19")
	assert.Equal(t, 502, rr.Code)
	assert.Equal(t, ErrWWDError, errCode(rr))
	assert.Equal(t, []string{"mserv", "nowhere", "air19"}, asked)
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
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	assert.Equal(t, map[string]any{"ready": true, "events": true}, m.Status(), "the watch runs, wwd or not")
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
	require.Contains(t, caps(), "pool.series.v1")
	require.Contains(t, caps(), "pool.assess.v1")
	require.NoError(t, m.Stop(context.Background()))
	require.NotContains(t, caps(), "pool.v1")
	require.NotContains(t, caps(), "pool.series.v1")
	require.NotContains(t, caps(), "pool.assess.v1")
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

// The non-null shape of the judgements, as a JSON sample the App can copy: pool_hosts_response_reasons.json. mlab is a warning-zone
// host with heavy full and a nearly full disk; the second host has light at 0 for no reason its record shows (reason "unknown"); the
// third was never reached (null reasons and load). The answer must equal the sample once decoded (JSONEq: the sample is hand-indented,
// the handler's output is compact; the keys, values and nulls are what the App reads).
func TestHosts_ReasonsAndLoadGolden(t *testing.T) {
	info, err := wire.Decode[wire.Info](fixture(t, "info.json"))
	require.NoError(t, err)
	f, err := wire.Decode[wire.Facts](fixture(t, "facts_darwin.json"))
	require.NoError(t, err)
	f.Worker = wire.FactsWorker{Enabled: true, Slots: wire.Slots{Heavy: 2, Light: 3}, Running: wire.Slots{Heavy: 2}}
	f.Derived = wire.Derived{Zone: wire.ZoneWarning, Headroom: wire.Slots{Light: 3}}
	f.Exception = []wire.FactExc{}
	f.Owner = nil
	f.CPU = wire.CPU{NCPU: 10, IdlePct: 20}
	f.Mem = wire.Mem{TotalMB: 16000, AvailableMB: 4000, Pressure: 1}
	f.Disk = []wire.Disk{{Mount: "/", UsedPct: 40}, {Mount: "/data", UsedPct: 91}}

	odd, err := wire.Decode[wire.Facts](fixture(t, "facts_linux.json"))
	require.NoError(t, err)
	odd.Worker = wire.FactsWorker{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 3}}
	odd.Derived = wire.Derived{Zone: wire.ZoneNormal, Headroom: wire.Slots{Heavy: 1}}
	odd.Exception = []wire.FactExc{}
	odd.Owner = nil
	odd.CPU = wire.CPU{NCPU: 4, IdlePct: 50}
	odd.Mem = wire.Mem{TotalMB: 8000, AvailableMB: 6000, Pressure: 1}
	odd.Disk = []wire.Disk{{Mount: "/", UsedPct: 30}}
	linux := wire.Info{HostID: "h-mserv", Alias: "mserv", Version: "0.1.0", Proto: 1, Platform: "linux", Arch: "amd64"}
	air19 := wire.Info{HostID: "h-air19", Alias: "air19", Version: "0.1.0", Proto: 1, Platform: "darwin", Arch: "amd64"}

	body, err := json.Marshal(wire.Pool{Peers: []wire.Peer{
		{Info: info, Facts: &f, Reachable: true},
		{Info: linux, Facts: &odd, Reachable: true},
		{Info: air19, Reachable: false},
	}})
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/pool", serve(body))
	rr := call(newModule(t, fakeWWD(t, mux)), "/api/pool/hosts")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, string(fixture(t, "pool_hosts_response_reasons.json")), rr.Body.String())
}
