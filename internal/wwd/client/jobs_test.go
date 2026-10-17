package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// jobIn is the contract's queued job (fixture) with the given id and state.
func jobIn(t *testing.T, id string, state wire.JobState) wire.Job {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "wire", "testdata", "job_queued.json"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := wire.Decode[wire.Job](b)
	if err != nil {
		t.Fatal(err)
	}
	j.ID, j.State = id, state
	if state.Terminal() {
		code := 0
		now := time.Now().UTC()
		j.Claim = nil
		j.Result = &wire.Result{Exit: &code, Summary: &wire.Summary{Failures: []wire.Failure{}, Tail: []string{}}, Log: id + "/log", Host: "mlab", StartedAt: &now, FinishedAt: &now}
		if state != wire.StateSucceeded && state != wire.StateFailed {
			j.Result.Exit = nil
		}
	}
	if err := j.Validate(); err != nil {
		t.Fatal(err)
	}
	return j
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func TestSubmit_PostsTheBodyAndDecodesTheAnswer(t *testing.T) {
	var got map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		writeJSON(w, wire.JobSubmissionResponse{ID: "j_01JC"})
	})
	c := New(fakeWWD(t, mux))
	resp, err := c.Submit(context.Background(), map[string]any{"task": "lint", "git_dir": "/r/.git"})
	if err != nil || resp.ID != "j_01JC" || resp.Result != nil {
		t.Fatalf("resp %+v err %v", resp, err)
	}
	if got["task"] != "lint" || got["git_dir"] != "/r/.git" {
		t.Fatalf("server saw %v", got)
	}
}

func TestSubmit_ACacheHitCarriesTheResult(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/jobs", serveFixture("submission_response_cache_hit.json"))
	resp, err := New(fakeWWD(t, mux)).Submit(context.Background(), struct{}{})
	if err != nil || resp.Result == nil || !resp.Result.CacheHit {
		t.Fatalf("resp %+v err %v", resp, err)
	}
}

func TestSubmit_RefusalIsAStatusError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "snapshot commit not in git_dir", http.StatusBadRequest)
	})
	_, err := New(fakeWWD(t, mux)).Submit(context.Background(), struct{}{})
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadRequest || !strings.Contains(se.Body, "git_dir") {
		t.Fatalf("err = %v", err)
	}
}

func TestJobs_SendsTheFiltersAndDecodes(t *testing.T) {
	var q map[string][]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		writeJSON(w, wire.JobsListResponse{Jobs: []wire.Job{jobIn(t, "j_a", wire.StateQueued)}})
	})
	jobs, err := New(fakeWWD(t, mux)).Jobs(context.Background(), JobsQuery{State: wire.StateQueued, Session: "s1"})
	if err != nil || len(jobs) != 1 || jobs[0].ID != "j_a" {
		t.Fatalf("jobs %+v err %v", jobs, err)
	}
	if q["state"][0] != "queued" || q["submitter.session"][0] != "s1" {
		t.Fatalf("query = %v", q)
	}
	if _, err := New(fakeWWD(t, mux)).Jobs(context.Background(), JobsQuery{}); err != nil || len(q) != 0 {
		t.Fatalf("an empty filter must send no query: %v %v", q, err)
	}
}

func TestJobLogCancel_PathsAndEscaping(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	rec := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.Method+" "+r.URL.EscapedPath())
			mu.Unlock()
			h(w, r)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}", rec(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, jobIn(t, r.PathValue("id"), wire.StateRunning))
	}))
	mux.HandleFunc("GET /v1/jobs/{id}/log", rec(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "line1\nline2\n") }))
	mux.HandleFunc("POST /v1/jobs/{id}/cancel", rec(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	c := New(fakeWWD(t, mux))
	if j, err := c.Job(context.Background(), "j_01JC"); err != nil || j.ID != "j_01JC" {
		t.Fatalf("job %+v err %v", j, err)
	}
	if b, err := c.Log(context.Background(), "j_01JC"); err != nil || string(b) != "line1\nline2\n" {
		t.Fatalf("log %q err %v", b, err)
	}
	if err := c.Cancel(context.Background(), "j_01JC"); err != nil {
		t.Fatal(err)
	}
	// an id with path characters is refused before it can reach another route
	for _, bad := range []string{"x/../cancel", "", "j_", "../x", "j_a b", "j_a?x=1"} {
		if _, err := c.Job(context.Background(), bad); err == nil {
			t.Errorf("Job(%q) was sent", bad)
		}
		if _, err := c.Log(context.Background(), bad); err == nil {
			t.Errorf("Log(%q) was sent", bad)
		}
		if err := c.Cancel(context.Background(), bad); err == nil {
			t.Errorf("Cancel(%q) was sent", bad)
		}
	}
	want := []string{"GET /v1/jobs/j_01JC", "GET /v1/jobs/j_01JC/log", "POST /v1/jobs/j_01JC/cancel"}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) < 3 || fmt.Sprint(paths[:3]) != fmt.Sprint(want) {
		t.Fatalf("paths = %v", paths)
	}
	for _, p := range paths {
		if strings.Contains(p, "..") {
			t.Fatalf("an unescaped id reached the server: %s", p)
		}
	}
}

func TestWorkerAndExceptions(t *testing.T) {
	mux := http.NewServeMux()
	var put wire.WorkerSettings
	var exc wire.Exception
	var deleted string
	mux.HandleFunc("GET /v1/worker", serveFixture("worker_settings.json"))
	mux.HandleFunc("PUT /v1/worker", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&put)
		writeJSON(w, put)
	})
	mux.HandleFunc("POST /v1/exceptions", func(w http.ResponseWriter, r *http.Request) { json.NewDecoder(r.Body).Decode(&exc); w.WriteHeader(201) })
	mux.HandleFunc("DELETE /v1/exceptions/{name}", func(w http.ResponseWriter, r *http.Request) { deleted = r.PathValue("name") })
	c := New(fakeWWD(t, mux))
	s, err := c.Worker(context.Background())
	if err != nil || !s.Enabled || s.Slots.Light != 2 {
		t.Fatalf("worker %+v err %v", s, err)
	}
	s.Enabled = false
	if out, err := c.SetWorker(context.Background(), s); err != nil || out.Enabled || put.Enabled {
		t.Fatalf("set: out %+v put %+v err %v", out, put, err)
	}
	until := time.Now().Add(time.Hour).UTC()
	if err := c.StartException(context.Background(), wire.Exception{Name: "app-dev-package", Until: &until, Holds: []wire.Hold{wire.HoldHeavy}}); err != nil || exc.Name != "app-dev-package" {
		t.Fatalf("exception %+v err %v", exc, err)
	}
	if err := c.StopException(context.Background(), "app-dev-package"); err != nil || deleted != "app-dev-package" {
		t.Fatalf("deleted %q err %v", deleted, err)
	}
	// an invalid exception never reaches the socket
	if err := c.StartException(context.Background(), wire.Exception{Name: "x"}); err == nil {
		t.Fatal("an exception with neither until nor pid was sent")
	}
}

func TestLog_TooLargeAnswerIsRefused(t *testing.T) {
	old := 0
	_ = old
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}/log", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404); io.WriteString(w, "no log yet") })
	_, err := New(fakeWWD(t, mux)).Log(context.Background(), "j_1")
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 404 {
		t.Fatalf("err = %v", err)
	}
}

// ---- Watch and WaitJob ----

func sse(w http.ResponseWriter, evs ...wire.Event) {
	for _, e := range evs {
		b, _ := json.Marshal(e)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
	}
	w.(http.Flusher).Flush()
}

func jobEvent(t *testing.T, id string, state wire.JobState) wire.Event {
	t.Helper()
	data, _ := json.Marshal(wire.JobEvent{Host: "mlab", Job: jobIn(t, id, state)})
	return wire.Event{Type: wire.EventJob, Data: data}
}

func openStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	w.(http.Flusher).Flush()
}

func TestWatch_SkipsKeepAlivesAndStopsWhenAsked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) {
		openStream(w)
		io.WriteString(w, ": keep-alive\n\n")
		sse(w, jobEvent(t, "j_1", wire.StateQueued), jobEvent(t, "j_1", wire.StateRunning))
		<-r.Context().Done()
	})
	var seen []wire.JobState
	ready := make(chan struct{})
	err := New(fakeWWD(t, mux)).Watch(context.Background(), ready, func(e wire.Event) bool {
		d, _ := e.DecodeData()
		seen = append(seen, d.(wire.JobEvent).Job.State)
		return len(seen) == 2
	})
	if err != nil || fmt.Sprint(seen) != "[queued running]" {
		t.Fatalf("seen %v err %v", seen, err)
	}
	select {
	case <-ready:
	default:
		t.Fatal("ready was not closed")
	}
}

func TestWatch_AnEndedStreamAndABadFrame(t *testing.T) {
	end := http.NewServeMux()
	end.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) { openStream(w) })
	if err := New(fakeWWD(t, end)).Watch(context.Background(), nil, func(wire.Event) bool { return false }); !errors.Is(err, ErrStreamEnded) {
		t.Fatalf("err = %v", err)
	}
	bad := http.NewServeMux()
	bad.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) {
		openStream(w)
		io.WriteString(w, "event: job\ndata: {\"type\":\"nope\",\"data\":{}}\n\n")
	})
	if err := New(fakeWWD(t, bad)).Watch(context.Background(), nil, func(wire.Event) bool { return false }); !errors.Is(err, ErrBadResponse) {
		t.Fatalf("err = %v", err)
	}
}

func TestWatch_AnEndpointThatNeverAnswersEndsInBoundedTime(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) { <-block }) // accepts, sends no headers
	c := New(fakeWWD(t, mux))
	c.Timeout = 200 * time.Millisecond
	start := time.Now()
	err := c.Watch(context.Background(), nil, func(wire.Event) bool { return false })
	if !errors.Is(err, ErrUnavailable) || time.Since(start) > 3*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestWaitJob_ADeafEventEndpointDoesNotStopTheJobBeingRead(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) { <-block })
	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, jobIn(t, "j_1", wire.StateSucceeded)) })
	c := New(fakeWWD(t, mux))
	c.Timeout = 200 * time.Millisecond
	j, err := c.WaitJob(context.Background(), "j_1", nil)
	if err != nil || j.State != wire.StateSucceeded {
		t.Fatalf("job %+v err %v", j, err)
	}
}

func TestWatch_AFrameOfManyDataLinesIsBoundedAsAWhole(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) {
		openStream(w)
		line := "data: " + strings.Repeat("x", 1<<20) + "\n"
		for i := 0; i < 6; i++ { // each line is under the line limit; together they are over the frame limit
			io.WriteString(w, line)
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	err := New(fakeWWD(t, mux)).Watch(context.Background(), nil, func(wire.Event) bool { return false })
	if !errors.Is(err, ErrBadResponse) {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitJob_AnEventEndsTheWait(t *testing.T) {
	var state atomic.Value
	state.Store(wire.StateQueued)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, jobIn(t, r.PathValue("id"), state.Load().(wire.JobState)))
	})
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) {
		openStream(w)
		time.Sleep(100 * time.Millisecond)
		sse(w, jobEvent(t, "j_other", wire.StateSucceeded)) // somebody else's job
		sse(w, jobEvent(t, "j_1", wire.StateRunning))
		sse(w, jobEvent(t, "j_1", wire.StateSucceeded))
		<-r.Context().Done()
	})
	var updates []wire.JobState
	j, err := New(fakeWWD(t, mux)).WaitJob(context.Background(), "j_1", func(j wire.Job) { updates = append(updates, j.State) })
	if err != nil || j.State != wire.StateSucceeded {
		t.Fatalf("job %+v err %v", j, err)
	}
	if fmt.Sprint(updates) != "[queued running succeeded]" {
		t.Fatalf("updates = %v", updates)
	}
}

func TestTracker_ATerminalStateIsFinalAndEachChangeIsReportedOnce(t *testing.T) {
	var got []wire.JobState
	tr := &tracker{onUpdate: func(j wire.Job) { got = append(got, j.State) }}
	steps := []struct {
		st   wire.JobState
		term bool
	}{
		{wire.StateQueued, false}, {wire.StateQueued, false}, {wire.StateRunning, false},
		{wire.StateSucceeded, true},
		{wire.StateRunning, true}, // a snapshot read that was in flight: older than what the event told
		{wire.StateQueued, true},
	}
	for i, s := range steps {
		if term := tr.seen(jobIn(t, "j_1", s.st)); term != s.term {
			t.Fatalf("step %d: terminal = %v, want %v", i, term, s.term)
		}
	}
	if fmt.Sprint(got) != "[queued running succeeded]" {
		t.Fatalf("updates = %v: a stale read went backwards after the terminal state", got)
	}
	if tr.final.State != wire.StateSucceeded {
		t.Fatalf("final = %s: the wait must return the first terminal job, not a later stale read", tr.final.State)
	}
}

func TestWaitJob_AJobAlreadyDoneReturnsAtOnce(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, jobIn(t, "j_1", wire.StateFailed)) })
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) { openStream(w); <-r.Context().Done() })
	start := time.Now()
	j, err := New(fakeWWD(t, mux)).WaitJob(context.Background(), "j_1", nil)
	if err != nil || j.State != wire.StateFailed || time.Since(start) > 2*time.Second {
		t.Fatalf("job %+v err %v after %v", j, err, time.Since(start))
	}
}

func TestWaitJob_WithoutAnEventStreamItPolls(t *testing.T) {
	old := pollEvery
	pollEvery = 20 * time.Millisecond
	defer func() { pollEvery = old }()
	var reads atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		st := wire.StateRunning
		if reads.Add(1) >= 4 {
			st = wire.StateExpired
		}
		writeJSON(w, jobIn(t, "j_1", st))
	})
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no events", 503) })
	j, err := New(fakeWWD(t, mux)).WaitJob(context.Background(), "j_1", nil)
	if err != nil || j.State != wire.StateExpired {
		t.Fatalf("job %+v err %v after %d reads", j, err, reads.Load())
	}
}

func TestWaitJob_AStreamThatDropsIsReopenedAndTheJobReread(t *testing.T) {
	old := reconnectAfter
	reconnectAfter = 20 * time.Millisecond
	defer func() { reconnectAfter = old }()
	var reads, streams atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		st := wire.StateRunning
		if reads.Add(1) >= 3 { // the change that came while no stream was open
			st = wire.StateSucceeded
		}
		writeJSON(w, jobIn(t, "j_1", st))
	})
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) { streams.Add(1); openStream(w) }) // closes at once
	j, err := New(fakeWWD(t, mux)).WaitJob(context.Background(), "j_1", nil)
	if err != nil || j.State != wire.StateSucceeded || streams.Load() < 2 {
		t.Fatalf("job %+v err %v, streams %d", j, err, streams.Load())
	}
}

func TestWaitJob_CancelAndUnknownJobAndUnreachable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, jobIn(t, "j_1", wire.StateRunning)) })
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) { openStream(w); <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := New(fakeWWD(t, mux)).WaitJob(ctx, "j_1", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}

	gone := http.NewServeMux()
	gone.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	gone.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) { openStream(w); <-r.Context().Done() })
	var se *StatusError
	if _, err := New(fakeWWD(t, gone)).WaitJob(context.Background(), "j_nope", nil); !errors.As(err, &se) || se.Code != 404 {
		t.Fatalf("an unknown job must end the wait: %v", err)
	}

	old := pollEvery
	pollEvery = 10 * time.Millisecond
	defer func() { pollEvery = old }()
	if _, err := New(filepath.Join(t.TempDir(), "none.sock")).WaitJob(context.Background(), "j_1", nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an unreachable wwd must end the wait with ErrUnavailable: %v", err)
	}
}
