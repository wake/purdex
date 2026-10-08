package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/resources"
)

// runArgs puts the command after the `--` that ends the flags.
func runArgs(flags []string, cmd ...string) []string {
	return append(append(append([]string{}, flags...), "--"), cmd...)
}

// driveRunCmd runs `pdx lease run <flags> -- <cmd…>` against d with the real
// clock (the child is a real process) and the signal channel sigs (nil: none).
func driveRunCmd(t *testing.T, d *fakeLeaseDaemon, sigs chan os.Signal, flags []string, cmd ...string) (int, string, string) {
	t.Helper()
	fixedHolder(t)
	old := leaseSignals
	leaseSignals = func() (<-chan os.Signal, func()) {
		if sigs == nil {
			return make(chan os.Signal), func() {}
		}
		return sigs, func() {}
	}
	t.Cleanup(func() { leaseSignals = old })
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout, stderr bytes.Buffer
	args := append([]string{"run", "--config", cfg}, runArgs(flags, cmd...)...)
	code := runLeaseCmd(context.Background(), args, fakeGetenv(nil), &stdout, &stderr, leadNoKeepAlive())
	return code, stdout.String(), stderr.String()
}

func TestRun_ExitCodePassthrough(t *testing.T) {
	for name, c := range map[string]struct {
		cmd  []string
		want int
	}{
		"zero":       {[]string{"sh", "-c", "exit 0"}, 0},
		"seven":      {[]string{"sh", "-c", "exit 7"}, 7},
		"signalled":  {[]string{"sh", "-c", "kill -9 $$"}, 128 + 9},
		"not found":  {[]string{"/no/such/pdx-lease-test-command"}, 127},
		"not a file": {[]string{"/"}, 126},
	} {
		d := &fakeLeaseDaemon{}
		code, _, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build"}, c.cmd...)
		if code != c.want {
			t.Errorf("%s: code %d, want %d (stderr %q)", name, code, c.want, stderr)
		}
	}
}

// The pdx process is the holder, the command is its child; the lease is given
// back after the child, and the daemon sees a DELETE by id.
func TestRun_HoldsAsPdxAndReleasesAfterTheChild(t *testing.T) {
	d := &fakeLeaseDaemon{}
	code, _, stderr := driveRunCmd(t, d, nil, []string{"--weight", "30", "--wait", "2m"}, "sh", "-c", "exit 3")
	posts, _, deletes := d.snapshot()
	if code != 3 || stderr != "" || len(posts) != 1 {
		t.Fatalf("code=%d stderr=%q posts=%d", code, stderr, len(posts))
	}
	p := posts[0]
	if p.HolderPID != os.Getpid() || p.Scope != resources.ScopeProcess || p.Weight != 30 || p.WaitS != 120 || p.SessionID != "" {
		t.Errorf("request = %+v", p)
	}
	if len(deletes) != 1 || deletes[0] != "/api/resources/leases/"+leaseRow {
		t.Errorf("deletes = %v", deletes)
	}
}

// The child sees the same stdin/stdout, and runs after the grant.
func TestRun_ChildOutputIsOurs(t *testing.T) {
	// stdout of the child is os.Stdout, not the captured writer: check through a file.
	out := filepath.Join(t.TempDir(), "out")
	d := &fakeLeaseDaemon{}
	code, _, _ := driveRunCmd(t, d, nil, []string{"--kind", "build"}, "sh", "-c", "echo ran > "+out)
	b, err := os.ReadFile(out)
	if code != 0 || err != nil || strings.TrimSpace(string(b)) != "ran" {
		t.Errorf("code=%d out=%q err=%v", code, b, err)
	}
}

// A signal sent to pdx reaches the child's process group: the child traps
// TERM and exits 7, and that is pdx's exit code.
func TestRun_SignalForwarded(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ready")
	sigs := make(chan os.Signal, 1)
	go func() {
		for {
			if _, err := os.Stat(marker); err == nil {
				sigs <- syscall.SIGTERM
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	d := &fakeLeaseDaemon{}
	script := `trap 'exit 7' TERM; touch ` + marker + `; while :; do sleep 0.05; done`
	code, _, _ := driveRunCmd(t, d, sigs, []string{"--kind", "build"}, "sh", "-c", script)
	_, _, deletes := d.snapshot()
	if code != 7 || len(deletes) != 1 {
		t.Fatalf("code=%d deletes=%v", code, deletes)
	}
}

// The child leads its own process group, unless stdin is a terminal: a group
// that is not the terminal's foreground would be stopped by SIGTTIN when it
// read.
func TestRun_ProcessGroupDependsOnTheTerminal(t *testing.T) {
	for _, tty := range []bool{false, true} {
		old := leaseStdinIsTTY
		leaseStdinIsTTY = func() bool { return tty }
		out := filepath.Join(t.TempDir(), "pgid")
		d := &fakeLeaseDaemon{}
		driveRunCmd(t, d, nil, []string{"--kind", "build"}, "sh", "-c", "ps -o pgid= -p $$ > "+out)
		leaseStdinIsTTY = old
		b, _ := os.ReadFile(out)
		got := strings.TrimSpace(string(b))
		same := got == strconv.Itoa(syscall.Getpgrp())
		if same != tty {
			t.Errorf("tty=%v: child pgid %q, pdx pgid %d (same group: %v)", tty, got, syscall.Getpgrp(), same)
		}
	}
}

// cancelOnWrite cancels a context when something is written to it: the signal
// that arrives while the wait notice is being printed, after the grant.
type cancelOnWrite struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (c *cancelOnWrite) Write(p []byte) (int, error) {
	c.cancel()
	return c.Buffer.Write(p)
}

// A signal that came after the grant and before the child started is not
// lost: the command does not run, the lease is given back, exit 12.
func TestRun_InterruptBetweenGrantAndStartDoesNotRun(t *testing.T) {
	fixedHolder(t)
	ctx, cancel := context.WithCancel(context.Background())
	d := &fakeLeaseDaemon{
		post: func(resources.LeaseRequest) (int, any) {
			// A grant that waited 5 s: acquire returns it, then the notice is printed.
			return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateHeld, Granted: true, WaitedMS: 5000}
		},
	}
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	out := filepath.Join(t.TempDir(), "out")
	var stdout bytes.Buffer
	stderr := &cancelOnWrite{cancel: cancel}
	code := runLeaseCmd(ctx, []string{"run", "--config", cfg, "--kind", "build", "--", "sh", "-c", "echo ran > " + out},
		fakeGetenv(nil), &stdout, stderr, leadNoKeepAlive())
	_, _, deletes := d.snapshot()
	if _, err := os.Stat(out); code != ExitCancelled || err == nil || len(deletes) != 1 {
		t.Fatalf("code=%d ran=%v deletes=%v", code, err == nil, deletes)
	}
}

// SIGHUP ends only `run` (a closing terminal); the other lease commands keep
// the default for it.
func TestRunSignals_SIGHUPOnlyForRun(t *testing.T) {
	has := func(args ...string) bool {
		for _, s := range runSignals(args) {
			if s == syscall.SIGHUP {
				return true
			}
		}
		return false
	}
	if !has("run") || has("acquire") || has("ls") || has("release") || has() {
		t.Error("SIGHUP is wired to the wrong commands")
	}
}

func TestRun_WaitedLines(t *testing.T) {
	waiting := func(r resources.LeaseResponse) *fakeLeaseDaemon {
		return &fakeLeaseDaemon{
			post: func(resources.LeaseRequest) (int, any) {
				return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting}
			},
			pollList: []resources.LeaseResponse{r},
		}
	}
	d := waiting(resources.LeaseResponse{ID: leaseRow, State: resources.StateHeld, Granted: true, WaitedMS: 37000, Host: resources.LeaseHost{Measured: 72}})
	_, _, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build"}, "true")
	if stderr != "pdx lease: 等了 37 秒主機資源（負載 72/100）\n" {
		t.Errorf("waited: %q", stderr)
	}
	d = waiting(resources.LeaseResponse{ID: leaseRow, State: resources.StateHeld, Granted: true, Overrun: true, WaitedMS: 300000, Host: resources.LeaseHost{Measured: 99}})
	_, _, stderr = driveRunCmd(t, d, nil, []string{"--kind", "build"}, "true")
	if stderr != "pdx lease: 等滿 5 分鐘，超量放行（已記錄）\n" {
		t.Errorf("overrun: %q", stderr)
	}
	// A wait under a second is not worth a line.
	d = waiting(resources.LeaseResponse{ID: leaseRow, State: resources.StateHeld, Granted: true, WaitedMS: 400})
	if _, _, stderr = driveRunCmd(t, d, nil, []string{"--kind", "build"}, "true"); stderr != "" {
		t.Errorf("short wait: %q", stderr)
	}
}

// The pool is advice: with no daemon the command runs anyway.
func TestRun_FailOpenRuns(t *testing.T) {
	fixedHolder(t)
	srv := httptest.NewServer(nil)
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	srv.Close()
	out := filepath.Join(t.TempDir(), "out")
	var stdout, stderr bytes.Buffer
	code := runLeaseCmd(context.Background(), []string{"run", "--config", cfg, "--kind", "build", "--", "sh", "-c", "echo ran > " + out + "; exit 4"},
		fakeGetenv(nil), &stdout, &stderr, leadNoKeepAlive(), daemonclient.WithAttemptTimeout(200*time.Millisecond), daemonclient.WithGrace(100*time.Millisecond))
	b, _ := os.ReadFile(out)
	if code != 4 || strings.TrimSpace(string(b)) != "ran" || !strings.Contains(stderr.String(), "直接執行") {
		t.Fatalf("code=%d out=%q stderr=%q", code, b, stderr.String())
	}
}

// Interrupted while it waits: exit 12 and the command never starts.
func TestRun_InterruptWhileWaitingDoesNotRun(t *testing.T) {
	fixedHolder(t)
	ctx, cancel := context.WithCancel(context.Background())
	d := &fakeLeaseDaemon{
		post: func(resources.LeaseRequest) (int, any) {
			return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting}
		},
		hold: true,
	}
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	out := filepath.Join(t.TempDir(), "out")
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	var stdout, stderr bytes.Buffer
	code := runLeaseCmd(ctx, []string{"run", "--config", cfg, "--kind", "build", "--", "sh", "-c", "echo ran > " + out},
		fakeGetenv(nil), &stdout, &stderr, leadNoKeepAlive(), daemonclient.WithAttemptTimeout(10*time.Second))
	_, _, deletes := d.snapshot()
	if _, err := os.Stat(out); code != ExitCancelled || err == nil || len(deletes) != 1 {
		t.Fatalf("code=%d ran=%v deletes=%v", code, err == nil, deletes)
	}
}

func TestRun_UsageErrors(t *testing.T) {
	for name, args := range map[string][]string{
		"no command":    {"run", "--kind", "build", "--"},
		"no separator":  {"run", "--kind", "build", "make"},
		"neither":       {"run", "--", "true"},
		"both":          {"run", "--kind", "build", "--weight", "5", "--", "true"},
		"wait too long": {"run", "--kind", "build", "--wait", "20m", "--", "true"},
		"unknown flag":  {"run", "--bogus", "--kind", "build", "--", "true"},
	} {
		d := &fakeLeaseDaemon{}
		srv := httptest.NewServer(d)
		cfg := writeTestConfig(t, srv.URL, "admin-tok")
		var stdout, errb bytes.Buffer
		// --config goes before the `--` that starts the command.
		code := runLeaseCmd(context.Background(), append([]string{args[0], "--config", cfg}, args[1:]...), fakeGetenv(nil), &stdout, &errb, leadNoKeepAlive())
		srv.Close()
		stderr := errb.String()
		posts, polls, deletes := d.snapshot()
		if code != ExitUsage || !strings.HasPrefix(stderr, "pdx lease: ") || len(posts)+polls+len(deletes) != 0 {
			t.Errorf("%s: code=%d stderr=%q", name, code, stderr)
		}
	}
}

func TestLs_HoldersWaitersRecentOverrun(t *testing.T) {
	old := leaseNow
	leaseNow = func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { leaseNow = old })
	snap := resources.Snapshot{
		Available: true, Capacity: 100, Host: resources.HostUse{Measured: 80, Load1: 8, NCPU: 10, Mem: 50, Pressure: 1},
		Leases: []resources.LeaseView{
			{ID: "h1", Kind: "build", Weight: 35, Charge: 17.5, Use: 12, SessionID: "11111111-aaaa", AgeS: 90},
			{ID: "h2", Kind: "test-full", Weight: 35, Charge: 35, Use: 40, SessionID: "22222222-bbbb", AgeS: 4000, Overrun: true},
		},
		Waiters: []resources.WaiterView{{ID: "w1", Kind: "test-pkg", Weight: 15, SessionID: "33333333-cccc", Position: 1, WaitedS: 37, DeadlineInS: 263}},
		Recent: []resources.RecentView{
			{ID: "e1", Kind: "build", Weight: 35, SessionID: "11111111-aaaa", EndReason: "released", WaitedMS: 2000, EndedAt: time.Date(2026, 10, 9, 11, 58, 0, 0, time.UTC)},
			{ID: "e2", Kind: "test-full", Weight: 35, SessionID: "44444444-dddd", EndReason: "released", Overrun: true, WaitedMS: 300000, EndedAt: time.Date(2026, 10, 9, 11, 50, 0, 0, time.UTC)},
		},
	}
	var out, errb bytes.Buffer
	if code := printLeaseTable(&out, &errb, snap); code != ExitOK {
		t.Fatal(code, errb.String())
	}
	got := out.String()
	for _, want := range []string{"HOLDERS", "WAITERS", "RECENT", "1h 超量", "#1  ", "4m", "released 超量", "2m ago", "10m ago", "5m"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Nothing held, nothing queued, nothing recent: no sections.
	out.Reset()
	printLeaseTable(&out, &errb, resources.Snapshot{Available: true, Capacity: 100, Host: resources.HostUse{Measured: 5, NCPU: 10}})
	if s := out.String(); strings.Contains(s, "HOLDERS") || strings.Contains(s, "WAITERS") || strings.Contains(s, "RECENT") {
		t.Errorf("empty sections printed:\n%s", s)
	}
}
