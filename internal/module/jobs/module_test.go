package jobsmod

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

const epoch = "0123456789abcdef0123456789abcdef"

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

func jobOf(t *testing.T, id string, state wire.JobState) wire.Job {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "wwd", "wire", "testdata", "job_queued.json"))
	require.NoError(t, err)
	j, err := wire.Decode[wire.Job](b)
	require.NoError(t, err)
	j.ID, j.State = id, state
	if state.Terminal() {
		code := 0
		now := j.CreatedAt
		j.Claim = nil
		j.Result = &wire.Result{Exit: &code, Summary: &wire.Summary{Failures: []wire.Failure{}, Tail: []string{}}, Log: id + "/log", Host: "mlab", StartedAt: &now, FinishedAt: &now}
	}
	require.NoError(t, j.Validate())
	return j
}

func newMux(t *testing.T, cl *client.Client) *http.ServeMux {
	t.Helper()
	m := New()
	m.cl = cl
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	return mux
}

func get(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
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

// The list is wwd's answer as it is: the jobs, the cursor to read on from and the database epoch. The filters, the host, the cursor
// and the limit reach wwd; a list that names no limit asks for one page.
func TestList_PassesTheQueryAndReturnsTheCursorAndEpoch(t *testing.T) {
	var q map[string][]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		writeJSON(w, 200, wire.JobsListResponse{Jobs: []wire.Job{jobOf(t, "j_a", wire.StateQueued)}, Cursor: "7", Epoch: epoch})
	})
	rr := get(newMux(t, fakeWWD(t, mux)), "/api/pool/jobs?state=queued&session=s1&host=air19&after=3&limit=50")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	var got wire.JobsListResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "7", got.Cursor)
	assert.Equal(t, epoch, got.Epoch)
	require.Len(t, got.Jobs, 1)
	assert.Equal(t, "j_a", got.Jobs[0].ID)
	assert.Equal(t, []string{"queued"}, q["state"])
	assert.Equal(t, []string{"s1"}, q["submitter.session"])
	assert.Equal(t, []string{"air19"}, q["host"])
	assert.Equal(t, []string{"3"}, q["after"])
	assert.Equal(t, []string{"50"}, q["limit"])

	get(newMux(t, fakeWWD(t, mux)), "/api/pool/jobs")
	assert.Equal(t, []string{"200"}, q["limit"], "no limit asked: one page, not every job wwd keeps")
	assert.Empty(t, q["host"], "no host asked: the local wwd answers for itself")
}

func TestList_NoJobsIsAnEmptyArray(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"jobs": nil, "cursor": "0", "epoch": epoch})
	})
	rr := get(newMux(t, fakeWWD(t, mux)), "/api/pool/jobs")
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"jobs":[]`)
}

// A request that is wrong is refused here: wwd is not asked.
func TestList_RefusesABadQueryWithoutAskingWWD(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	h := newMux(t, fakeWWD(t, mux))
	for _, path := range []string{
		"/api/pool/jobs?state=nope", "/api/pool/jobs?limit=0", "/api/pool/jobs?limit=abc", "/api/pool/jobs?limit=1001",
		"/api/pool/jobs?after=-1", "/api/pool/jobs?after=x", "/api/pool/jobs?host=" + strings.Repeat("a", 129), "/api/pool/jobs?host=a%00b",
		"/api/pool/jobs/j_a?host=a%00b", "/api/pool/jobs/j_a/log?host=a%00b",
		"/api/pool/jobs/foo", "/api/pool/jobs/foo/log", "/api/pool/jobs/j_%2e%2e", // not a job id: the caller's error, wwd is not asked
	} {
		rr := get(h, path)
		assert.Equal(t, 400, rr.Code, path)
		assert.Equal(t, ErrBadRequest, errCode(rr), path)
	}
	assert.Zero(t, calls.Load())
}

// A cursor past wwd's head is the App's signal to start over: 409 with wwd's own body.
func TestList_ACursorAheadIs409WithWWDsBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 409, wire.CursorAhead{Reason: wire.CursorAheadReason, Head: "3", Epoch: epoch})
	})
	rr := get(newMux(t, fakeWWD(t, mux)), "/api/pool/jobs?after=900")
	require.Equal(t, 409, rr.Code, rr.Body.String())
	var got wire.CursorAhead
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, wire.CursorAhead{Reason: wire.CursorAheadReason, Head: "3", Epoch: epoch}, got)
}

// A job that ended comes with its stored log; one that has not, or whose log is not stored yet, has none (null).
func TestJob_DetailCarriesTheLogWhenItIsStored(t *testing.T) {
	var hosts []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.URL.Query().Get("host"))
		switch r.PathValue("id") {
		case "j_done", "j_nolog", "j_big":
			writeJSON(w, 200, jobOf(t, r.PathValue("id"), wire.StateSucceeded))
		default:
			writeJSON(w, 200, jobOf(t, r.PathValue("id"), wire.StateRunning))
		}
	})
	var logCalls atomic.Int32
	mux.HandleFunc("GET /v1/jobs/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		logCalls.Add(1)
		switch r.PathValue("id") {
		case "j_done":
			w.Write([]byte("ok\n"))
		case "j_big":
			w.Write([]byte(strings.Repeat("x", maxDetailLog+1)))
		default:
			http.Error(w, `{"error":"not found"}`, 404)
		}
	})
	h := newMux(t, fakeWWD(t, mux))
	read := func(id string) wire.JobDetail {
		rr := get(h, "/api/pool/jobs/"+id+"?host=air19")
		require.Equal(t, 200, rr.Code, rr.Body.String())
		var d wire.JobDetail
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &d))
		return d
	}
	d := read("j_done")
	require.NotNil(t, d.Log)
	assert.Equal(t, "ok\n", *d.Log)
	assert.Equal(t, "j_done", d.Job.ID)
	assert.Nil(t, read("j_nolog").Log, "the log is not stored yet")
	assert.Nil(t, read("j_big").Log, "a log over the cap is read from /log")
	before := logCalls.Load()
	assert.Nil(t, read("j_run").Log)
	assert.Equal(t, before, logCalls.Load(), "a job still running has no log to ask for")
	assert.Equal(t, "air19", hosts[0], "the host reaches wwd")
}

func TestLog_IsTextAnd404UntilItIsStored(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "j_a" {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("line 1\nline 2\n"))
			return
		}
		http.Error(w, "not found", 404)
	})
	h := newMux(t, fakeWWD(t, mux))
	rr := get(h, "/api/pool/jobs/j_a/log")
	require.Equal(t, 200, rr.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rr.Header().Get("Content-Type"))
	assert.Equal(t, "line 1\nline 2\n", rr.Body.String())
	rr = get(h, "/api/pool/jobs/j_b/log")
	assert.Equal(t, 404, rr.Code)
	assert.Equal(t, ErrNotFound, errCode(rr))
}

// What goes wrong is told by status and code: wwd not running, a host the local wwd cannot reach, a job it does not have, and wwd
// answering outside its contract.
func TestErrors_AreMappedToWhatTheAppReads(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("id") {
		case "j_gone":
			http.Error(w, `{"error":"not found"}`, 404)
		case "j_peer":
			writeJSON(w, 502, map[string]string{"host": "air19", "reason": "timeout"})
		case "j_500":
			http.Error(w, "boom", 500)
		default:
			w.Write([]byte(`{"not":"a job"}`))
		}
	})
	h := newMux(t, fakeWWD(t, mux))
	for _, c := range []struct {
		id     string
		status int
		code   string
	}{
		{"j_gone", 404, ErrNotFound}, {"j_peer", 502, ErrHostUnreachable}, {"j_500", 502, ErrWWDError}, {"j_odd", 502, ErrWWDBadResponse},
	} {
		rr := get(h, "/api/pool/jobs/"+c.id)
		assert.Equal(t, c.status, rr.Code, c.id)
		assert.Equal(t, c.code, errCode(rr), c.id)
	}
	down := newMux(t, client.New(filepath.Join(t.TempDir(), "none.sock")))
	for _, path := range []string{"/api/pool/jobs", "/api/pool/jobs/j_a", "/api/pool/jobs/j_a/log"} {
		rr := get(down, path)
		assert.Equal(t, 503, rr.Code, path)
		assert.Equal(t, ErrWWDUnavailable, errCode(rr), path)
	}
}

// However many readers there are, only maxInFlight wwd calls are under way; the next one is turned away with 429.
func TestBusy_TurnsAwayWhatExceedsTheInFlightBound(t *testing.T) {
	m := New()
	for i := 0; i < maxInFlight; i++ {
		m.inflight <- struct{}{}
	}
	m.cl = client.New(filepath.Join(t.TempDir(), "none.sock"))
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	for _, path := range []string{"/api/pool/jobs", "/api/pool/jobs/j_a", "/api/pool/jobs/j_a/log"} {
		rr := get(mux, path)
		assert.Equal(t, 429, rr.Code, path)
		assert.Equal(t, ErrBusy, errCode(rr), path)
	}
}

// The real module registered in a real core, asked through GET /api/info (the lesson of fleet.v1): jobs.v1 is declared while the
// module is up and gone after Stop.
func TestInfo_AnnouncesJobsV1ForTheRealModule(t *testing.T) {
	t.Setenv("PDX_WWD_SOCK", filepath.Join(t.TempDir(), "none.sock"))
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}, Registry: core.NewServiceRegistry()})
	c.AddModule(m)
	require.NoError(t, m.Init(c))
	require.NoError(t, m.Start(context.Background()))
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
	require.Contains(t, caps(), "jobs.v1")
	require.NoError(t, m.Stop(context.Background()))
	require.NotContains(t, caps(), "jobs.v1")
}

// Init never fails when wwd is not there, and its routes are registered next to the pool's without a clash.
func TestInit_NeverFailsAndTheRoutesDoNotClashWithThePool(t *testing.T) {
	t.Setenv("PDX_WWD_SOCK", filepath.Join(t.TempDir(), "none.sock"))
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}, Registry: core.NewServiceRegistry()})
	require.NoError(t, m.Init(c))
	assert.Equal(t, map[string]any{"ready": true}, m.Status())
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	mux.HandleFunc("GET /api/pool/hosts", func(http.ResponseWriter, *http.Request) {}) // the pool module's, registered after
	mux.HandleFunc("GET /api/pool/facts/history", func(http.ResponseWriter, *http.Request) {})
}
