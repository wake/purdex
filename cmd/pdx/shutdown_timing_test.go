package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func findLog(h *harness, prefix string) (string, bool) {
	for _, l := range h.logged() {
		if strings.HasPrefix(l, prefix) {
			return l, true
		}
	}
	return "", false
}

func TestServeAndWait_LogsPhaseTimingLine(t *testing.T) {
	h := newHarness()
	h.sig <- syscall.SIGTERM
	if err := h.run(testBudget); err != nil {
		t.Fatalf("err = %v", err)
	}
	line, ok := findLog(h, "shutdown: stop-modules=")
	if !ok {
		t.Fatalf("no phase line in %v", h.logged())
	}
	re := regexp.MustCompile(`^shutdown: stop-modules=\d+ms http-shutdown=\d+ms serve-return=\d+ms close-modules=\d+ms total=\d+ms$`)
	if !re.MatchString(line) {
		t.Fatalf("line %q does not match %v", line, re)
	}
}

func TestServeAndWait_PhaseLineMarksForcedClose(t *testing.T) {
	h := newHarness()
	h.srv.shutdownErr = errors.New("deadline exceeded")
	h.sig <- syscall.SIGTERM
	h.run(testBudget)
	line, ok := findLog(h, "shutdown: stop-modules=")
	if !ok || !strings.HasSuffix(line, " http-forced-close") {
		t.Fatalf("line %q (found=%v) lacks http-forced-close", line, ok)
	}
}

func TestServeAndWait_PhaseLineAfterOtherLogsAndNoForcedMarkerWhenClean(t *testing.T) {
	h := newHarness()
	h.sig <- syscall.SIGTERM
	h.run(testBudget)
	line, _ := findLog(h, "shutdown: stop-modules=")
	if strings.Contains(line, "http-forced-close") {
		t.Fatalf("clean shutdown marked forced: %q", line)
	}
}

type fakeInflight struct {
	total   int
	summary string
}

func (f fakeInflight) Total() int      { return f.total }
func (f fakeInflight) Summary() string { return f.summary }

func TestServeAndWait_LogsInflightBeforeAndAfterHTTPShutdown(t *testing.T) {
	h := newHarness()
	h.sig <- syscall.SIGTERM
	src := fakeInflight{3, "[GET /api/events 1, GET /ws/terminal 2]"}
	err := serveAndWait(h.srv, nil, h.sig, h.restart, h.cancel, h.target, testBudget, h.logf, h.exit, withInflight(src))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := findLog(h, "shutdown: in-flight requests: 3 [GET /api/events 1, GET /ws/terminal 2]"); !ok {
		t.Fatalf("no before line in %v", h.logged())
	}
	if _, ok := findLog(h, "shutdown: in-flight after http shutdown: 3"); !ok {
		t.Fatalf("no after line in %v", h.logged())
	}
}

func TestShutdownDoneLine(t *testing.T) {
	if got := shutdownDoneLine(8120 * time.Millisecond); got != "shutdown: done, restarting after 8120ms" {
		t.Fatalf("got %q", got)
	}
}

func TestRestartRequestedCarriesElapsed(t *testing.T) {
	h := newHarness()
	h.target.stopHook = func(context.Context) { time.Sleep(5 * time.Millisecond) }
	h.restart <- struct{}{}
	var rr *restartRequested
	if err := h.run(testBudget); !errors.As(err, &rr) {
		t.Fatalf("err = %v", err)
	}
	if rr.elapsed < 5*time.Millisecond {
		t.Fatalf("elapsed = %v, want >= 5ms", rr.elapsed)
	}
}

func TestInflightKeyOnlyMethodAndFirstTwoSegments(t *testing.T) {
	cases := map[string]string{
		"GET /api/events":                          "GET /api/events",
		"GET /ws/terminal/abc123?token=SECRET&x=1": "GET /ws/terminal",
		"POST /api/sessions/s1/x/y?ticket=T":       "POST /api/sessions",
		"GET /":                                    "GET /",
		"GET /single":                              "GET /single",
	}
	for in, want := range cases {
		parts := strings.SplitN(in, " ", 2)
		r := httptest.NewRequest(parts[0], parts[1], nil)
		if got := inflightKey(r); got != want {
			t.Errorf("inflightKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInflightTrackerSymmetricAndNoQuery(t *testing.T) {
	tr := newInflightTracker()
	r := httptest.NewRequest("GET", "/ws/terminal/abc?token=SECRET", nil)
	d1 := tr.Enter(r)
	d2 := tr.Enter(r)
	if tr.Total() != 2 {
		t.Fatalf("Total = %d, want 2", tr.Total())
	}
	s := tr.Summary()
	if s != "[GET /ws/terminal 2]" {
		t.Fatalf("Summary = %q", s)
	}
	if strings.Contains(s, "SECRET") || strings.Contains(s, "token") || strings.Contains(s, "abc") {
		t.Fatalf("Summary leaks request detail: %q", s)
	}
	d1()
	d2()
	if tr.Total() != 0 {
		t.Fatalf("Total after Done = %d, want 0", tr.Total())
	}
	if len(tr.m) != 0 {
		t.Fatalf("map not emptied: %v", tr.m)
	}
}

func TestInflightSummaryCapsAtTenKeys(t *testing.T) {
	tr := newInflightTracker()
	for i := 0; i < 15; i++ {
		tr.Enter(httptest.NewRequest("GET", fmt.Sprintf("/api/k%02d", i), nil))
	}
	s := tr.Summary()
	if n := strings.Count(s, "GET /api/"); n != 10 {
		t.Fatalf("Summary lists %d keys, want 10: %q", n, s)
	}
	if tr.Total() != 15 {
		t.Fatalf("Total = %d, want 15", tr.Total())
	}
}

func TestInflightWrapCountsWhileHandlerRunsAndReleasesAfter(t *testing.T) {
	tr := newInflightTracker()
	inHandler := make(chan struct{})
	release := make(chan struct{})
	h := tr.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(inHandler)
		<-release
	}))
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/events", nil))
		close(done)
	}()
	<-inHandler
	if tr.Total() != 1 {
		t.Fatalf("Total during handler = %d, want 1", tr.Total())
	}
	close(release)
	<-done
	if tr.Total() != 0 {
		t.Fatalf("Total after handler = %d, want 0", tr.Total())
	}
}

func TestInflightWrapReleasesOnPanic(t *testing.T) {
	tr := newInflightTracker()
	h := tr.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("x") }))
	func() {
		defer func() { _ = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/x", nil))
	}()
	if tr.Total() != 0 {
		t.Fatalf("Total after panic = %d, want 0", tr.Total())
	}
}

func TestInflightConcurrentRace(t *testing.T) {
	tr := newInflightTracker()
	h := tr.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = tr.Summary() }))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", fmt.Sprintf("/api/p%d/x", i%5), nil))
		}(i)
	}
	wg.Wait()
	if tr.Total() != 0 {
		t.Fatalf("Total = %d, want 0", tr.Total())
	}
}
