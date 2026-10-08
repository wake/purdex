package main

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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/team"
)

// leadClock is a fake clock for daemonclient.WithClock / WithAfterFunc: time
// advances only when the client sleeps or a test fires a timer, so neither
// the 30 s grace nor the 35 s attempt timeout costs real time. onSleep
// (optional) runs after each sleep with its 1-based index; the restart test
// (P2b-3) brings the new daemon up from it.
type leadClock struct {
	mu      sync.Mutex
	t       time.Time
	n       int
	timers  []*leadTimer
	onSleep func(n int)
}

type leadTimer struct {
	at      time.Time
	fire    func()
	fired   bool
	stopped bool
}

func newLeadClock() *leadClock {
	return &leadClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
}

func (c *leadClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *leadClock) sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.n++
	n := c.n
	hook := c.onSleep
	c.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return ctx.Err()
}

func (c *leadClock) afterFunc(d time.Duration, fire func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &leadTimer{at: c.t.Add(d), fire: fire}
	c.timers = append(c.timers, t)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if t.fired || t.stopped {
			return false
		}
		t.stopped = true
		return true
	}
}

// fireNext advances the clock to the earliest pending timer and fires it:
// the attempt the client is running ends as the daemon "never answered".
func (c *leadClock) fireNext() {
	c.mu.Lock()
	var next *leadTimer
	for _, t := range c.timers {
		if !t.fired && !t.stopped && (next == nil || t.at.Before(next.at)) {
			next = t
		}
	}
	if next == nil {
		c.mu.Unlock()
		return
	}
	next.fired = true
	if next.at.After(c.t) {
		c.t = next.at
	}
	c.mu.Unlock()
	next.fire()
}

// fireDue fires every pending timer whose time has come (at <= now), in
// time order, without advancing the clock: what a real time.AfterFunc does
// by itself once sleeps have carried the clock past it.
func (c *leadClock) fireDue() {
	for {
		c.mu.Lock()
		var next *leadTimer
		for _, t := range c.timers {
			if !t.fired && !t.stopped && !t.at.After(c.t) && (next == nil || t.at.Before(next.at)) {
				next = t
			}
		}
		if next == nil {
			c.mu.Unlock()
			return
		}
		next.fired = true
		c.mu.Unlock()
		next.fire()
	}
}

func (c *leadClock) sleeps() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// opt is one daemonclient.Option that installs both the clock and the timer.
func (c *leadClock) opt() daemonclient.Option {
	return func(cl *daemonclient.Client) {
		daemonclient.WithClock(c.now, c.sleep)(cl)
		daemonclient.WithAfterFunc(c.afterFunc)(cl)
	}
}

func leadClockOpt() daemonclient.Option { return newLeadClock().opt() }

// leadNoKeepAlive makes every request a fresh connection: on a reused one
// Go's Transport retries an EOF'd request by itself and the client under
// test would never see the drop.
func leadNoKeepAlive() daemonclient.Option {
	return daemonclient.WithHTTPClient(&http.Client{Transport: &http.Transport{DisableKeepAlives: true}})
}

// fakeTeamDaemon speaks the P2 routes of spec §6.2 for one request id:
// POST creates (createStatus 201, or a 409 request_open carrying openID;
// when dropFirstCreate is set the first POST is read and then the
// connection is closed without an answer), GET polls 1..openUntil answer
// open and later ones answer final, DELETE records the id and answers
// cancelled. A GET is held (blocks until the client goes away) when hold is
// set or holdPoll(n) says so; the first held GET signals pollStarted, and
// onPoll(n) runs on every GET before any of that. Health answers bootID.
// When onFirstPoll is set, the first GET runs it, then the connection is
// hijacked and closed WITHOUT any response — the poll was in flight when
// the daemon process died (P2b-3's restart test); the client sees EOF /
// ECONNRESET, never a settled answer.
type fakeTeamDaemon struct {
	mu              sync.Mutex
	creates         []team.CreateApprovalRequest
	auths           []string
	polls           []string
	deletes         []string
	createStatus    int
	dropFirstCreate bool
	openID          string
	refuseCode      string // the 409 body's error code; empty means request_open
	final           team.Approval
	openUntil       int
	hold            bool
	holdPoll        func(n int) bool
	onPoll          func(n int)
	pollStarted     chan struct{}
	startOnce       sync.Once
	bootID          string
	onFirstPoll     func()
	noOrigin        bool // answer create without origin.session_id (an older daemon)
}

func newFakeTeamDaemon(final team.Approval) *fakeTeamDaemon {
	return &fakeTeamDaemon{createStatus: http.StatusCreated, final: final, openUntil: 1,
		pollStarted: make(chan struct{}), bootID: "b1"}
}

func (f *fakeTeamDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/health" {
		f.mu.Lock()
		boot := f.bootID
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "boot_id": boot})
		return
	}
	f.mu.Lock()
	f.auths = append(f.auths, r.Header.Get("Authorization"))
	f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/team/approvals":
		var req team.CreateApprovalRequest
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.creates = append(f.creates, req)
		n := len(f.creates)
		status, openID, drop, refuse, noOrigin := f.createStatus, f.openID, f.dropFirstCreate, f.refuseCode, f.noOrigin
		f.mu.Unlock()
		if drop && n == 1 {
			// The body was read: the daemon may have applied it. Then the
			// connection dies without an answer.
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		if status == http.StatusConflict {
			w.WriteHeader(status)
			if refuse != "" && refuse != team.ErrRequestOpen {
				json.NewEncoder(w).Encode(team.APIError{Error: refuse})
				return
			}
			json.NewEncoder(w).Encode(team.APIError{Error: team.ErrRequestOpen, Approval: &team.Approval{ID: openID, State: team.StateOpen}})
			return
		}
		if status >= 400 {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(team.APIError{Error: team.ErrBadRequest, Detail: "reason is required"})
			return
		}
		w.WriteHeader(status)
		ap := team.Approval{ID: req.ID, Kind: req.Kind, State: team.StateOpen, Origin: team.Origin{SessionID: fakeLeadSessionID}}
		if noOrigin {
			ap.Origin = team.Origin{}
		}
		json.NewEncoder(w).Encode(ap)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/team/approvals/"):
		f.mu.Lock()
		f.polls = append(f.polls, r.URL.RequestURI())
		n := len(f.polls)
		hold := f.hold || (f.holdPoll != nil && f.holdPoll(n))
		onPoll, onFirstPoll, openUntil := f.onPoll, f.onFirstPoll, f.openUntil
		f.mu.Unlock()
		if onPoll != nil {
			onPoll(n)
		}
		if hold {
			f.startOnce.Do(func() { close(f.pollStarted) })
			<-r.Context().Done()
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/team/approvals/")
		if n == 1 && onFirstPoll != nil {
			onFirstPoll()
			// The daemon died mid-poll: the request was read, nothing is
			// written back, the socket just closes.
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		if n <= openUntil {
			json.NewEncoder(w).Encode(team.Approval{ID: id, State: team.StateOpen})
			return
		}
		ap := f.final
		ap.ID = id
		json.NewEncoder(w).Encode(ap)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/team/approvals/"):
		id := strings.TrimPrefix(r.URL.Path, "/api/team/approvals/")
		f.mu.Lock()
		f.deletes = append(f.deletes, id)
		f.mu.Unlock()
		json.NewEncoder(w).Encode(team.Approval{ID: id, State: team.StateCancelled})
	default:
		http.NotFound(w, r)
	}
}

// fakeLeadSessionID is the origin the fake daemon attributes every create
// to: the CC session id the hard-lock flag is named after (P2c).
const fakeLeadSessionID = "cc-sid-lead-1"

func (f *fakeTeamDaemon) snapshot() (creates []team.CreateApprovalRequest, polls, deletes, auths []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]team.CreateApprovalRequest{}, f.creates...), append([]string{}, f.polls...),
		append([]string{}, f.deletes...), append([]string{}, f.auths...)
}

func leadEnv() func(string) string {
	return fakeGetenv(map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/cc-socks/1.sock"})
}

func fixedID() func() string { return func() string { return "11111111-2222-4333-8444-555555555555" } }

// driveLead drives runLeadCmd against d with a fake clock and returns the
// exit code and both streams. (Not runLead: that name is the production
// switch target in lead.go, same package.)
func driveLead(t *testing.T, ctx context.Context, d http.Handler, args ...string) (int, string, string) {
	t.Helper()
	return driveLeadWith(t, ctx, d, []daemonclient.Option{leadClockOpt()}, args...)
}

// driveLeadWith is driveLead with the client options chosen by the test.
func driveLeadWith(t *testing.T, ctx context.Context, d http.Handler, opts []daemonclient.Option, args ...string) (int, string, string) {
	t.Helper()
	return driveLeadHook(t, ctx, d, opts, nil, args...)
}

// driveLeadHook is driveLeadWith with the onCancelled hook (production: the
// signal.NotifyContext stop func) chosen by the test.
func driveLeadHook(t *testing.T, ctx context.Context, d http.Handler, opts []daemonclient.Option, onCancelled func(), args ...string) (int, string, string) {
	t.Helper()
	code, stdout, stderr, _ := driveLeadDir(t, ctx, d, opts, onCancelled, t.TempDir(), args...)
	return code, stdout, stderr
}

// driveLeadDir is driveLeadHook with the config's data_dir chosen by the
// test (the hard-lock flag lives under it; "" means the config has none)
// and returned, so the test can look for the flag.
func driveLeadDir(t *testing.T, ctx context.Context, d http.Handler, opts []daemonclient.Option, onCancelled func(), dataDir string, args ...string) (int, string, string, string) {
	t.Helper()
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfgPath := writeTestConfigDataDir(t, srv.URL, "admin-tok", dataDir)
	var stdout, stderr bytes.Buffer
	full := append(append([]string{"request"}, args...), "--config", cfgPath)
	code := runLeadCmd(ctx, full, leadEnv(), &stdout, &stderr, fixedID(), onCancelled, opts...)
	return code, stdout.String(), stderr.String(), dataDir
}

// leadReservePort listens on a free 127.0.0.1 port and KEEPS the listener:
// the first daemon serves on it (leadServeOn), so no other process can take
// the port between reserving it and serving on it.
func leadReservePort(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// leadServeOn starts an httptest.Server on an already-bound listener. It
// repeats daemonclient's serveOn because that package's test helpers are
// not importable from package main.
func leadServeOn(t *testing.T, ln net.Listener, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	return srv
}

// leadRelisten binds addr again after the first daemon's listener closed —
// a restarted daemon comes back on the same port, and that same-port
// handoff is inherent to the scenario (the CLI's base URL is fixed). Task
// 2b.1 measured that a closed port can be re-listened at once; the retry on
// "address already in use" (up to 50 × 10 ms) bounds the residual race with
// another process grabbing the port in between.
func leadRelisten(t *testing.T, addr string) net.Listener {
	t.Helper()
	var err error
	for i := 0; i < 50; i++ {
		var ln net.Listener
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			return ln
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("re-listen %s: %v", addr, err)
	return nil
}

func TestRunLeadCmd_UsageErrorsExit2BeforeConfig(t *testing.T) {
	var reqCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reqCount, 1)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")

	cases := []struct {
		name string
		args []string
	}{
		{"no verb", []string{}},
		{"unknown verb", []string{"approve"}},
		{"missing reason", []string{"request"}},
		{"empty reason", []string{"request", "--reason", "  "}},
		{"max-members too large", []string{"request", "--reason", "x", "--max-members", "9"}},
		{"max-members negative", []string{"request", "--reason", "x", "--max-members", "-1"}},
		{"wait zero", []string{"request", "--reason", "x", "--wait", "0"}},
		{"wait over cap", []string{"request", "--reason", "x", "--wait", "11m"}},
		{"unknown flag", []string{"request", "--reason", "x", "--bogus"}},
		{"leftover positional", []string{"request", "--reason", "x", "extra"}},
		{"name over 64 bytes", []string{"request", "--reason", "x", "--name", strings.Repeat("a", 65)}},
		{"name with a control character", []string{"request", "--reason", "x", "--name", "a\x07b"}},
		{"name with an ideographic space", []string{"request", "--reason", "x", "--name", "a　b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := atomic.LoadInt64(&reqCount)
			args := append(append([]string{}, tc.args...), "--config", cfgPath)
			var stdout, stderr bytes.Buffer
			code := runLeadCmd(context.Background(), args, leadEnv(), &stdout, &stderr, fixedID(), nil, leadClockOpt())
			if code != ExitUsage {
				t.Errorf("exit code = %d, want %d; stderr=%q", code, ExitUsage, stderr.String())
			}
			if stderr.String() == "" {
				t.Errorf("stderr is empty, want a usage/error message")
			}
			if stdout.String() != "" {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if after := atomic.LoadInt64(&reqCount); after != before {
				t.Errorf("server saw %d request(s), want 0", after-before)
			}
		})
	}
}

func TestRunLeadCmd_NoInboxExit1(t *testing.T) {
	var reqCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reqCount, 1)
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runLeadCmd(context.Background(), []string{"request", "--reason", "x", "--config", cfgPath},
		fakeGetenv(nil), &stdout, &stderr, fixedID(), nil, leadClockOpt())
	if code != ExitError || !strings.Contains(stderr.String(), "CLAUDE_CODE_MESSAGING_SOCKET") ||
		!strings.HasPrefix(stderr.String(), "pdx lead:") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if atomic.LoadInt64(&reqCount) != 0 {
		t.Errorf("server saw %d request(s), want 0", reqCount)
	}
}

func TestRunLeadCmd_ApprovedExit0PrintsGrant(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{
		State: team.StateApproved,
		Grant: &team.Grant{MaxMembers: 2, Roots: []string{"/Users/wake/Workspace/wake/purdex"}},
	})
	code, stdout, stderr := driveLead(t, context.Background(), d,
		"--reason", "split the P3 dialog work", "--max-members", "2", "--root", "/Users/wake/Workspace/wake/purdex")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var out struct {
		RequestID string     `json:"request_id"`
		TeamID    string     `json:"team_id"`
		Grant     team.Grant `json:"grant"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	if out.RequestID != fixedID()() || out.TeamID != fixedID()() || out.Grant.MaxMembers != 2 || len(out.Grant.Roots) != 1 {
		t.Errorf("stdout = %q", stdout)
	}
	want := "申請 lead 中（" + fixedID()() + "），請在 Purdex 介面核准；這個呼叫必須在前景等待（Bash timeout 600000）"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}

	creates, polls, deletes, auths := d.snapshot()
	if len(creates) != 1 {
		t.Fatalf("creates = %+v", creates)
	}
	c := creates[0]
	if c.ID != fixedID()() || c.Kind != team.KindLead || c.OriginInbox != "/tmp/cc-socks/1.sock" ||
		c.Reason != "split the P3 dialog work" || c.MaxMembers != 2 || c.WaitS != team.DefaultWaitS ||
		len(c.Roots) != 1 || c.Roots[0] != "/Users/wake/Workspace/wake/purdex" {
		t.Errorf("create body = %+v", c)
	}
	if len(polls) != 2 || !strings.HasSuffix(polls[0], "/api/team/approvals/"+fixedID()()+"?wait=25") {
		t.Errorf("polls = %v", polls)
	}
	if len(deletes) != 0 {
		t.Errorf("deletes = %v, want none", deletes)
	}
	if len(auths) != 3 {
		t.Errorf("auths = %v, want one per create and poll", auths)
	}
	for _, a := range auths {
		if a != "Bearer admin-tok" {
			t.Errorf("auth = %q", a)
		}
	}
}

// --max-members 0 (or absent) means the daemon default: the body carries no
// max_members (omitempty), and no --root sends no roots.
func TestRunLeadCmd_DefaultsLeaveMaxMembersAndRootsToDaemon(t *testing.T) {
	for _, args := range [][]string{
		{"--reason", "r"},
		{"--reason", "r", "--max-members", "0"},
	} {
		d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Grant: &team.Grant{MaxMembers: 3}})
		if code, _, stderr := driveLead(t, context.Background(), d, args...); code != ExitOK {
			t.Fatalf("%v: code=%d stderr=%q", args, code, stderr)
		}
		creates, _, _, _ := d.snapshot()
		if len(creates) != 1 || creates[0].MaxMembers != 0 || creates[0].Roots != nil {
			t.Errorf("%v: create body = %+v, want max_members 0 and no roots", args, creates)
		}
	}
}

// D-N2: --name is trimmed and sent as team_name; no --name (or a blank one)
// leaves the key out of the body altogether (omitempty, D-N1).
func TestRunLeadCmd_NameReachesTheRequestBody(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		want    string
		wantKey bool
	}{
		{[]string{"--reason", "r", "--name", "  驗收 team  "}, "驗收 team", true},
		{[]string{"--reason", "r", "--name", strings.Repeat("a", 64)}, strings.Repeat("a", 64), true},
		{[]string{"--reason", "r", "--name", "   "}, "", false},
		{[]string{"--reason", "r"}, "", false},
	} {
		var body []byte
		d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Grant: &team.Grant{MaxMembers: 3}})
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/api/team/approvals" {
				body, _ = io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
			d.ServeHTTP(w, r)
		})
		if code, _, stderr := driveLead(t, context.Background(), h, tc.args...); code != ExitOK {
			t.Fatalf("%v: code=%d stderr=%q", tc.args, code, stderr)
		}
		creates, _, _, _ := d.snapshot()
		if len(creates) != 1 || creates[0].TeamName != tc.want {
			t.Errorf("%v: team_name = %+v, want %q", tc.args, creates, tc.want)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("%v: body %q: %v", tc.args, body, err)
		}
		if _, has := raw["team_name"]; has != tc.wantKey {
			t.Errorf("%v: body %s has team_name = %v, want %v", tc.args, body, has, tc.wantKey)
		}
	}
}

// An invalid --name is refused as a usage error (exit 2) before any HTTP
// call, with the same usage line as the other bad flags.
func TestRunLeadCmd_InvalidNameExits2BeforeAnyHTTPCall(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved})
	code, stdout, stderr := driveLead(t, context.Background(), d, "--reason", "r", "--name", strings.Repeat("a", 65))
	if code != ExitUsage || stdout != "" || !strings.Contains(stderr, "--name") || !strings.Contains(stderr, "usage: pdx lead request") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	creates, polls, deletes, auths := d.snapshot()
	if len(creates)+len(polls)+len(deletes)+len(auths) != 0 {
		t.Errorf("daemon saw creates=%v polls=%v deletes=%v auths=%v, want nothing", creates, polls, deletes, auths)
	}
}

func TestLeadUsage_MentionsName(t *testing.T) {
	if !strings.Contains(leadUsage, "[--name <team name>]") {
		t.Errorf("leadUsage = %q, want [--name <team name>]", leadUsage)
	}
}

func TestRunLeadCmd_WaitFlagSetsWaitS(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Grant: &team.Grant{MaxMembers: 3}})
	if code, _, stderr := driveLead(t, context.Background(), d, "--reason", "r", "--wait", "2m30s"); code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if creates, _, _, _ := d.snapshot(); len(creates) != 1 || creates[0].WaitS != 150 {
		t.Errorf("create body = %+v, want wait_s 150", creates)
	}
}

// A positive --wait below one second would truncate to wait_s 0, which the
// body omits (omitempty) and the daemon reads as its default. That is a
// usage error (exit 2) before any config load or request; --wait 1s is the
// smallest value and goes out as wait_s 1.
func TestRunLeadCmd_WaitBelowOneSecondIsUsageError(t *testing.T) {
	var reqCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reqCount, 1)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	for _, wait := range []string{"500ms", "999ms"} {
		t.Run(wait, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runLeadCmd(context.Background(), []string{"request", "--reason", "r", "--wait", wait, "--config", cfgPath},
				leadEnv(), &stdout, &stderr, fixedID(), nil, leadClockOpt())
			if code != ExitUsage || stdout.String() != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), "pdx lead: --wait must be at least 1s") {
				t.Errorf("stderr = %q, want the at-least-1s line", stderr.String())
			}
			if n := atomic.LoadInt64(&reqCount); n != 0 {
				t.Errorf("server saw %d request(s), want 0", n)
			}
		})
	}

	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Grant: &team.Grant{MaxMembers: 3}})
	if code, _, stderr := driveLead(t, context.Background(), d, "--reason", "r", "--wait", "1s"); code != ExitOK {
		t.Fatalf("--wait 1s: code=%d stderr=%q", code, stderr)
	}
	if creates, _, _, _ := d.snapshot(); len(creates) != 1 || creates[0].WaitS != 1 {
		t.Errorf("--wait 1s: create body = %+v, want wait_s 1", creates)
	}
}

// Spec §6.1 step 4: an approved request prints the grant with the team id on
// stdout, one JSON line and nothing else. The team id is the approving
// request's id (plan v3 deviation 1), so no second call is needed. stderr
// carries only the U20 activation reminder (P4-7).
func TestLeadFinish_ApprovedPrintsTeamID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ap := team.Approval{ID: "8f2c0f8e-3b1a-4c6e-9d2a-0e5b7c1d9a44", Kind: team.KindLead, State: team.StateApproved,
		Grant: &team.Grant{MaxMembers: 3, Roots: []string{"/w"}}}
	if code := leadFinish(ap, &stdout, &stderr); code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
		t.Fatalf("stdout = %q, want exactly one line", got)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout %q: %v", stdout.String(), err)
	}
	if string(out["team_id"]) != `"`+ap.ID+`"` || string(out["request_id"]) != `"`+ap.ID+`"` || len(out) != 3 {
		t.Fatalf("stdout = %s, want request_id, team_id (= the request id) and grant", stdout.String())
	}
	if stderr.String() != leadActivationReminder+"\n" {
		t.Fatalf("stderr = %q, want the activation reminder alone", stderr.String())
	}
}

// leadActivationReminder is U20 (b)'s line, spelled out here rather than read
// from team.ReminderAtActivation, so dropping or rewording it turns this
// package's tests red too.
const leadActivationReminder = "已成為 lead。預設模型不固定：spawn member 時請依工作需求用 --model 指定（例：--model sonnet 做機械性修改、--model opus 做設計）。"

// U20 (b), spec §15: an approved `pdx lead request` prints the activation
// reminder once on stderr, and stdout stays the grant JSON alone. A request
// that is not approved never prints it.
func TestLeadRequest_ApprovedPrintsTheReminderOnStderrOnlyTheGrantOnStdout(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Grant: &team.Grant{MaxMembers: 2, Roots: []string{"/w"}}})
	code, stdout, stderr := driveLead(t, context.Background(), d, "--reason", "r")
	if code != ExitOK {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var out map[string]json.RawMessage
	if strings.Count(stdout, "\n") != 1 || json.Unmarshal([]byte(stdout), &out) != nil || len(out) != 3 {
		t.Fatalf("stdout = %q, want the grant JSON line alone", stdout)
	}
	if strings.Contains(stdout, "已成為 lead") {
		t.Errorf("stdout carries the reminder: %q", stdout)
	}
	if n := strings.Count(stderr, leadActivationReminder+"\n"); n != 1 {
		t.Errorf("stderr has the reminder line %d times, want 1: %q", n, stderr)
	}

	for _, st := range []team.State{team.StateDenied, team.StateTimeout, team.StateCancelled, team.StateAbandoned} {
		d := newFakeTeamDaemon(team.Approval{State: st})
		_, _, stderr := driveLead(t, context.Background(), d, "--reason", "r")
		if strings.Contains(stderr, "已成為 lead") {
			t.Errorf("%s: stderr carries the reminder: %q", st, stderr)
		}
	}
}

func TestRunLeadCmd_ApprovedWithoutGrantFallsBackToPayload(t *testing.T) {
	payload, _ := json.Marshal(team.LeadPayload{Reason: "r", MaxMembers: 3, Roots: []string{"/w"}})
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Payload: payload})
	code, stdout, _ := driveLead(t, context.Background(), d, "--reason", "r")
	if code != ExitOK || !strings.Contains(stdout, `"max_members":3`) || !strings.Contains(stdout, `"/w"`) {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

// The older-daemon fallback (no grant on the closed approval) carries the
// requested name too, so the printed grant is the same shape.
func TestRunLeadCmd_ApprovedWithoutGrantFallsBackToPayloadWithName(t *testing.T) {
	payload, _ := json.Marshal(team.LeadPayload{Reason: "r", MaxMembers: 3, TeamName: "驗收 team"})
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Payload: payload})
	code, stdout, _ := driveLead(t, context.Background(), d, "--reason", "r", "--name", "驗收 team")
	var out struct {
		Grant team.Grant `json:"grant"`
	}
	if code != ExitOK || json.Unmarshal([]byte(stdout), &out) != nil || out.Grant.TeamName == nil || *out.Grant.TeamName != "驗收 team" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	// No name in the payload: the grant carries no team_name key.
	payload, _ = json.Marshal(team.LeadPayload{Reason: "r", MaxMembers: 3})
	d = newFakeTeamDaemon(team.Approval{State: team.StateApproved, Payload: payload})
	if _, stdout, _ := driveLead(t, context.Background(), d, "--reason", "r"); strings.Contains(stdout, "team_name") {
		t.Errorf("stdout = %q, want no team_name key", stdout)
	}
}

func TestRunLeadCmd_DeniedExit10(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateDenied, DecidedBy: &team.Client{Kind: "app", Label: "Purdex.app @ air26"}})
	code, stdout, stderr := driveLead(t, context.Background(), d, "--reason", "r")
	if code != ExitDenied || stdout != "" || !strings.Contains(stderr, "Purdex.app @ air26") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunLeadCmd_TimeoutExit11(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateTimeout})
	if code, stdout, stderr := driveLead(t, context.Background(), d, "--reason", "r"); code != ExitTimeout || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunLeadCmd_AbandonedAndCancelledExit12(t *testing.T) {
	for _, st := range []team.State{team.StateAbandoned, team.StateCancelled} {
		d := newFakeTeamDaemon(team.Approval{State: st})
		if code, stdout, stderr := driveLead(t, context.Background(), d, "--reason", "r"); code != ExitCancelled || stdout != "" {
			t.Errorf("%s: code=%d stdout=%q stderr=%q", st, code, stdout, stderr)
		}
	}
}

func TestRunLeadCmd_RequestOpenExit13(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{})
	d.createStatus, d.openID = http.StatusConflict, "open-1"
	code, stdout, stderr := driveLead(t, context.Background(), d, "--reason", "r")
	if code != ExitRefused || stdout != "" || !strings.Contains(stderr, "open-1") || !strings.Contains(stderr, team.ErrRequestOpen) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, polls, _, _ := d.snapshot(); len(polls) != 0 {
		t.Errorf("polled a refused request: %v", polls)
	}
}

// Every team-rule refusal of spec §14 is exit 13 with a stderr line naming
// the code; a 409 with a code the table does not know stays exit 1.
func TestRunLeadCmd_RuleRefusalsExit13(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{team.ErrRequestOpen, ExitRefused},
		{team.ErrAlreadyLead, ExitRefused},
		{team.ErrMemberCannotLead, ExitRefused},
		{"some_future_rule", ExitError},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			d := newFakeTeamDaemon(team.Approval{})
			d.createStatus, d.openID, d.refuseCode = http.StatusConflict, "open-1", tc.code
			code, stdout, stderr := driveLead(t, context.Background(), d, "--reason", "r")
			if code != tc.want || stdout != "" {
				t.Fatalf("code=%d want %d stdout=%q stderr=%q", code, tc.want, stdout, stderr)
			}
			if !strings.Contains(stderr, tc.code) {
				t.Errorf("stderr = %q, want it to name %q", stderr, tc.code)
			}
			if _, polls, _, _ := d.snapshot(); len(polls) != 0 {
				t.Errorf("polled a refused request: %v", polls)
			}
		})
	}
}

func TestRunLeadCmd_BadRequestExit1(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{})
	d.createStatus = http.StatusBadRequest
	if code, _, stderr := driveLead(t, context.Background(), d, "--reason", "r"); code != ExitError || !strings.Contains(stderr, "bad_request") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestRunLeadCmd_CancelOnCtxSendsDelete(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{})
	d.hold = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-d.pollStarted
		cancel()
	}()
	code, stdout, stderr := driveLead(t, ctx, d, "--reason", "r")
	if code != ExitCancelled || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	_, polls, deletes, _ := d.snapshot()
	if len(polls) != 1 {
		t.Errorf("polls = %v, want the one that was cut", polls)
	}
	if len(deletes) != 1 || deletes[0] != fixedID()() {
		t.Errorf("deletes = %v, want the request id exactly once", deletes)
	}
	if !strings.Contains(stderr, "已取消申請（"+fixedID()()+"）") {
		t.Errorf("stderr = %q", stderr)
	}
}

// The first signal cancels ctx; before the best-effort DELETE goes out the
// command must hand signal handling back to the runtime (onCancelled is the
// signal.NotifyContext stop func in production), so a second Ctrl-C during
// the 3 s DELETE terminates the process instead of being swallowed.
func TestRunLeadCmd_FirstSignalRestoresDefaultHandling(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{})
	d.hold = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-d.pollStarted
		cancel()
	}()
	restored, deletesAtRestore := 0, -1
	restore := func() {
		restored++
		_, _, deletes, _ := d.snapshot()
		deletesAtRestore = len(deletes)
	}
	code, stdout, stderr := driveLeadHook(t, ctx, d, []daemonclient.Option{leadClockOpt()}, restore, "--reason", "r")
	if code != ExitCancelled || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if restored != 1 {
		t.Fatalf("onCancelled ran %d time(s), want exactly once", restored)
	}
	if deletesAtRestore != 0 {
		t.Errorf("DELETE had already reached the daemon when onCancelled ran (deletes=%d), want 0", deletesAtRestore)
	}
	if _, _, deletes, _ := d.snapshot(); len(deletes) != 1 {
		t.Errorf("deletes = %v, want the request id exactly once after onCancelled", deletes)
	}
}

// The create POST carries a client UUID, so the client may replay it: the
// daemon reads the body and drops the connection, the CLI retries once and
// goes on to poll. Without daemonclient.Idempotent() the client returns
// ErrSentNoResponse and this is exit 1 with one create.
func TestRunLeadCmd_CreateDroppedAfterBodyIsReplayedOnce(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Grant: &team.Grant{MaxMembers: 3}})
	d.dropFirstCreate = true
	clock := newLeadClock()
	code, stdout, stderr := driveLeadWith(t, context.Background(), d,
		[]daemonclient.Option{clock.opt(), leadNoKeepAlive()}, "--reason", "r")
	if code != ExitOK || !strings.Contains(stdout, `"request_id"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	creates, polls, _, _ := d.snapshot()
	if len(creates) != 2 || creates[0].ID != fixedID()() || creates[1].ID != fixedID()() {
		t.Errorf("creates = %+v, want the same id twice", creates)
	}
	if len(polls) != 2 {
		t.Errorf("polls = %v, want 2", polls)
	}
	if clock.sleeps() != 1 || strings.Count(stderr, daemonclient.MsgRestarting) != 1 {
		t.Errorf("sleeps=%d stderr=%q, want one backoff and one restart line", clock.sleeps(), stderr)
	}
}

func TestRunLeadCmd_PlainNotFoundExit21(t *testing.T) {
	mux := http.NewServeMux() // an older daemon: no /api/team routes
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"boot_id":"b1"}`))
	})
	if code, stdout, stderr := driveLead(t, context.Background(), mux, "--reason", "r"); code != ExitUnsupported || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunLeadCmd_RefusedConnectionExit20(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	cfgPath := writeTestConfig(t, srv.URL, "admin-tok")
	srv.Close() // the port is now closed; the fake clock makes the 30 s grace instant

	var stdout, stderr bytes.Buffer
	code := runLeadCmd(context.Background(), []string{"request", "--reason", "r", "--config", cfgPath},
		leadEnv(), &stdout, &stderr, fixedID(), nil, leadClockOpt())
	if code != ExitUnavailable || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Count(stderr.String(), daemonclient.MsgRestarting) != 1 {
		t.Errorf("restart line count != 1: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "daemon_unavailable") {
		t.Errorf("stderr = %q, want the daemon_unavailable line", stderr.String())
	}
}

// A daemon that accepts every poll and never answers: each poll ends on the
// client's attempt timer (ErrNoAnswer, fired here by the fake clock), and
// the third in a row is exit 20 `daemon 沒有回應`. No DELETE: the request
// may still be answered by the daemon; only the caller gave up.
func TestRunLeadCmd_ThreeHungPollsExit20(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved})
	clock := newLeadClock()
	d.hold = true
	d.onPoll = func(int) { clock.fireNext() }
	code, stdout, stderr := driveLeadWith(t, context.Background(), d, []daemonclient.Option{clock.opt()}, "--reason", "r")
	if code != ExitUnavailable || stdout != "" || !strings.Contains(stderr, "daemon 沒有回應") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	_, polls, deletes, _ := d.snapshot()
	if len(polls) != 3 || len(deletes) != 0 {
		t.Errorf("polls=%v deletes=%v, want 3 polls and no delete", polls, deletes)
	}
	if strings.Contains(stderr, daemonclient.MsgRestarting) || clock.sleeps() != 0 {
		t.Errorf("a silent daemon is not a restart: sleeps=%d stderr=%q", clock.sleeps(), stderr)
	}
}

// The count is of consecutive hung polls: an answer in between resets it.
func TestRunLeadCmd_HungPollCountResetsOnAnswer(t *testing.T) {
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Grant: &team.Grant{MaxMembers: 3}})
	clock := newLeadClock()
	hung := map[int]bool{1: true, 3: true, 4: true}
	d.holdPoll = func(n int) bool { return hung[n] }
	d.onPoll = func(n int) {
		if hung[n] {
			clock.fireNext()
		}
	}
	d.openUntil = 2 // poll 2 answers open; poll 5 is the first final answer
	code, stdout, stderr := driveLeadWith(t, context.Background(), d, []daemonclient.Option{clock.opt()}, "--reason", "r")
	if code != ExitOK || !strings.Contains(stdout, `"request_id"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, polls, _, _ := d.snapshot(); len(polls) != 5 {
		t.Errorf("polls = %v, want 5 (hung, open, hung, hung, approved)", polls)
	}
}

// Review Focus 3, end to end through lead.go: the daemon restarts while
// `pdx lead request` is long-polling. The first daemon (boot b1) accepts
// the create; its first poll is dropped MID-FLIGHT — the request was read,
// the listener closes, then the socket closes with no response at all —
// so the client sees EOF / ECONNRESET on a GET, not a settled answer. The
// shipped client replays a GET after such an after-send failure (restart
// line once, then backoff), and its health probes before each retry are
// refused until the new daemon (boot b2, same port) is up: the restarted
// line is printed once, the SAME request id is polled again, found still
// open, then approved. No real time passes (fake clock).
//
// Each client request is a fresh connection (leadNoKeepAlive): on a reused
// one Go's Transport would replay the EOF'd GET by itself and the client
// under test would only ever see ECONNREFUSED — the retry under test would
// not be exercised.
//
// Sleep count = the backoff sleep the new daemon comes up on. Sleep 1
// follows the EOF (the GET's own failure); every refused health probe after
// that adds one more. With the new daemon up from sleep n, the probe after
// sleep n reaches it and no further backoff happens: sleeps == n.
func TestLeadRequest_SurvivesDaemonRestartMidPoll(t *testing.T) {
	t.Run("daemon2UpAtFirstBackoff", func(t *testing.T) { leadRestartScenario(t, 1) })
	t.Run("daemon2StartsAfterSeveralBackoffs", func(t *testing.T) { leadRestartScenario(t, 3) })
}

// leadRestartScenario runs the restart scenario with the second daemon
// bound on the upOnSleep-th backoff sleep and asserts the invariants above.
func leadRestartScenario(t *testing.T, upOnSleep int) {
	t.Helper()
	ln := leadReservePort(t)
	addr := ln.Addr().String()
	first := newFakeTeamDaemon(team.Approval{})
	firstSrv := leadServeOn(t, ln, first)
	first.onFirstPoll = func() { firstSrv.Listener.Close() } // the daemon is going down: no new connection is accepted
	defer firstSrv.Close()

	second := newFakeTeamDaemon(team.Approval{
		State: team.StateApproved,
		Grant: &team.Grant{MaxMembers: 3, Roots: []string{"/w"}},
	})
	second.bootID = "b2"
	var secondSrv *httptest.Server
	var once sync.Once
	clock := newLeadClock()
	clock.onSleep = func(n int) {
		if n == upOnSleep {
			once.Do(func() { secondSrv = leadServeOn(t, leadRelisten(t, addr), second) })
		}
	}
	defer func() {
		if secondSrv != nil {
			secondSrv.Close()
		}
	}()

	cfgPath := writeTestConfig(t, "http://"+addr, "admin-tok")
	var stdout, stderr bytes.Buffer
	code := runLeadCmd(context.Background(), []string{"request", "--reason", "r", "--config", cfgPath},
		leadEnv(), &stdout, &stderr, fixedID(), nil, clock.opt(), leadNoKeepAlive())
	if code != ExitOK {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var out struct {
		RequestID string     `json:"request_id"`
		Grant     team.Grant `json:"grant"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil || out.RequestID != fixedID()() || out.Grant.MaxMembers != 3 {
		t.Fatalf("stdout %q: %v", stdout.String(), err)
	}
	errText := stderr.String()
	if n := strings.Count(errText, daemonclient.MsgRestarting); n != 1 {
		t.Errorf("restart line count = %d, want 1: %q", n, errText)
	}
	if n := strings.Count(errText, "daemon 已重新啟動（boot b2）"); n != 1 {
		t.Errorf("restarted line count = %d, want 1: %q", n, errText)
	}
	if clock.sleeps() != upOnSleep {
		t.Errorf("sleeps = %d, want %d (one after the dropped poll, one per refused probe until the new daemon is up)", clock.sleeps(), upOnSleep)
	}

	// One create on the first daemon, none on the second: the request id is
	// never re-created, only re-polled.
	creates1, polls1, deletes1, _ := first.snapshot()
	creates2, polls2, deletes2, _ := second.snapshot()
	if len(creates1) != 1 || creates1[0].ID != fixedID()() || len(creates2) != 0 {
		t.Errorf("creates: first=%+v second=%+v", creates1, creates2)
	}
	if len(polls1) != 1 || len(polls2) != 2 {
		t.Errorf("polls: first=%v second=%v, want 1 then 2", polls1, polls2)
	}
	wantPoll := "/api/team/approvals/" + fixedID()() + "?wait=25"
	for _, p := range append(append([]string{}, polls1...), polls2...) {
		if !strings.HasSuffix(p, wantPoll) {
			t.Errorf("poll %q is not the same request id (%s)", p, wantPoll)
		}
	}
	if len(deletes1)+len(deletes2) != 0 {
		t.Errorf("a restart must not cancel the request: deletes=%v %v", deletes1, deletes2)
	}
}

// TL-1c, D-L4: --label is trimmed and sent as team_label; none (or a blank
// one) leaves the key out of the body.
func TestRunLeadCmd_LabelReachesTheRequestBody(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		want    string
		wantKey bool
	}{
		{[]string{"--reason", "r", "--label", "  A 線  "}, "A 線", true},
		{[]string{"--reason", "r", "--label", "資源租約派"}, "資源租約派", true}, // 10: five Chinese characters
		{[]string{"--reason", "r", "--label", "   "}, "", false},
		{[]string{"--reason", "r"}, "", false},
	} {
		var body []byte
		d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Grant: &team.Grant{MaxMembers: 3}})
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/api/team/approvals" {
				body, _ = io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
			d.ServeHTTP(w, r)
		})
		if code, _, stderr := driveLead(t, context.Background(), h, tc.args...); code != ExitOK {
			t.Fatalf("%v: code=%d stderr=%q", tc.args, code, stderr)
		}
		creates, _, _, _ := d.snapshot()
		if len(creates) != 1 || creates[0].TeamLabel != tc.want {
			t.Errorf("%v: team_label = %+v, want %q", tc.args, creates, tc.want)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("%v: body %q: %v", tc.args, body, err)
		}
		if _, has := raw["team_label"]; has != tc.wantKey {
			t.Errorf("%v: body %s has team_label = %v, want %v", tc.args, body, has, tc.wantKey)
		}
	}
}

// An invalid --label is refused before any HTTP call, naming the flag and the
// rule (the width), with the usage line.
func TestRunLeadCmd_InvalidLabelExits2BeforeAnyHTTPCall(t *testing.T) {
	for name, bad := range map[string]string{"six Chinese characters": "資源租約派工", "11 ASCII": "01234567890", "control": "a\x07b", "invisible": "️"} {
		d := newFakeTeamDaemon(team.Approval{State: team.StateApproved})
		code, stdout, stderr := driveLead(t, context.Background(), d, "--reason", "r", "--label", bad)
		creates, _, _, _ := d.snapshot()
		if code != ExitUsage || stdout != "" || !strings.Contains(stderr, "--label") || !strings.Contains(stderr, "usage: pdx lead request") || len(creates) != 0 {
			t.Errorf("%s: code=%d stdout=%q stderr=%q creates=%d", name, code, stdout, stderr, len(creates))
		}
	}
	if !strings.Contains(leadUsage, "[--label <短名>]") {
		t.Errorf("leadUsage = %q, want [--label <短名>]", leadUsage)
	}
}

// The older-daemon fallback (no grant on the approval) carries the requested
// label as the grant does.
func TestRunLeadCmd_ApprovedWithoutGrantCarriesTheLabel(t *testing.T) {
	payload, _ := json.Marshal(team.LeadPayload{Reason: "r", MaxMembers: 3, TeamLabel: "A 線"})
	d := newFakeTeamDaemon(team.Approval{State: team.StateApproved, Payload: payload})
	code, stdout, _ := driveLead(t, context.Background(), d, "--reason", "r", "--label", "A 線")
	var out struct {
		Grant team.Grant `json:"grant"`
	}
	if code != ExitOK || json.Unmarshal([]byte(stdout), &out) != nil || out.Grant.TeamLabel == nil || *out.Grant.TeamLabel != "A 線" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	payload, _ = json.Marshal(team.LeadPayload{Reason: "r", MaxMembers: 3})
	d = newFakeTeamDaemon(team.Approval{State: team.StateApproved, Payload: payload})
	if _, stdout, _ := driveLead(t, context.Background(), d, "--reason", "r"); strings.Contains(stdout, "team_label") {
		t.Errorf("stdout = %q, want no team_label key", stdout)
	}
	// An approved approval with its grant prints the grant as the daemon gave it.
	empty := ""
	d = newFakeTeamDaemon(team.Approval{State: team.StateApproved, Grant: &team.Grant{MaxMembers: 3, TeamLabel: &empty}})
	if _, stdout, _ := driveLead(t, context.Background(), d, "--reason", "r"); !strings.Contains(stdout, `"team_label":""`) {
		t.Errorf("stdout = %q, want the grant's team_label as it is", stdout)
	}
}
