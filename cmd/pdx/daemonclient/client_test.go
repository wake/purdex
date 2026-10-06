package daemonclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// fakeClock advances only when the client sleeps or a test fires a timer,
// so no test waits for real time. onSleep (optional) runs after each sleep
// with its 1-based index. Timers the client arms through afterFunc stay
// pending until fireNext advances the clock to the earliest one.
type fakeClock struct {
	mu      sync.Mutex
	t       time.Time
	sleeps  []time.Duration
	timers  []*fakeTimer
	onSleep func(n int)
}

type fakeTimer struct {
	at      time.Time
	fire    func()
	fired   bool
	stopped bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.sleeps = append(f.sleeps, d)
	n := len(f.sleeps)
	hook := f.onSleep
	f.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return ctx.Err()
}

func (f *fakeClock) afterFunc(d time.Duration, fire func()) func() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{at: f.t.Add(d), fire: fire}
	f.timers = append(f.timers, t)
	return func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		if t.fired || t.stopped {
			return false
		}
		t.stopped = true
		return true
	}
}

// fireNext advances the clock to the earliest pending timer and fires it.
func (f *fakeClock) fireNext() {
	f.mu.Lock()
	var next *fakeTimer
	for _, t := range f.timers {
		if !t.fired && !t.stopped && (next == nil || t.at.Before(next.at)) {
			next = t
		}
	}
	if next == nil {
		f.mu.Unlock()
		return
	}
	next.fired = true
	if next.at.After(f.t) {
		f.t = next.at
	}
	f.mu.Unlock()
	next.fire()
}

func (f *fakeClock) timerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

func (f *fakeClock) elapsed(since time.Time) time.Duration { return f.now().Sub(since) }

// fakeDaemon answers /api/health with bootID and everything else with next.
// It counts hits per path.
type fakeDaemon struct {
	mu     sync.Mutex
	bootID string
	hits   map[string]int
	next   http.HandlerFunc
}

func newFakeDaemon(bootID string, next http.HandlerFunc) *fakeDaemon {
	return &fakeDaemon{bootID: bootID, hits: map[string]int{}, next: next}
}

func (f *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits[r.URL.Path]++
	boot := f.bootID
	f.mu.Unlock()
	if r.URL.Path == "/api/health" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "boot_id": boot})
		return
	}
	f.next(w, r)
}

func (f *fakeDaemon) hitsFor(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

// serveOn starts an httptest.Server on a fixed address so a test can close
// it and start another on the same port (a daemon restart).
func serveOn(t *testing.T, addr string, h http.Handler) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	return srv
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func okJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// hangUp closes the connection after the request was read: the client sees
// EOF (a fresh connection only; see TestDo_EOFOnGetIsRetried).
func hangUp(w http.ResponseWriter) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		conn.Close()
	}
}

func newTestClient(base string, clock *fakeClock, stderr io.Writer, opts ...Option) *Client {
	all := append([]Option{WithClock(clock.now, clock.sleep), WithAfterFunc(clock.afterFunc), WithStderr(stderr),
		WithHTTPClient(&http.Client{Transport: &http.Transport{}})}, opts...)
	return New(base, "tok", all...)
}

// noKeepAlive is the transport for tests that make the server drop the
// connection: on a REUSED connection Go's Transport retries an EOF'd GET by
// itself and this client would never see the failure (measured 2026-10-07).
func noKeepAlive() Option {
	return WithHTTPClient(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}})
}

func TestDo_SendsAuthAndJSONAndDecodes(t *testing.T) {
	var gotAuth, gotCT string
	var gotBody []byte
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		okJSON(w, http.StatusCreated, team.Approval{ID: "r1", State: team.StateOpen})
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	clock := newFakeClock()
	var stderr bytes.Buffer
	c := newTestClient(srv.URL, clock, &stderr)

	var ap team.Approval
	status, err := c.Do(context.Background(), http.MethodPost, "/api/team/approvals",
		team.CreateApprovalRequest{ID: "r1", Kind: team.KindLead}, &ap)
	if err != nil || status != http.StatusCreated {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if gotAuth != "Bearer tok" || gotCT != "application/json" {
		t.Errorf("auth=%q ct=%q", gotAuth, gotCT)
	}
	if !strings.Contains(string(gotBody), `"id":"r1"`) || !strings.Contains(string(gotBody), `"kind":"lead"`) {
		t.Errorf("body = %s", gotBody)
	}
	if ap.ID != "r1" || ap.State != team.StateOpen {
		t.Errorf("decoded = %+v", ap)
	}
	if d.hitsFor("/api/health") != 1 {
		t.Errorf("health probes = %d, want 1 (baseline boot id)", d.hitsFor("/api/health"))
	}
	if len(clock.sleeps) != 0 || stderr.Len() != 0 {
		t.Errorf("no failure: sleeps=%v stderr=%q", clock.sleeps, stderr.String())
	}
}

func TestDo_RefusedThenNewBootIDThenSucceeds(t *testing.T) {
	addr := freeAddr(t)
	first := serveOn(t, addr, newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, http.StatusOK, team.Approval{ID: "r1", State: team.StateOpen})
	}))
	clock := newFakeClock()
	var stderr bytes.Buffer
	c := newTestClient("http://"+addr, clock, &stderr)

	var ap team.Approval
	if _, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1?wait=25", nil, &ap); err != nil {
		t.Fatalf("first call: %v", err)
	}
	first.Close() // the daemon goes down

	var second *httptest.Server
	var once sync.Once
	clock.onSleep = func(n int) {
		if n == 2 {
			once.Do(func() {
				second = serveOn(t, addr, newFakeDaemon("b2", func(w http.ResponseWriter, r *http.Request) {
					okJSON(w, http.StatusOK, team.Approval{ID: "r1", State: team.StateApproved})
				}))
			})
		}
	}
	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1?wait=25", nil, &ap)
	if second != nil {
		defer second.Close()
	}
	if err != nil || status != http.StatusOK || ap.State != team.StateApproved {
		t.Fatalf("status=%d err=%v ap=%+v", status, err, ap)
	}
	want := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond}
	if len(clock.sleeps) != len(want) || clock.sleeps[0] != want[0] || clock.sleeps[1] != want[1] {
		t.Errorf("sleeps = %v, want %v", clock.sleeps, want)
	}
	out := stderr.String()
	if strings.Count(out, MsgRestarting) != 1 {
		t.Errorf("restart line count = %d in %q", strings.Count(out, MsgRestarting), out)
	}
	if strings.Count(out, "daemon 已重新啟動（boot b2）") != 1 {
		t.Errorf("restarted line missing or repeated: %q", out)
	}
}

func TestDo_503ShuttingDownAndNotReadyAreRetried(t *testing.T) {
	var calls int
	var mu sync.Mutex
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		switch n {
		case 1:
			okJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "shutting_down"})
		case 2:
			okJSON(w, http.StatusServiceUnavailable, team.APIError{Error: team.ErrNotReady, Detail: "stopping"})
		default:
			okJSON(w, http.StatusOK, team.Approval{ID: "r1", State: team.StateDenied})
		}
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	clock := newFakeClock()
	var stderr bytes.Buffer
	c := newTestClient(srv.URL, clock, &stderr)

	var ap team.Approval
	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1", nil, &ap)
	if err != nil || status != http.StatusOK || ap.State != team.StateDenied {
		t.Fatalf("status=%d err=%v ap=%+v", status, err, ap)
	}
	if len(clock.sleeps) != 2 {
		t.Errorf("sleeps = %v, want two", clock.sleeps)
	}
	if strings.Count(stderr.String(), MsgRestarting) != 1 {
		t.Errorf("stderr = %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "已重新啟動") {
		t.Errorf("boot id did not change, must not print restarted: %q", stderr.String())
	}
}

func TestDo_PairingModeIsNotRetried(t *testing.T) {
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, http.StatusServiceUnavailable, map[string]string{"reason": "pairing_mode"})
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	clock := newFakeClock()
	var stderr bytes.Buffer
	c := newTestClient(srv.URL, clock, &stderr)

	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1", nil, nil)
	var se *StatusError
	if status != http.StatusServiceUnavailable || !errors.As(err, &se) || se.API.Error != "" {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if !strings.Contains(string(se.Body), "pairing_mode") {
		t.Errorf("body = %s", se.Body)
	}
	if d.hitsFor("/api/team/approvals/r1") != 1 || len(clock.sleeps) != 0 || stderr.Len() != 0 {
		t.Errorf("retried: hits=%d sleeps=%v stderr=%q", d.hitsFor("/api/team/approvals/r1"), clock.sleeps, stderr.String())
	}
}

func TestDo_GraceExpiresIntoErrUnavailable(t *testing.T) {
	addr := freeAddr(t) // nothing listens
	clock := newFakeClock()
	start := clock.now()
	var stderr bytes.Buffer
	c := newTestClient("http://"+addr, clock, &stderr)

	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1", nil, nil)
	if !errors.Is(err, ErrUnavailable) || status != 0 {
		t.Fatalf("status=%d err=%v, want ErrUnavailable", status, err)
	}
	if clock.elapsed(start) < Grace {
		t.Errorf("gave up after %v, want >= %v", clock.elapsed(start), Grace)
	}
	if len(clock.sleeps) < 3 || clock.sleeps[0] != 250*time.Millisecond || clock.sleeps[1] != 500*time.Millisecond {
		t.Fatalf("sleeps = %v", clock.sleeps)
	}
	last := len(clock.sleeps) - 1
	for i, d := range clock.sleeps[2:last] {
		if d != time.Second {
			t.Errorf("sleep %d = %v, want 1s", i+2, d)
		}
	}
	if clock.sleeps[last] > time.Second {
		t.Errorf("last sleep = %v, want <= 1s (the remainder of the grace)", clock.sleeps[last])
	}
	if strings.Count(stderr.String(), MsgRestarting) != 1 {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// F1(b): the backoff schedule lands at 29.75 s; the next sleep is cut to the
// 0.25 s left, not a full second, and Do gives up at exactly Grace.
func TestDo_GraceIsAHardBound(t *testing.T) {
	addr := freeAddr(t) // nothing listens
	clock := newFakeClock()
	start := clock.now()
	c := newTestClient("http://"+addr, clock, io.Discard)

	_, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1", nil, nil)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if got := clock.elapsed(start); got != Grace {
		t.Errorf("elapsed = %v, want exactly %v (the sleep past 29.75 s must be truncated)", got, Grace)
	}
	if last := clock.sleeps[len(clock.sleeps)-1]; last != 250*time.Millisecond {
		t.Errorf("last sleep = %v, want 250ms", last)
	}
}

// F1(a): a daemon that accepts the connection and never answers; the caller
// has no deadline. Do ends at the per-attempt timeout, not never.
func TestDo_SilentDaemonEndsAtAttemptTimeout(t *testing.T) {
	clock := newFakeClock()
	start := clock.now()
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		clock.fireNext() // the attempt timer runs out while the daemon holds the request
		<-r.Context().Done()
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	c := newTestClient(srv.URL, clock, io.Discard)

	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1?wait=35", nil, nil)
	if !errors.Is(err, ErrNoAnswer) || status != 0 {
		t.Fatalf("status=%d err=%v, want ErrNoAnswer", status, err)
	}
	if got := clock.elapsed(start); got != DefaultAttemptTimeout {
		t.Errorf("elapsed = %v, want %v", got, DefaultAttemptTimeout)
	}
	if len(clock.sleeps) != 0 || d.hitsFor("/api/team/approvals/r1") != 1 {
		t.Errorf("a silent daemon is not a restart: sleeps=%v hits=%d", clock.sleeps, d.hitsFor("/api/team/approvals/r1"))
	}
}

func TestDo_WithAttemptTimeoutShortensIt(t *testing.T) {
	clock := newFakeClock()
	start := clock.now()
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		clock.fireNext()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	c := newTestClient(srv.URL, clock, io.Discard, WithAttemptTimeout(5*time.Second))

	_, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1", nil, nil)
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("err = %v, want ErrNoAnswer", err)
	}
	if got := clock.elapsed(start); got != 5*time.Second {
		t.Errorf("elapsed = %v, want 5s", got)
	}
}

// A caller deadline replaces the per-attempt timeout: no timer is armed.
func TestDo_CallerDeadlineDisablesAttemptTimeout(t *testing.T) {
	clock := newFakeClock()
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, http.StatusOK, team.Approval{ID: "r1"})
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	c := newTestClient(srv.URL, clock, io.Discard)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := c.Do(ctx, http.MethodGet, "/api/team/approvals/r1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if clock.timerCount() != 0 {
		t.Errorf("timers armed = %d, want 0 with a caller deadline", clock.timerCount())
	}
}

// F1(b): once restarting, a hanging attempt is bounded by the grace deadline
// (29.75 s left here), not by the 60 s attempt timeout.
func TestDo_RestartingSilentDaemonEndsAtGrace(t *testing.T) {
	clock := newFakeClock()
	start := clock.now()
	var calls int
	var mu sync.Mutex
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			okJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "shutting_down"})
			return
		}
		clock.fireNext()
		<-r.Context().Done()
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	c := newTestClient(srv.URL, clock, io.Discard)

	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1", nil, nil)
	if !errors.Is(err, ErrUnavailable) || status != 0 {
		t.Fatalf("status=%d err=%v, want ErrUnavailable", status, err)
	}
	if got := clock.elapsed(start); got != Grace {
		t.Errorf("elapsed = %v, want %v (0.25 s sleep + 29.75 s bounded attempt)", got, Grace)
	}
	if len(clock.sleeps) != 1 {
		t.Errorf("sleeps = %v, want one", clock.sleeps)
	}
}

func TestDo_ContextCancelWinsOverGrace(t *testing.T) {
	addr := freeAddr(t)
	clock := newFakeClock()
	ctx, cancel := context.WithCancel(context.Background())
	clock.onSleep = func(n int) {
		if n == 1 {
			cancel()
		}
	}
	c := newTestClient("http://"+addr, clock, io.Discard)

	_, err := c.Do(ctx, http.MethodGet, "/api/team/approvals/r1", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(clock.sleeps) != 1 {
		t.Errorf("sleeps = %v, want one", clock.sleeps)
	}
}

func TestDo_PlainNotFoundIsUnsupported(t *testing.T) {
	mux := http.NewServeMux() // an older daemon: health only, no /api/team
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, http.StatusOK, map[string]any{"ok": true, "boot_id": "b1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	clock := newFakeClock()
	c := newTestClient(srv.URL, clock, io.Discard)

	status, err := c.Do(context.Background(), http.MethodPost, "/api/team/approvals", map[string]string{"id": "r1"}, nil)
	if !errors.Is(err, ErrUnsupported) || status != http.StatusNotFound {
		t.Fatalf("status=%d err=%v, want ErrUnsupported", status, err)
	}
	if len(clock.sleeps) != 0 {
		t.Errorf("a 404 must not be retried: %v", clock.sleeps)
	}
}

// F3: a 404 is "unsupported" unless its body is a team.APIError with a code.
func TestDo_NotFoundDecidedByBody(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		unsupported bool
	}{
		{"go plain text", "text/plain; charset=utf-8", "404 page not found\n", true},
		{"nginx html", "text/html", "<html><head><title>404 Not Found</title></head><body><center><h1>404 Not Found</h1></center><hr><center>nginx</center></body></html>", true},
		{"empty body", "", "", true},
		{"json without code", "application/json", `{"message":"nope"}`, true},
		{"json not_found", "application/json", `{"error":"not_found"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
				if tc.contentType != "" {
					w.Header().Set("Content-Type", tc.contentType)
				}
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, tc.body)
			})
			srv := httptest.NewServer(d)
			defer srv.Close()
			clock := newFakeClock()
			c := newTestClient(srv.URL, clock, io.Discard)

			status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/x", nil, nil)
			if status != http.StatusNotFound {
				t.Fatalf("status = %d", status)
			}
			var se *StatusError
			switch {
			case tc.unsupported && !errors.Is(err, ErrUnsupported):
				t.Errorf("err = %v, want ErrUnsupported", err)
			case !tc.unsupported && (!errors.As(err, &se) || se.API.Error != team.ErrNotFound || errors.Is(err, ErrUnsupported)):
				t.Errorf("err = %v, want *StatusError not_found", err)
			}
			if len(clock.sleeps) != 0 {
				t.Errorf("a 404 must not be retried: %v", clock.sleeps)
			}
		})
	}
}

func TestDo_JSONNotFoundPassesThrough(t *testing.T) {
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, http.StatusNotFound, team.APIError{Error: team.ErrNotFound})
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	c := newTestClient(srv.URL, newFakeClock(), io.Discard)

	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/nope", nil, nil)
	var se *StatusError
	if status != http.StatusNotFound || !errors.As(err, &se) || se.API.Error != team.ErrNotFound {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if errors.Is(err, ErrUnsupported) {
		t.Error("a JSON not_found is the daemon's answer, not an unsupported route")
	}
}

func TestDo_RequestOpenCarriesApproval(t *testing.T) {
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		okJSON(w, http.StatusConflict, team.APIError{Error: team.ErrRequestOpen, Approval: &team.Approval{ID: "open-1", State: team.StateOpen}})
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	c := newTestClient(srv.URL, newFakeClock(), io.Discard)

	status, err := c.Do(context.Background(), http.MethodPost, "/api/team/approvals", map[string]string{"id": "r2"}, nil)
	var se *StatusError
	if status != http.StatusConflict || !errors.As(err, &se) || se.API.Approval == nil || se.API.Approval.ID != "open-1" {
		t.Fatalf("status=%d err=%v", status, err)
	}
}

// dropFirst answers the first request by closing the connection and every
// later one with 200.
func dropFirst() (*fakeDaemon, func() int) {
	var calls int
	var mu sync.Mutex
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			hangUp(w)
			return
		}
		okJSON(w, http.StatusOK, team.Approval{ID: "r1", State: team.StateTimeout})
	})
	return d, func() int { mu.Lock(); defer mu.Unlock(); return calls }
}

func TestDo_EOFOnGetIsRetried(t *testing.T) {
	d, _ := dropFirst()
	srv := httptest.NewServer(d)
	defer srv.Close()
	clock := newFakeClock()
	c := newTestClient(srv.URL, clock, io.Discard, noKeepAlive())

	var ap team.Approval
	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1", nil, &ap)
	if err != nil || status != http.StatusOK || ap.State != team.StateTimeout {
		t.Fatalf("status=%d err=%v ap=%+v", status, err, ap)
	}
	if len(clock.sleeps) != 1 {
		t.Errorf("sleeps = %v, want one", clock.sleeps)
	}
}

// F4: a POST the daemon may have received is not replayed on its own.
func TestDo_PostDroppedAfterSendIsNotRetried(t *testing.T) {
	d, calls := dropFirst()
	srv := httptest.NewServer(d)
	defer srv.Close()
	clock := newFakeClock()
	var stderr bytes.Buffer
	c := newTestClient(srv.URL, clock, &stderr, noKeepAlive())

	status, err := c.Do(context.Background(), http.MethodPost, "/api/team/approvals", map[string]string{"id": "r1"}, nil)
	if !errors.Is(err, ErrSentNoResponse) || status != 0 {
		t.Fatalf("status=%d err=%v, want ErrSentNoResponse", status, err)
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want the transport error kept in the chain", err)
	}
	if calls() != 1 || len(clock.sleeps) != 0 || stderr.Len() != 0 {
		t.Errorf("replayed: calls=%d sleeps=%v stderr=%q", calls(), clock.sleeps, stderr.String())
	}
}

func TestDo_PostDroppedAfterSendIsRetriedWhenIdempotent(t *testing.T) {
	d, calls := dropFirst()
	srv := httptest.NewServer(d)
	defer srv.Close()
	clock := newFakeClock()
	c := newTestClient(srv.URL, clock, io.Discard, noKeepAlive())

	var ap team.Approval
	status, err := c.Do(context.Background(), http.MethodPost, "/api/team/approvals", map[string]string{"id": "r1"}, &ap, Idempotent())
	if err != nil || status != http.StatusOK || ap.ID != "r1" {
		t.Fatalf("status=%d err=%v ap=%+v", status, err, ap)
	}
	if calls() != 2 || len(clock.sleeps) != 1 {
		t.Errorf("calls=%d sleeps=%v, want 2 and one", calls(), clock.sleeps)
	}
}

// F4: refused happens before anything is sent, so a POST retries through the
// restart (what `pdx lead request` relies on).
func TestDo_PostRefusedBeforeSendIsRetried(t *testing.T) {
	addr := freeAddr(t)
	clock := newFakeClock()
	var srv *httptest.Server
	var once sync.Once
	clock.onSleep = func(n int) {
		once.Do(func() {
			srv = serveOn(t, addr, newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
				okJSON(w, http.StatusCreated, team.Approval{ID: "r1", State: team.StateOpen})
			}))
		})
	}
	c := newTestClient("http://"+addr, clock, io.Discard)

	status, err := c.Do(context.Background(), http.MethodPost, "/api/team/approvals", map[string]string{"id": "r1"}, nil)
	if srv != nil {
		defer srv.Close()
	}
	if err != nil || status != http.StatusCreated {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if len(clock.sleeps) != 1 {
		t.Errorf("sleeps = %v, want one", clock.sleeps)
	}
}

// F2: only refused / reset / EOF / 503 restart answers are restart signals.
func TestClassify_RestartSignalsOnly(t *testing.T) {
	dial := func(err error) error {
		return &url.Error{Op: "Post", URL: "http://127.0.0.1:7860/api/team/approvals",
			Err: &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: err}}}
	}
	read := func(err error) error {
		return &url.Error{Op: "Get", URL: "http://127.0.0.1:7860/api/team/approvals/r1",
			Err: &net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: err}}}
	}
	status := func(code int, api string) error {
		se := &StatusError{Status: code, Body: []byte(api)}
		_ = json.Unmarshal([]byte(api), &se.API)
		return se
	}
	cases := []struct {
		name string
		err  error
		want failure
	}{
		{"nil", nil, settled},
		{"ECONNREFUSED", dial(syscall.ECONNREFUSED), beforeSend},
		{"ECONNRESET", read(syscall.ECONNRESET), afterSend},
		{"EOF", &url.Error{Op: "Get", Err: io.EOF}, afterSend},
		{"unexpected EOF", fmt.Errorf("read body: %w", io.ErrUnexpectedEOF), afterSend},
		{"503 shutting_down", status(503, `{"error":"shutting_down"}`), beforeSend},
		{"503 not_ready", status(503, `{"error":"not_ready","detail":"stopping"}`), beforeSend},
		{"503 pairing_mode", status(503, `{"reason":"pairing_mode"}`), settled},
		{"404 not_found", status(404, `{"error":"not_found"}`), settled},
		{"500", status(500, `{"error":"internal"}`), settled},
		{"DNS", &url.Error{Op: "Get", Err: &net.OpError{Op: "dial", Net: "tcp",
			Err: &net.DNSError{Err: "no such host", Name: "daemon.local", IsNotFound: true}}}, settled},
		{"EHOSTUNREACH", dial(syscall.EHOSTUNREACH), settled},
		{"ENETUNREACH", dial(syscall.ENETUNREACH), settled},
		{"ETIMEDOUT (another OpError)", dial(syscall.ETIMEDOUT), settled},
		{"EPIPE (another OpError)", read(syscall.EPIPE), settled},
		{"tls verification", &url.Error{Op: "Get",
			Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}}, settled},
		{"x509 unknown authority", &url.Error{Op: "Get", Err: x509.UnknownAuthorityError{}}, settled},
		{"tls record header", &url.Error{Op: "Get", Err: tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}}, settled},
		{"context canceled", &url.Error{Op: "Get", Err: context.Canceled}, settled},
		{"context deadline", &url.Error{Op: "Get", Err: context.DeadlineExceeded}, settled},
		{"encode", errors.New("encode request: unsupported type"), settled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.err); got != tc.want {
				t.Errorf("classify(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestOnce_DoesNotRetry(t *testing.T) {
	addr := freeAddr(t)
	clock := newFakeClock()
	var stderr bytes.Buffer
	c := newTestClient("http://"+addr, clock, &stderr)

	_, err := c.Once(context.Background(), http.MethodDelete, "/api/team/approvals/r1", nil, nil)
	if err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want the raw transport error", err)
	}
	if len(clock.sleeps) != 0 || stderr.Len() != 0 {
		t.Errorf("Once must not wait or print: sleeps=%v stderr=%q", clock.sleeps, stderr.String())
	}
}

func TestStatusError_Message(t *testing.T) {
	withAPI := &StatusError{Status: 400, API: team.APIError{Error: team.ErrBadRequest, Detail: "reason is required"}}
	if got := withAPI.Error(); got != "HTTP 400 bad_request: reason is required" {
		t.Errorf("got %q", got)
	}
	plain := &StatusError{Status: 401, Body: []byte("unauthorized\n")}
	if got := plain.Error(); got != "HTTP 401: unauthorized" {
		t.Errorf("got %q", got)
	}
}
