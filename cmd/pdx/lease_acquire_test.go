package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/resources"
)

const (
	leaseCID  = "11111111-1111-4111-8111-111111111111"
	leaseRow  = "row-1"
	leaseWhen = "Thu Oct  9 00:00:00 2026"
)

// fakeLeaseDaemon speaks the three lease routes. post answers the POST; the
// polls answer from polls in order (the last repeats); a poll in hold is
// never answered.
type fakeLeaseDaemon struct {
	mu       sync.Mutex
	posts    []resources.LeaseRequest
	polls    int
	deletes  []string // path?query of each DELETE
	post     func(resources.LeaseRequest) (int, any)
	pollList []resources.LeaseResponse
	hold     bool
	delCode  int
}

func (f *fakeLeaseDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/health" {
		_, _ = w.Write([]byte(`{"ok":true,"boot_id":"b1"}`))
		return
	}
	f.mu.Lock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/resources/leases":
		var req resources.LeaseRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.posts = append(f.posts, req)
		code, body := http.StatusCreated, any(resources.LeaseResponse{ID: leaseRow, State: resources.StateHeld, Granted: true, Host: resources.LeaseHost{Measured: 31}, Mode: "lease"})
		if f.post != nil {
			code, body = f.post(req)
		}
		f.mu.Unlock()
		write(w, answer{status: code, body: body})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/resources/leases/"):
		f.polls++
		n := f.polls
		hold := f.hold
		var resp resources.LeaseResponse
		if len(f.pollList) > 0 {
			resp = f.pollList[min(n-1, len(f.pollList)-1)]
		}
		f.mu.Unlock()
		if hold {
			<-r.Context().Done()
			return
		}
		write(w, answer{body: resp})
	case r.Method == http.MethodDelete:
		f.deletes = append(f.deletes, r.URL.RequestURI())
		code := f.delCode
		f.mu.Unlock()
		if code != 0 {
			write(w, answer{status: code, body: resources.APIError{Error: resources.ErrNoLease}})
			return
		}
		write(w, answer{body: resources.LeaseResponse{ID: leaseRow, State: resources.StateEnded, EndReason: resources.EndReleased}})
	default:
		f.mu.Unlock()
		http.NotFound(w, r)
	}
}

func (f *fakeLeaseDaemon) snapshot() (posts []resources.LeaseRequest, polls int, deletes []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]resources.LeaseRequest(nil), f.posts...), f.polls, append([]string(nil), f.deletes...)
}

func fixedHolder(t *testing.T) {
	t.Helper()
	op, os_ := leaseParentPID, leaseHolderStart
	leaseParentPID = func() int { return 4242 }
	leaseHolderStart = func(context.Context, int) (string, error) { return leaseWhen, nil }
	t.Cleanup(func() { leaseParentPID, leaseHolderStart = op, os_ })
}

func driveLease(t *testing.T, ctx context.Context, d http.Handler, extra []daemonclient.Option, args ...string) (int, string, string) {
	t.Helper()
	return driveLeaseWith(t, ctx, d, append([]daemonclient.Option{leadClockOpt()}, extra...), args...)
}

// driveLeaseWith runs with exactly the options given plus no keep-alive: a
// test about attempt timeouts needs the real clock, not the fake one.
func driveLeaseWith(t *testing.T, ctx context.Context, d http.Handler, extra []daemonclient.Option, args ...string) (int, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	opts := append([]daemonclient.Option{leadNoKeepAlive()}, extra...)
	code := runLeaseCmd(ctx, append(append([]string{}, args...), "--config", cfg), fakeGetenv(nil), &stdout, &stderr, opts...)
	return code, stdout.String(), stderr.String()
}

func parseAcquireLine(t *testing.T, stdout string) acquireLine {
	t.Helper()
	var l acquireLine
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &l); err != nil || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("stdout %q is not one JSON line: %v", stdout, err)
	}
	return l
}

func TestAcquire_GrantedImmediately(t *testing.T) {
	fixedHolder(t)
	d := &fakeLeaseDaemon{}
	code, stdout, stderr := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "test-full", "--wait", "2m", "--client-id", leaseCID, "--tool-use", "tu1")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	l := parseAcquireLine(t, stdout)
	if !l.Granted || l.ID != leaseRow || l.HostMeasured != 31 || l.FailOpen != "" {
		t.Errorf("line = %+v", l)
	}
	posts, polls, _ := d.snapshot()
	if len(posts) != 1 || polls != 0 {
		t.Fatalf("posts %d polls %d", len(posts), polls)
	}
	p := posts[0]
	if p.ClientID != leaseCID || p.Kind != "test-full" || p.WaitS != 120 || p.Scope != resources.ScopeProcess ||
		p.HolderPID != 4242 || p.HolderStart != leaseWhen || p.ToolUseID != "tu1" || p.SessionID != "" {
		t.Errorf("request = %+v", p)
	}
}

// --session is the mod's call: session-new scope; without --client-id one is
// made.
func TestAcquire_SessionNewAndMintedClientID(t *testing.T) {
	fixedHolder(t)
	d := &fakeLeaseDaemon{}
	code, _, _ := driveLease(t, context.Background(), d, nil, "acquire", "--weight", "20", "--session", "sid-1", "--holder-pid", "99", "--holder-start", leaseWhen)
	posts, _, _ := d.snapshot()
	if code != ExitOK || len(posts) != 1 || posts[0].Scope != resources.ScopeSessionNew || posts[0].SessionID != "sid-1" ||
		posts[0].Weight != 20 || posts[0].HolderPID != 99 || !validLeaseClientID(posts[0].ClientID) {
		t.Fatalf("code=%d posts=%+v", code, posts)
	}
}

func TestAcquire_PollsUntilGranted(t *testing.T) {
	fixedHolder(t)
	d := &fakeLeaseDaemon{
		post: func(resources.LeaseRequest) (int, any) {
			return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting, Position: 1}
		},
		pollList: []resources.LeaseResponse{
			{ID: leaseRow, State: resources.StateWaiting, Position: 1},
			{ID: leaseRow, State: resources.StateHeld, Granted: true, WaitedMS: 4100, Overrun: true, Host: resources.LeaseHost{Measured: 88}},
		},
	}
	code, stdout, stderr := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build")
	l := parseAcquireLine(t, stdout)
	_, polls, _ := d.snapshot()
	if code != ExitOK || stderr != "" || !l.Granted || !l.Overrun || l.WaitedMS != 4100 || l.HostMeasured != 88 || polls != 2 {
		t.Fatalf("code=%d line=%+v polls=%d stderr=%q", code, l, polls, stderr)
	}
}

// Modes off and measure answer state none: nothing to wait for.
func TestAcquire_ModeOffGrantsWithoutARow(t *testing.T) {
	fixedHolder(t)
	d := &fakeLeaseDaemon{post: func(resources.LeaseRequest) (int, any) {
		return 200, resources.LeaseResponse{State: resources.StateNone, Granted: true, Mode: "off"}
	}}
	code, stdout, _ := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build")
	if l := parseAcquireLine(t, stdout); code != ExitOK || !l.Granted || l.ID != "" {
		t.Errorf("code=%d line=%+v", code, l)
	}
}

// The pool is advice: a daemon that is down, or answers anything unexpected,
// lets the caller run (exit 0, granted, the reason named).
func TestAcquire_FailOpenWhenDaemonDown(t *testing.T) {
	fixedHolder(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	srv.Close() // nothing listens any more
	var stdout, stderr bytes.Buffer
	code := runLeaseCmd(context.Background(), []string{"acquire", "--kind", "build", "--config", cfg}, fakeGetenv(nil), &stdout, &stderr, leadClockOpt(), leadNoKeepAlive())
	if l := parseAcquireLine(t, stdout.String()); code != ExitOK || !l.Granted || l.FailOpen == "" {
		t.Fatalf("code=%d line=%+v stderr=%q", code, l, stderr.String())
	}
}

func TestAcquire_FailOpenOnRefusalsAndServerErrors(t *testing.T) {
	fixedHolder(t)
	for name, c := range map[string]struct {
		code   int
		body   any
		reason string
	}{
		"unknown kind": {400, resources.APIError{Error: resources.ErrUnknownKind}, resources.ErrUnknownKind},
		"client reuse": {409, resources.APIError{Error: resources.ErrClientIDReused}, resources.ErrClientIDReused},
		"not ready":    {503, resources.APIError{Error: resources.ErrNotReady}, "daemon_unavailable"}, // the client retries a not_ready through its grace, then gives up
		"plain 500":    {500, map[string]string{}, "http_500"},
	} {
		d := &fakeLeaseDaemon{post: func(resources.LeaseRequest) (int, any) { return c.code, c.body }}
		code, stdout, _ := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build", "--client-id", leaseCID)
		if l := parseAcquireLine(t, stdout); code != ExitOK || !l.Granted || l.FailOpen != c.reason {
			t.Errorf("%s: code=%d line=%+v", name, code, l)
		}
		// A 5xx may have made the row (the answer was lost): it is taken back by
		// client id. A refusal made nothing, and a 409 must not delete the other
		// request's lease.
		_, _, deletes := d.snapshot()
		wantDelete := c.code >= 500 && c.code != 503
		if (len(deletes) == 1) != wantDelete || len(deletes) > 1 {
			t.Errorf("%s: deletes = %v, want a cleanup: %v", name, deletes, wantDelete)
		}
	}
	// A lease that ended before it was granted (cancelled elsewhere): go.
	d := &fakeLeaseDaemon{post: func(resources.LeaseRequest) (int, any) {
		return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting}
	}, pollList: []resources.LeaseResponse{{ID: leaseRow, State: resources.StateEnded, EndReason: resources.EndCancelled}}}
	code, stdout, _ := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build")
	if l := parseAcquireLine(t, stdout); code != ExitOK || !l.Granted || l.FailOpen != "ended_cancelled" {
		t.Errorf("ended: code=%d line=%+v", code, l)
	}
}

func TestAcquire_FailOpenOnHungPolls(t *testing.T) {
	fixedHolder(t)
	d := &fakeLeaseDaemon{
		post: func(resources.LeaseRequest) (int, any) {
			return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting}
		},
		hold: true,
	}
	code, stdout, _ := driveLeaseWith(t, context.Background(), d, []daemonclient.Option{daemonclient.WithAttemptTimeout(30 * time.Millisecond)}, "acquire", "--kind", "build")
	_, polls, deletes := d.snapshot()
	if l := parseAcquireLine(t, stdout); code != ExitOK || !l.Granted || l.FailOpen != "daemon_not_answering" || polls < leaseMaxHungPolls {
		t.Errorf("code=%d line=%+v polls=%d", code, l, polls)
	}
	if len(deletes) != 1 || !strings.Contains(deletes[0], "client_id=") {
		t.Errorf("the row the polls were about was not taken back: %v", deletes)
	}
}

// An interrupted acquire cancels what it may have created (by client id) and
// exits 12.
func TestAcquire_InterruptCancels(t *testing.T) {
	fixedHolder(t)
	ctx, cancel := context.WithCancel(context.Background())
	d := &fakeLeaseDaemon{
		post: func(resources.LeaseRequest) (int, any) {
			return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting}
		},
		hold: true,
	}
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	code, stdout, _ := driveLease(t, ctx, d, []daemonclient.Option{daemonclient.WithAttemptTimeout(10 * time.Second)}, "acquire", "--kind", "build", "--client-id", leaseCID)
	_, _, deletes := d.snapshot()
	if code != ExitCancelled || len(deletes) != 1 || !strings.Contains(deletes[0], "client_id="+leaseCID) {
		t.Fatalf("code=%d deletes=%v stdout=%q", code, deletes, stdout)
	}
}

// With no way to tell the holder's start, the caller runs (fail open).
func TestAcquire_FailOpenWhenHolderStartUnknown(t *testing.T) {
	old := leaseHolderStart
	leaseHolderStart = func(context.Context, int) (string, error) { return "", context.DeadlineExceeded }
	t.Cleanup(func() { leaseHolderStart = old })
	d := &fakeLeaseDaemon{}
	code, stdout, _ := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build")
	posts, _, _ := d.snapshot()
	if l := parseAcquireLine(t, stdout); code != ExitOK || l.FailOpen != "holder_start_unknown" || len(posts) != 0 {
		t.Errorf("code=%d line=%+v posts=%d", code, l, len(posts))
	}
}

// Field errors are exit 2, before any call.
func TestAcquire_FieldErrorsAreExit2BeforeAnyCall(t *testing.T) {
	fixedHolder(t)
	for name, args := range map[string][]string{
		"neither":          {"acquire"},
		"both":             {"acquire", "--kind", "build", "--weight", "5"},
		"weight 0":         {"acquire", "--weight", "0"},
		"weight 201":       {"acquire", "--weight", "201"},
		"wait 600s":        {"acquire", "--kind", "build", "--wait", "10m"},
		"wait negative":    {"acquire", "--kind", "build", "--wait", "-1s"},
		"bad client id":    {"acquire", "--kind", "build", "--client-id", "x"},
		"bad holder pid":   {"acquire", "--kind", "build", "--holder-pid", "-3"},
		"bad holder start": {"acquire", "--kind", "build", "--holder-start", "soon"},
		"extra argument":   {"acquire", "--kind", "build", "stray"},
		"unknown verb":     {"hold"},
	} {
		d := &fakeLeaseDaemon{}
		code, stdout, stderr := driveLease(t, context.Background(), d, nil, args...)
		posts, polls, deletes := d.snapshot()
		if code != ExitUsage || stdout != "" || !strings.HasPrefix(stderr, "pdx lease: ") || len(posts)+polls+len(deletes) != 0 {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", name, code, stdout, stderr)
		}
	}
}

func TestRelease_ByIDAndByClientID(t *testing.T) {
	d := &fakeLeaseDaemon{}
	if code, _, stderr := driveLease(t, context.Background(), d, nil, "release", leaseRow); code != ExitOK || stderr != "" {
		t.Fatalf("by id: code=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := driveLease(t, context.Background(), d, nil, "release", "--client-id", leaseCID); code != ExitOK || stderr != "" {
		t.Fatalf("by client id: code=%d stderr=%q", code, stderr)
	}
	_, _, deletes := d.snapshot()
	if len(deletes) != 2 || deletes[0] != "/api/resources/leases/"+leaseRow || !strings.HasPrefix(deletes[1], "/api/resources/leases?client_id="+leaseCID) {
		t.Errorf("deletes = %v", deletes)
	}
}

// Releasing is best effort: the sweeper is the backstop. A daemon that is
// down, or errors, is a line on stderr and exit 0; "no such lease" is silent.
func TestRelease_BestEffort(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	srv.Close()
	var stdout, stderr bytes.Buffer
	code := runLeaseCmd(context.Background(), []string{"release", leaseRow, "--config", cfg}, fakeGetenv(nil), &stdout, &stderr, leadClockOpt(), leadNoKeepAlive())
	if code != ExitOK || !strings.Contains(stderr.String(), "釋放沒成功") {
		t.Errorf("down: code=%d stderr=%q", code, stderr.String())
	}
	d := &fakeLeaseDaemon{delCode: 404}
	code, _, stderr2 := driveLease(t, context.Background(), d, nil, "release", leaseRow)
	if code != ExitOK || stderr2 != "" {
		t.Errorf("no such lease: code=%d stderr=%q", code, stderr2)
	}
	for _, args := range [][]string{{"release"}, {"release", "a", "b"}, {"release", "a", "--client-id", leaseCID}, {"release", "--client-id", "x"}} {
		if code, _, _ := driveLease(t, context.Background(), &fakeLeaseDaemon{}, nil, args...); code != ExitUsage {
			t.Errorf("%v: code %d, want usage", args, code)
		}
	}
}

// wait_s is whole seconds and 0 means the host's default: a shorter --wait is
// rounded up, never turned into the default.
func TestAcquire_WaitIsRoundedUpToWholeSeconds(t *testing.T) {
	fixedHolder(t)
	for wait, want := range map[string]int{"1ms": 1, "500ms": 1, "999ms": 1, "1s": 1, "1500ms": 2, "2m": 120, "0s": 0} {
		d := &fakeLeaseDaemon{}
		driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build", "--wait", wait)
		posts, _, _ := d.snapshot()
		if len(posts) != 1 || posts[0].WaitS != want {
			t.Errorf("--wait %s: wait_s = %+v, want %d", wait, posts, want)
		}
	}
}
