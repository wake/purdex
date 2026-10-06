package daemonclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// fakeClock advances only when the client sleeps, so no test waits for real
// time. onSleep (optional) runs after each sleep with its 1-based index.
type fakeClock struct {
	mu      sync.Mutex
	t       time.Time
	sleeps  []time.Duration
	onSleep func(n int)
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

func newTestClient(base string, clock *fakeClock, stderr io.Writer) *Client {
	return New(base, "tok", WithClock(clock.now, clock.sleep), WithStderr(stderr),
		WithHTTPClient(&http.Client{Transport: &http.Transport{}}))
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
	for i, d := range clock.sleeps[2:] {
		if d != time.Second {
			t.Errorf("sleep %d = %v, want 1s", i+2, d)
		}
	}
	if strings.Count(stderr.String(), MsgRestarting) != 1 {
		t.Errorf("stderr = %q", stderr.String())
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

func TestDo_EOFIsRetried(t *testing.T) {
	var calls int
	var mu sync.Mutex
	d := newFakeDaemon("b1", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close() // the client reads EOF
			}
			return
		}
		okJSON(w, http.StatusOK, team.Approval{ID: "r1", State: team.StateTimeout})
	})
	srv := httptest.NewServer(d)
	defer srv.Close()
	clock := newFakeClock()
	// No keep-alive: on a REUSED connection Go's Transport retries an EOF'd
	// GET by itself and this client would never see the failure. The daemon
	// closing a fresh connection is what reaches Do (measured 2026-10-07).
	c := New(srv.URL, "tok", WithClock(clock.now, clock.sleep), WithStderr(io.Discard),
		WithHTTPClient(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}}))

	var ap team.Approval
	status, err := c.Do(context.Background(), http.MethodGet, "/api/team/approvals/r1", nil, &ap)
	if err != nil || status != http.StatusOK || ap.State != team.StateTimeout {
		t.Fatalf("status=%d err=%v ap=%+v", status, err, ap)
	}
	if len(clock.sleeps) != 1 {
		t.Errorf("sleeps = %v, want one", clock.sleeps)
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
