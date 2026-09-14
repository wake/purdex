package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/peers/proxyhelper"
	"github.com/wake/purdex/internal/peers/proxyhelper/proxyhelpertest"
)

// --- fakes ------------------------------------------------------------------

const (
	// stPanePID is the tmux pane pid: the `sh -c` wrapper that pipes into
	// claude. stTargetPID is the claude process, learned from the registry.
	stPanePID         = 4100
	stPaneProcStart   = "Mon Sep 14 01:02:02 2026"
	stTargetPID       = 4242
	stTargetProcStart = "Mon Sep 14 01:02:03 2026"
	stForeignStart    = "Mon Jan  1 00:00:00 2001"
)

// sigCall is one recorded deps.signal invocation.
type sigCall struct {
	pid int
	sig os.Signal
}

// fakeClock backs deps.now/deps.sleep: sleep advances the clock and
// returns at once, except that a "long" sleep (≥ 1 s — only the reply
// wait sleeps that long) blocks until ctx is cancelled unless expireLong
// is set, so a test decides whether the reply wait times out.
type fakeClock struct {
	mu         sync.Mutex
	t          time.Time
	expireLong bool
	sleeps     []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	expire := c.expireLong
	c.mu.Unlock()
	if d >= time.Second && !expire {
		<-ctx.Done()
		return ctx.Err()
	}
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
	return nil
}

// tmuxCall is one recorded deps.tmux invocation.
type tmuxCall struct {
	args   []string
	ctxErr error // ctx.Err() at call time
}

// fakePeer is a selftestPeer whose Frames the test feeds directly.
type fakePeer struct {
	pid    int
	sock   string
	files  []string
	frames chan string

	mu    sync.Mutex
	stops int
}

func (p *fakePeer) PID() int              { return p.pid }
func (p *fakePeer) Sock() string          { return p.sock }
func (p *fakePeer) Files() []string       { return append([]string(nil), p.files...) }
func (p *fakePeer) Frames() <-chan string { return p.frames }
func (p *fakePeer) Stop(time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stops++
	return nil
}
func (p *fakePeer) Stops() int { p.mu.Lock(); defer p.mu.Unlock(); return p.stops }

// stFixture is a fully faked selftest environment.
type stFixture struct {
	t     *testing.T
	deps  selftestDeps
	clock *fakeClock

	registryDir, sockDir string
	inbox                string

	mu          sync.Mutex
	tmuxCalls   []tmuxCall
	polls       int
	signals     []sigCall
	writes      []stWrite
	removed     []string
	alive       map[int]bool   // pidAlive answers; absent ⇒ dead
	starts      map[int]string // procStart answers; absent ⇒ error
	entries     func(poll int) []ipeers.Entry
	listPanes   func() ([]byte, error)
	newSession  func() ([]byte, error)
	killSession func() ([]byte, error)
	removeErr   func(path string) error
	dialRefused func(sock string) bool

	peer     *fakePeer
	spawnErr error
	spawnCfg proxyhelper.Config
	spawned  bool
	// onWrite runs inside the fake writeFrame (after recording) — used to
	// inject a reply or cancel the run ctx at the right moment.
	onWrite func()
	// writeErr, when set, is what writeFrame returns.
	writeErr error
}

type stWrite struct {
	sock string
	line []byte
}

func newStFixture(t *testing.T) *stFixture {
	t.Helper()
	sockDir, registryDir := proxyhelpertest.TempDirs(t)
	f := &stFixture{
		t:           t,
		clock:       newFakeClock(),
		registryDir: registryDir,
		sockDir:     sockDir,
		inbox:       filepath.Join(sockDir, strconv.Itoa(stTargetPID)+".sock"),
		alive:       map[int]bool{},
		starts:      map[int]string{stPanePID: stPaneProcStart, stTargetPID: stTargetProcStart},
	}
	f.peer = &fakePeer{
		pid:    900001,
		sock:   filepath.Join(sockDir, "900001.sock"),
		files:  []string{filepath.Join(registryDir, "900001.json"), filepath.Join(registryDir, "900001.abcd.key")},
		frames: make(chan string, 16),
	}
	f.listPanes = func() ([]byte, error) { return []byte(strconv.Itoa(stPanePID) + "\n"), nil }
	f.newSession = func() ([]byte, error) { return nil, nil }
	f.killSession = func() ([]byte, error) { return nil, nil }
	f.dialRefused = func(string) bool { return true }
	// Default: the entry appears on the very first poll.
	f.entries = func(int) []ipeers.Entry { return []ipeers.Entry{f.targetEntry()} }

	f.deps = selftestDeps{
		tmux: func(ctx context.Context, args ...string) ([]byte, error) {
			f.mu.Lock()
			f.tmuxCalls = append(f.tmuxCalls, tmuxCall{args: append([]string(nil), args...), ctxErr: ctx.Err()})
			f.mu.Unlock()
			switch args[0] {
			case "new-session":
				return f.newSession()
			case "list-panes":
				return f.listPanes()
			case "kill-session":
				return f.killSession()
			}
			return nil, fmt.Errorf("unexpected tmux verb %q", args[0])
		},
		registryDir: registryDir,
		sockDir:     sockDir,
		cwd:         "/tmp",
		readRegistry: func(dir string) ([]ipeers.Entry, error) {
			if dir != registryDir {
				t.Errorf("readRegistry dir = %q, want %q", dir, registryDir)
			}
			f.mu.Lock()
			f.polls++
			n := f.polls
			f.mu.Unlock()
			return f.entries(n), nil
		},
		spawn: func(ctx context.Context, cfg proxyhelper.Config) (selftestPeer, error) {
			f.mu.Lock()
			f.spawned = true
			f.spawnCfg = cfg
			f.mu.Unlock()
			if f.spawnErr != nil {
				return nil, f.spawnErr
			}
			return f.peer, nil
		},
		writeFrame: func(ctx context.Context, sock string, line []byte, timeout time.Duration) error {
			f.mu.Lock()
			f.writes = append(f.writes, stWrite{sock: sock, line: append([]byte(nil), line...)})
			f.mu.Unlock()
			if f.writeErr != nil {
				return f.writeErr
			}
			if f.onWrite != nil {
				f.onWrite()
			}
			return nil
		},
		pidAlive: func(pid int) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.alive[pid]
		},
		procStart: func(pid int) (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if s, ok := f.starts[pid]; ok {
				return s, nil
			}
			return "", fmt.Errorf("ps -p %d: no such process", pid)
		},
		signal: func(pid int, sig os.Signal) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.signals = append(f.signals, sigCall{pid, sig})
			return nil
		},
		readPeerFeatures:  func(dir string, pid int) ([]string, bool) { return nil, false },
		glob:              filepath.Glob,
		registryProcStart: ccuds.RegistryProcStart,
		remove: func(path string) error {
			if f.removeErr != nil {
				if err := f.removeErr(path); err != nil {
					return err
				}
			}
			err := os.Remove(path)
			if err == nil {
				f.mu.Lock()
				f.removed = append(f.removed, path)
				f.mu.Unlock()
			}
			return err
		},
		dialRefused: func(sock string) bool { return f.dialRefused(sock) },
		sleep:       f.clock.sleep,
		now:         f.clock.now,
	}
	return f
}

func (f *stFixture) targetEntry() ipeers.Entry {
	return ipeers.Entry{
		PID:       stTargetPID,
		SessionID: "cccccccc-3333-4333-8333-333333333333",
		Name:      "whatever",
		Tmux:      "<name>:@1.%1", // rewritten per run in run()
		Inbox:     f.inbox,
		ProcStart: stTargetProcStart,
		Version:   "2.1.270",
	}
}

// run executes the selftest with ctx and returns exit code and stdout.
// The fixture learns the session name from the new-session call so
// registry entries can carry the right Tmux field.
func (f *stFixture) run(ctx context.Context, timeout time.Duration) (int, string, string) {
	f.t.Helper()
	var stdout, stderr bytes.Buffer
	orig := f.entries
	f.entries = func(n int) []ipeers.Entry {
		name := f.sessionName()
		es := orig(n)
		for i := range es {
			es[i].Tmux = strings.Replace(es[i].Tmux, "<name>", name, 1)
		}
		return es
	}
	done := make(chan int, 1)
	go func() { done <- runMsgSelftest(ctx, f.deps, timeout, &stdout, &stderr) }()
	select {
	case code := <-done:
		return code, stdout.String(), stderr.String()
	case <-time.After(10 * time.Second):
		f.t.Fatalf("runMsgSelftest did not return within 10 s; stdout so far:\n%s", stdout.String())
		return -1, "", ""
	}
}

func (f *stFixture) sessionName() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.tmuxCalls {
		if c.args[0] == "new-session" {
			return c.args[3]
		}
	}
	return ""
}

func (f *stFixture) calls(verb string) []tmuxCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []tmuxCall
	for _, c := range f.tmuxCalls {
		if c.args[0] == verb {
			out = append(out, c)
		}
	}
	return out
}

func (f *stFixture) lastLine(stdout string) string {
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	return lines[len(lines)-1]
}

// replyFromTarget builds a native reply frame from the target's inbox.
func (f *stFixture) replyFromTarget(text string) string {
	line, err := ccuds.BuildFrame("reply-1", f.inbox, ccuds.Wrapper{
		From: "uds:" + f.inbox, FromName: "target", FromMode: ipeers.ModePrompting, Text: text,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.TrimSuffix(string(line), "\n")
}

// writtenNonce parses the frame the selftest wrote to the target inbox
// and returns the nonce it carries.
func (f *stFixture) writtenNonce() string {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) != 1 {
		f.t.Fatalf("writes = %d, want 1", len(f.writes))
	}
	fr, err := ccuds.ParseFrame(f.writes[0].line)
	if err != nil {
		f.t.Fatalf("written frame does not parse: %v", err)
	}
	w, ok := ccuds.Parse(fr.Message.Content)
	if !ok {
		f.t.Fatalf("written content is not a wrapper: %q", fr.Message.Content)
	}
	// "PDX_SELFTEST <nonce>: reply with exactly: PONG <nonce>"
	fields := strings.Fields(w.Text)
	if len(fields) < 2 || fields[0] != "PDX_SELFTEST" {
		f.t.Fatalf("unexpected text %q", w.Text)
	}
	return strings.TrimSuffix(fields[1], ":")
}

func writeRegistryFile(t *testing.T, path, procStart string) {
	t.Helper()
	body := `{"pid":` + strconv.Itoa(stTargetPID) + `,"sessionId":"x","procStart":"` + procStart + `","peerToken":"y","pidDomain":"darwin"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// --- tmux argv goldens ------------------------------------------------------

func TestSelftest_TmuxArgvGoldens(t *testing.T) {
	f := newStFixture(t)
	f.onWrite = func() { f.peer.frames <- f.replyFromTarget("PONG " + f.writtenNonce()) }
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 0 {
		t.Fatalf("exit = %d, stdout:\n%s", code, out)
	}
	name := f.sessionName()
	if !strings.HasPrefix(name, "pdx-selftest-") || len(name) != len("pdx-selftest-")+6 {
		t.Errorf("session name = %q, want pdx-selftest-<6 hex>", name)
	}

	// A pipe on stdin keeps `claude -p --input-format stream-json` alive
	// (a tty stdin makes it exit at once); the claude arguments are
	// positional to `sh -c` so nothing is re-quoted.
	wantNew := []string{"new-session", "-d", "-s", name, "--",
		"sh", "-c", `sleep 2147483647 | exec claude "$@"`, "pdx-selftest",
		"-p", "--verbose",
		"--input-format", "stream-json", "--output-format", "stream-json",
		"--name", name, "--settings", `{"crossSessionInbound":"accept"}`}
	if got := f.calls("new-session"); len(got) != 1 || !equalArgs(got[0].args, wantNew) {
		t.Errorf("new-session argv = %v\nwant %q", got, wantNew)
	}
	wantPanes := []string{"list-panes", "-t", name, "-F", "#{pane_pid}"}
	if got := f.calls("list-panes"); len(got) != 1 || !equalArgs(got[0].args, wantPanes) {
		t.Errorf("list-panes argv = %v\nwant %q", got, wantPanes)
	}
	wantKill := []string{"kill-session", "-t", name}
	if got := f.calls("kill-session"); len(got) != 1 || !equalArgs(got[0].args, wantKill) {
		t.Errorf("kill-session argv = %v\nwant %q", got, wantKill)
	}
	// Order: new-session, list-panes, …, kill-session last.
	f.mu.Lock()
	verbs := make([]string, 0, len(f.tmuxCalls))
	for _, c := range f.tmuxCalls {
		verbs = append(verbs, c.args[0])
	}
	f.mu.Unlock()
	if want := []string{"new-session", "list-panes", "kill-session"}; !equalArgs(verbs, want) {
		t.Errorf("tmux verbs = %v, want %v", verbs, want)
	}
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSelftest_NewSessionFailure(t *testing.T) {
	f := newStFixture(t)
	f.newSession = func() ([]byte, error) { return nil, errors.New("exec: tmux: not found") }
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "FAIL: tmux new-session: exec: tmux: not found\n") {
		t.Errorf("stdout:\n%s", out)
	}
	// new-session may have created the session before failing (or being
	// cancelled): kill-session is always attempted.
	if got := f.calls("kill-session"); len(got) != 1 {
		t.Errorf("kill-session calls = %d, want 1", len(got))
	}
	if got := f.calls("list-panes"); len(got) != 0 {
		t.Errorf("list-panes called after new-session failed")
	}
	if f.spawned {
		t.Errorf("helper spawned after new-session failed")
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

// TestSelftest_NewSessionCancelledInFlight: the run ctx is cancelled while
// tmux is creating the session — the client reports an error although the
// session exists. Cleanup must still kill it by name, under its own ctx.
func TestSelftest_NewSessionCancelledInFlight(t *testing.T) {
	f := newStFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sessionExists := false
	f.newSession = func() ([]byte, error) {
		sessionExists = true // tmux got there first
		cancel()
		return nil, context.Canceled
	}
	f.killSession = func() ([]byte, error) {
		if !sessionExists {
			return nil, &exec.ExitError{Stderr: []byte("can't find session\n")}
		}
		sessionExists = false
		return nil, nil
	}
	code, out, _ := f.run(ctx, 5*time.Second)
	if code != 1 || !strings.Contains(out, "FAIL: tmux new-session: context canceled\n") {
		t.Errorf("exit = %d, stdout:\n%s", code, out)
	}
	kills := f.calls("kill-session")
	if len(kills) != 1 {
		t.Fatalf("kill-session calls = %d, want 1", len(kills))
	}
	if kills[0].ctxErr != nil {
		t.Errorf("kill-session ran under the cancelled run ctx: %v", kills[0].ctxErr)
	}
	if sessionExists {
		t.Errorf("the session tmux created was left behind")
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

// --- step 2: identity capture -----------------------------------------------

func TestSelftest_ListPanesFailure(t *testing.T) {
	f := newStFixture(t)
	f.listPanes = func() ([]byte, error) { return nil, errors.New("exit status 1") }
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "FAIL: cannot identify the throwaway session's process\n") {
		t.Errorf("stdout:\n%s", out)
	}
	if got := f.calls("kill-session"); len(got) != 1 {
		t.Errorf("kill-session calls = %d, want 1", len(got))
	}
	if f.spawned {
		t.Errorf("helper spawned after identity capture failed")
	}
	f.mu.Lock()
	polls, signals := f.polls, len(f.signals)
	f.mu.Unlock()
	if polls != 0 {
		t.Errorf("registry polled %d times without a pid", polls)
	}
	if signals != 0 {
		t.Errorf("signals sent without a pid: %d", signals)
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

func TestSelftest_ListPanesGarbage(t *testing.T) {
	f := newStFixture(t)
	f.listPanes = func() ([]byte, error) { return []byte("not-a-pid\n"), nil }
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 || !strings.Contains(out, "FAIL: cannot identify the throwaway session's process\n") {
		t.Errorf("exit = %d, stdout:\n%s", code, out)
	}
}

// --- step 3: registration polling -------------------------------------------

func TestSelftest_EntryAppearsOnThirdPoll(t *testing.T) {
	f := newStFixture(t)
	f.entries = func(n int) []ipeers.Entry {
		if n < 3 {
			return nil
		}
		return []ipeers.Entry{f.targetEntry()}
	}
	f.onWrite = func() { f.peer.frames <- f.replyFromTarget("PONG " + f.writtenNonce()) }
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 0 {
		t.Fatalf("exit = %d, stdout:\n%s", code, out)
	}
	f.mu.Lock()
	polls := f.polls
	f.mu.Unlock()
	if polls != 3 {
		t.Errorf("polls = %d, want 3", polls)
	}
	if !f.spawned {
		t.Errorf("helper not spawned after registration")
	}
}

// TestSelftest_IdentifyFromRegistry: the pane pid is the `sh -c` wrapper;
// the claude pid comes from the registry entry, and it is that pid whose
// procStart is captured and whose peerFeatures are copied.
func TestSelftest_IdentifyFromRegistry(t *testing.T) {
	f := newStFixture(t)
	var featPID int
	f.deps.readPeerFeatures = func(dir string, pid int) ([]string, bool) {
		featPID = pid
		return nil, false
	}
	f.onWrite = func() { f.peer.frames <- f.replyFromTarget("PONG " + f.writtenNonce()) }
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 0 {
		t.Fatalf("exit = %d, stdout:\n%s", code, out)
	}
	if featPID != stTargetPID {
		t.Errorf("peerFeatures read for pid %d, want the registry's %d (not the pane's %d)", featPID, stTargetPID, stPanePID)
	}
}

func TestSelftest_RegistryPIDProcStartFailure(t *testing.T) {
	f := newStFixture(t)
	f.mu.Lock()
	delete(f.starts, stTargetPID) // the claude process vanished right after registering
	f.mu.Unlock()
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 || !strings.Contains(out, "FAIL: cannot identify the throwaway session's process\n") {
		t.Errorf("exit = %d, stdout:\n%s", code, out)
	}
	if f.spawned {
		t.Errorf("helper spawned without a target identity")
	}
	if got := f.calls("kill-session"); len(got) != 1 {
		t.Errorf("kill-session calls = %d, want 1", len(got))
	}
}

func TestSelftest_EntryMatchedByNameOnly(t *testing.T) {
	f := newStFixture(t)
	f.entries = func(n int) []ipeers.Entry {
		otherName := f.targetEntry()
		otherName.PID = 9999
		otherName.Tmux = "someone-else:@1.%1"
		proxy := f.targetEntry()
		proxy.IsProxy = true
		proxy.PID = 9998
		if n < 2 {
			return []ipeers.Entry{otherName, proxy}
		}
		return []ipeers.Entry{otherName, proxy, f.targetEntry()}
	}
	f.onWrite = func() { f.peer.frames <- f.replyFromTarget("PONG " + f.writtenNonce()) }
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 0 {
		t.Fatalf("exit = %d, stdout:\n%s", code, out)
	}
	f.mu.Lock()
	polls := f.polls
	writes := append([]stWrite(nil), f.writes...)
	f.mu.Unlock()
	if polls != 2 {
		t.Errorf("polls = %d, want 2 (other-name and proxy entries must not match)", polls)
	}
	if len(writes) != 1 || writes[0].sock != f.inbox {
		t.Errorf("frame written to %v, want the target inbox %s", writes, f.inbox)
	}
}

func TestSelftest_NeverRegisters_KillsAndSignalsPane(t *testing.T) {
	f := newStFixture(t)
	f.entries = func(int) []ipeers.Entry { return nil }
	f.alive[stPanePID] = true // the wrapper ignores kill-session
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "FAIL: session did not register (Claude Code ≥ 2.1.224 with peer messaging required)\n") {
		t.Errorf("stdout:\n%s", out)
	}
	if got := f.calls("kill-session"); len(got) != 1 {
		t.Errorf("kill-session calls = %d, want 1", len(got))
	}
	if f.spawned {
		t.Errorf("helper spawned without registration")
	}
	f.mu.Lock()
	polls := f.polls
	signals := append([]sigCall(nil), f.signals...)
	f.mu.Unlock()
	// 15 s at 250 ms: the first poll at t=0, then one per sleep.
	if polls < 60 || polls > 62 {
		t.Errorf("polls = %d, want ≈ 61 (15 s / 250 ms)", polls)
	}
	want := []sigCall{{stPanePID, syscall.SIGTERM}, {stPanePID, syscall.SIGKILL}}
	if !equalSigs(signals, want) {
		t.Errorf("signals = %v, want %v (pane pid only — claude was never identified)", signals, want)
	}
	if strings.Contains(out, "target pid") {
		t.Errorf("a target pid was reported although claude was never identified:\n%s", out)
	}
	last := f.lastLine(out)
	if !strings.HasPrefix(last, "cleanup incomplete: ") || !strings.Contains(last, "pane pid 4100 still alive") {
		t.Errorf("last line = %q", last)
	}
}

func TestSelftest_BothPidsEscalateInOrder(t *testing.T) {
	f := newStFixture(t)
	f.clock.expireLong = true // registered, no reply ⇒ FAIL, then cleanup
	f.alive[stTargetPID] = true
	f.alive[stPanePID] = true
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	f.mu.Lock()
	signals := append([]sigCall(nil), f.signals...)
	f.mu.Unlock()
	want := []sigCall{
		{stTargetPID, syscall.SIGTERM}, {stTargetPID, syscall.SIGKILL},
		{stPanePID, syscall.SIGTERM}, {stPanePID, syscall.SIGKILL},
	}
	if !equalSigs(signals, want) {
		t.Errorf("signals = %v, want %v (claude first, then the pane wrapper)", signals, want)
	}
	last := f.lastLine(out)
	if !strings.Contains(last, "target pid 4242 still alive") || !strings.Contains(last, "pane pid 4100 still alive") {
		t.Errorf("last line = %q", last)
	}
	if strings.Index(last, "target pid") > strings.Index(last, "pane pid") {
		t.Errorf("target should be reported before the pane: %q", last)
	}
}

func TestSelftest_TargetDiesAfterSIGTERM(t *testing.T) {
	f := newStFixture(t)
	f.clock.expireLong = true
	f.alive[stTargetPID] = true
	origSignal := f.deps.signal
	f.deps.signal = func(pid int, sig os.Signal) error {
		err := origSignal(pid, sig)
		f.mu.Lock()
		f.alive[pid] = false
		f.mu.Unlock()
		return err
	}
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1 (the selftest failed even though cleanup succeeded)", code)
	}
	f.mu.Lock()
	signals := append([]sigCall(nil), f.signals...)
	f.mu.Unlock()
	if want := []sigCall{{stTargetPID, syscall.SIGTERM}}; !equalSigs(signals, want) {
		t.Errorf("signals = %v, want %v", signals, want)
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

func TestSelftest_PIDReusedIsNotSignalled(t *testing.T) {
	f := newStFixture(t)
	f.clock.expireLong = true
	f.alive[stTargetPID] = true
	f.alive[stPanePID] = true
	// After kill-session both pids are held by other processes.
	f.killSession = func() ([]byte, error) {
		f.mu.Lock()
		f.starts[stTargetPID] = stForeignStart
		f.starts[stPanePID] = stForeignStart
		f.mu.Unlock()
		return nil, nil
	}
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	f.mu.Lock()
	signals := append([]sigCall(nil), f.signals...)
	f.mu.Unlock()
	if len(signals) != 0 {
		t.Errorf("a reused pid was signalled: %v", signals)
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

// TestSelftest_IdentityUnknownWhileAlive_LeftRunning pins the tri-state
// identity (as sweep.go's): a live pid whose start time cannot be read is
// neither ours nor another's — it is not signalled, its registry files
// and socket are kept, and cleanup reports it and is incomplete.
func TestSelftest_IdentityUnknownWhileAlive_LeftRunning(t *testing.T) {
	f := newStFixture(t)
	f.clock.expireLong = true // registered, no reply ⇒ FAIL, then cleanup
	f.alive[stTargetPID] = true
	ownJSON := filepath.Join(f.registryDir, "4242.json")
	writeRegistryFile(t, ownJSON, stTargetProcStart)
	if err := os.MkdirAll(f.sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inbox, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// After kill-session (identity already captured) ps stops answering
	// for the target while it is still alive.
	f.killSession = func() ([]byte, error) {
		f.mu.Lock()
		delete(f.starts, stTargetPID)
		f.mu.Unlock()
		return nil, nil
	}

	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	f.mu.Lock()
	signals := append([]sigCall(nil), f.signals...)
	f.mu.Unlock()
	if len(signals) != 0 {
		t.Errorf("a pid of unknown identity was signalled: %v", signals)
	}
	if !proxyhelpertest.Exists(ownJSON) || !proxyhelpertest.Exists(f.inbox) {
		t.Errorf("files of a process of unknown identity were removed: json %v sock %v", proxyhelpertest.Exists(ownJSON), proxyhelpertest.Exists(f.inbox))
	}
	if !strings.Contains(out, "target pid 4242: identity unknown, left running\n") {
		t.Errorf("stdout lacks the identity-unknown line:\n%s", out)
	}
	last := f.lastLine(out)
	if !strings.HasPrefix(last, "cleanup incomplete: ") || !strings.Contains(last, "target pid 4242: identity unknown, left running") {
		t.Errorf("last line = %q, want cleanup incomplete naming the target", last)
	}
}

// TestSelftest_ProcStartErrorWhileDead_IsGone pins the other half: a pid
// that is not alive is gone whatever ps says, as before.
func TestSelftest_ProcStartErrorWhileDead_IsGone(t *testing.T) {
	f := newStFixture(t)
	f.clock.expireLong = true
	f.alive[stTargetPID] = true
	ownJSON := filepath.Join(f.registryDir, "4242.json")
	writeRegistryFile(t, ownJSON, stTargetProcStart)
	f.killSession = func() ([]byte, error) {
		f.mu.Lock()
		f.alive[stTargetPID] = false
		delete(f.starts, stTargetPID)
		f.mu.Unlock()
		return nil, nil
	}

	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1 (the selftest itself failed)", code)
	}
	f.mu.Lock()
	signals := append([]sigCall(nil), f.signals...)
	f.mu.Unlock()
	if len(signals) != 0 {
		t.Errorf("a dead pid was signalled: %v", signals)
	}
	if proxyhelpertest.Exists(ownJSON) {
		t.Errorf("own registry file of the dead process left behind")
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

func equalSigs(a, b []sigCall) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSelftest_SpawnFailure(t *testing.T) {
	f := newStFixture(t)
	f.spawnErr = fmt.Errorf("proxyhelper: %w: no ready line within 3s", proxyhelper.ErrNotReady)
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "FAIL: helper did not start: proxyhelper: helper did not become ready: no ready line within 3s\n") {
		t.Errorf("stdout:\n%s", out)
	}
	if got := f.calls("kill-session"); len(got) != 1 {
		t.Errorf("kill-session calls = %d, want 1", len(got))
	}
	if f.peer.Stops() != 0 {
		t.Errorf("Stop called on a helper that never started")
	}
	f.mu.Lock()
	writes := len(f.writes)
	removed := append([]string(nil), f.removed...)
	f.mu.Unlock()
	if writes != 0 {
		t.Errorf("frame written without a helper")
	}
	for _, p := range removed {
		if strings.Contains(p, "900001") {
			t.Errorf("probe cleanup attempted without a helper: removed %s", p)
		}
	}
	if !strings.Contains(out, "cleanup: ok") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestSelftest_SpawnConfig(t *testing.T) {
	f := newStFixture(t)
	f.deps.readPeerFeatures = func(dir string, pid int) ([]string, bool) {
		if dir != f.registryDir || pid != stTargetPID {
			t.Errorf("readPeerFeatures(%q, %d)", dir, pid)
		}
		return []string{"notify_idle", "extra_feature"}, true
	}
	f.onWrite = func() { f.peer.frames <- f.replyFromTarget("PONG " + f.writtenNonce()) }
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 0 {
		t.Fatalf("exit = %d, stdout:\n%s", code, out)
	}
	cfg := f.spawnCfg
	if cfg.Name != "pdx-selftest-probe" || cfg.RegistryDir != f.registryDir || cfg.SockDir != f.sockDir ||
		cfg.Version != ccuds.VerifiedCCVersion || cfg.Cwd != "/tmp" {
		t.Errorf("spawn cfg = %+v", cfg)
	}
	if len(cfg.SessionID) != 36 || strings.Count(cfg.SessionID, "-") != 4 {
		t.Errorf("SessionID = %q, want a UUID", cfg.SessionID)
	}
	if !equalArgs(cfg.PeerFeatures, []string{"notify_idle", "extra_feature"}) {
		t.Errorf("PeerFeatures = %v, want the target's", cfg.PeerFeatures)
	}
}

func TestSelftest_SpawnConfigDefaultFeatures(t *testing.T) {
	f := newStFixture(t)
	f.onWrite = func() { f.peer.frames <- f.replyFromTarget("PONG " + f.writtenNonce()) }
	if code, out, _ := f.run(context.Background(), 5*time.Second); code != 0 {
		t.Fatalf("exit = %d, stdout:\n%s", code, out)
	}
	if !equalArgs(f.spawnCfg.PeerFeatures, ccuds.DefaultPeerFeatures) {
		t.Errorf("PeerFeatures = %v, want DefaultPeerFeatures", f.spawnCfg.PeerFeatures)
	}
}

// --- step 5: the probe frame ------------------------------------------------

func TestSelftest_WrittenFrame(t *testing.T) {
	f := newStFixture(t)
	f.onWrite = func() { f.peer.frames <- f.replyFromTarget("PONG " + f.writtenNonce()) }
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 0 {
		t.Fatalf("exit = %d, stdout:\n%s", code, out)
	}
	f.mu.Lock()
	w := f.writes[0]
	f.mu.Unlock()
	if w.sock != f.inbox {
		t.Errorf("written to %q, want %q", w.sock, f.inbox)
	}
	fr, err := ccuds.ParseFrame(w.line)
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	if fr.Type != "user" || fr.MsgV != 1 || fr.From != "uds:"+f.peer.sock {
		t.Errorf("frame = %+v, want type user, msgV 1, from uds:%s", fr, f.peer.sock)
	}
	if len(fr.MsgID) != 36 {
		t.Errorf("msg_id = %q, want a UUID", fr.MsgID)
	}
	wr, ok := ccuds.Parse(fr.Message.Content)
	if !ok {
		t.Fatalf("content is not a wrapper: %q", fr.Message.Content)
	}
	if wr.From != "uds:"+f.peer.sock || wr.FromName != "pdx-selftest-probe" || wr.FromMode != ipeers.ModePrompting {
		t.Errorf("wrapper = %+v", wr)
	}
	nonce := f.writtenNonce()
	if len(nonce) != 8 {
		t.Errorf("nonce = %q, want 8 hex", nonce)
	}
	if want := "PDX_SELFTEST " + nonce + ": reply with exactly: PONG " + nonce; wr.Text != want {
		t.Errorf("text = %q, want %q", wr.Text, want)
	}
}

func TestSelftest_WriteFailure(t *testing.T) {
	f := newStFixture(t)
	f.writeErr = errors.New("dial unix: connection refused")
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "FAIL: write to "+f.inbox+": dial unix: connection refused\n") {
		t.Errorf("stdout:\n%s", out)
	}
	if f.peer.Stops() != 1 {
		t.Errorf("helper Stop calls = %d, want 1", f.peer.Stops())
	}
	if got := f.calls("kill-session"); len(got) != 1 {
		t.Errorf("kill-session calls = %d, want 1", len(got))
	}
}

// --- step 6: the reply wait -------------------------------------------------

func TestSelftest_ReplyWithNonce_PASS(t *testing.T) {
	f := newStFixture(t)
	f.onWrite = func() { f.peer.frames <- f.replyFromTarget("PONG " + f.writtenNonce()) }
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 0 {
		t.Fatalf("exit = %d, stdout:\n%s", code, out)
	}
	want := "PASS: reply from " + f.sessionName() + " via helper pid 900001 in "
	if !strings.Contains(out, want) {
		t.Errorf("stdout:\n%s\nwant a line starting %q", out, want)
	}
	if f.peer.Stops() != 1 {
		t.Errorf("helper Stop calls = %d, want 1", f.peer.Stops())
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

func TestSelftest_FrameFromAnotherSocketIgnored(t *testing.T) {
	f := newStFixture(t)
	f.onWrite = func() {
		nonce := f.writtenNonce()
		other, _ := ccuds.BuildFrame("x", "/tmp/other.sock", ccuds.Wrapper{
			From: "uds:/tmp/other.sock", FromName: "stranger", FromMode: ipeers.ModePrompting, Text: "PONG " + nonce,
		})
		f.peer.frames <- strings.TrimSuffix(string(other), "\n")
		f.peer.frames <- "not json at all"
		f.peer.frames <- f.replyFromTarget("thinking about it")
		f.peer.frames <- f.replyFromTarget("PONG " + nonce)
	}
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 0 {
		t.Fatalf("exit = %d, stdout:\n%s", code, out)
	}
	if !strings.Contains(out, "note: ") {
		t.Errorf("a target frame without the nonce should produce a note line:\n%s", out)
	}
	if !strings.Contains(out, "PASS: reply from ") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestSelftest_FramesClosed_HelperExited(t *testing.T) {
	f := newStFixture(t)
	f.onWrite = func() { close(f.peer.frames) }
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "FAIL: helper exited\n") {
		t.Errorf("stdout:\n%s", out)
	}
	if f.peer.Stops() != 1 {
		t.Errorf("helper Stop calls = %d, want 1 (cleanup still stops it)", f.peer.Stops())
	}
}

// TestSelftest_FramesClosedAfterInterrupt: the run ctx also owns the real
// helper process, so a Ctrl-C closes Frames — that must read as
// `interrupted`, never as `helper exited`.
func TestSelftest_FramesClosedAfterInterrupt(t *testing.T) {
	// Both ctx.Done() and the closed Frames are ready at once; whichever
	// case select picks, the verdict must be `interrupted` — repeated so
	// both arms are exercised.
	for i := 0; i < 20; i++ {
		f := newStFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		f.onWrite = func() {
			cancel()
			close(f.peer.frames)
		}
		code, out, _ := f.run(ctx, 5*time.Second)
		cancel()
		if code != 1 || !strings.Contains(out, "FAIL: interrupted\n") || strings.Contains(out, "helper exited") {
			t.Fatalf("run %d: exit = %d, stdout:\n%s", i, code, out)
		}
	}
}

func TestSelftest_Timeout(t *testing.T) {
	f := newStFixture(t)
	f.clock.expireLong = true
	// 90 s: time.Duration's own format would say "1m30s"; the golden is
	// whole seconds so the default reads "60s".
	code, out, _ := f.run(context.Background(), 90*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "FAIL: no reply within 90s\n") {
		t.Errorf("stdout:\n%s", out)
	}
	if f.peer.Stops() != 1 {
		t.Errorf("helper Stop calls = %d, want 1", f.peer.Stops())
	}
	if got := f.calls("kill-session"); len(got) != 1 {
		t.Errorf("kill-session calls = %d, want 1", len(got))
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

func TestSelftest_Interrupted_CleanupRunsUnderOwnCtx(t *testing.T) {
	f := newStFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.alive[stTargetPID] = true
	f.onWrite = func() { cancel() }
	code, out, _ := f.run(ctx, 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "FAIL: interrupted\n") {
		t.Errorf("stdout:\n%s", out)
	}
	kills := f.calls("kill-session")
	if len(kills) != 1 {
		t.Fatalf("kill-session calls = %d, want 1", len(kills))
	}
	if kills[0].ctxErr != nil {
		t.Errorf("kill-session ran under a cancelled ctx: %v", kills[0].ctxErr)
	}
	if f.peer.Stops() != 1 {
		t.Errorf("helper Stop calls = %d, want 1", f.peer.Stops())
	}
	// The pid wait ran to completion under the cleanup ctx: both signals
	// were sent even though the run ctx was cancelled.
	f.mu.Lock()
	signals := append([]sigCall(nil), f.signals...)
	f.mu.Unlock()
	if want := []sigCall{{stTargetPID, syscall.SIGTERM}, {stTargetPID, syscall.SIGKILL}}; !equalSigs(signals, want) {
		t.Errorf("signals = %v, want %v", signals, want)
	}
	if !strings.HasPrefix(f.lastLine(out), "cleanup incomplete: ") {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

func TestSelftest_InterruptedWhilePolling(t *testing.T) {
	f := newStFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.entries = func(n int) []ipeers.Entry {
		if n == 2 {
			cancel()
		}
		return nil
	}
	code, out, _ := f.run(ctx, 5*time.Second)
	if code != 1 || !strings.Contains(out, "FAIL: interrupted\n") {
		t.Errorf("exit = %d, stdout:\n%s", code, out)
	}
	if got := f.calls("kill-session"); len(got) != 1 || got[0].ctxErr != nil {
		t.Errorf("kill-session = %v", got)
	}
}

// --- step 7: cleanup --------------------------------------------------------

func TestSelftest_Cleanup_TargetFilesByProcStart(t *testing.T) {
	f := newStFixture(t)
	f.clock.expireLong = true // no reply ⇒ FAIL, then cleanup
	ownJSON := filepath.Join(f.registryDir, "4242.json")
	ownKey := filepath.Join(f.registryDir, "4242.aaaa.key")
	foreignKey := filepath.Join(f.registryDir, "4242.bbbb.key")
	writeRegistryFile(t, ownJSON, stTargetProcStart)
	writeRegistryFile(t, ownKey, stTargetProcStart)
	writeRegistryFile(t, foreignKey, stForeignStart)
	if err := os.MkdirAll(f.sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inbox, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if proxyhelpertest.Exists(ownJSON) || proxyhelpertest.Exists(ownKey) {
		t.Errorf("own registry files left behind")
	}
	if !proxyhelpertest.Exists(foreignKey) {
		t.Errorf("foreign key file removed")
	}
	if proxyhelpertest.Exists(f.inbox) {
		t.Errorf("dead target socket left behind")
	}
	if !strings.Contains(out, "foreign file kept: "+foreignKey+"\n") {
		t.Errorf("stdout:\n%s", out)
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

func TestSelftest_Cleanup_ForeignJSONKept(t *testing.T) {
	f := newStFixture(t)
	f.clock.expireLong = true
	foreignJSON := filepath.Join(f.registryDir, "4242.json")
	writeRegistryFile(t, foreignJSON, stForeignStart)
	_, out, _ := f.run(context.Background(), 5*time.Second)
	if !proxyhelpertest.Exists(foreignJSON) {
		t.Errorf("foreign json removed")
	}
	if !strings.Contains(out, "foreign file kept: "+foreignJSON+"\n") {
		t.Errorf("stdout:\n%s", out)
	}
}

func TestSelftest_Cleanup_LiveSocketKept(t *testing.T) {
	f := newStFixture(t)
	f.clock.expireLong = true
	f.dialRefused = func(string) bool { return false }
	if err := os.MkdirAll(f.sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inbox, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.run(context.Background(), 5*time.Second)
	if !proxyhelpertest.Exists(f.inbox) {
		t.Errorf("a socket somebody listens on was unlinked")
	}
}

// TestSelftest_Cleanup_RegisteredInboxIsAuthoritative: the registry's
// messagingSocketPath — not a path constructed from sockDir — is what
// cleanup probes and unlinks once the session registered.
func TestSelftest_Cleanup_RegisteredInboxIsAuthoritative(t *testing.T) {
	f := newStFixture(t)
	f.clock.expireLong = true
	otherDir := filepath.Join(filepath.Dir(f.sockDir), "elsewhere")
	if err := os.MkdirAll(otherDir, 0o700); err != nil {
		t.Fatal(err)
	}
	f.inbox = filepath.Join(otherDir, "4242.sock") // targetEntry() reads f.inbox
	if err := os.WriteFile(f.inbox, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	constructed := filepath.Join(f.sockDir, "4242.sock")
	if err := os.MkdirAll(f.sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(constructed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var probed []string
	f.dialRefused = func(sock string) bool { probed = append(probed, sock); return true }

	_, out, _ := f.run(context.Background(), 5*time.Second)
	if proxyhelpertest.Exists(f.inbox) {
		t.Errorf("registered inbox %s not removed", f.inbox)
	}
	if !proxyhelpertest.Exists(constructed) {
		t.Errorf("constructed %s removed although the registry said otherwise", constructed)
	}
	for _, p := range probed {
		if p == constructed {
			t.Errorf("constructed path probed: %v", probed)
		}
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

// TestSelftest_Cleanup_UnregisteredTouchesNoRegistryFiles: without a
// registry entry the claude pid is unknown, so nothing under registryDir
// or sockDir can be proven ours — the pane pid's namesakes are left alone.
func TestSelftest_Cleanup_UnregisteredTouchesNoRegistryFiles(t *testing.T) {
	f := newStFixture(t)
	f.entries = func(int) []ipeers.Entry { return nil }
	paneJSON := filepath.Join(f.registryDir, strconv.Itoa(stPanePID)+".json")
	writeRegistryFile(t, paneJSON, stPaneProcStart)
	paneSock := filepath.Join(f.sockDir, strconv.Itoa(stPanePID)+".sock")
	if err := os.MkdirAll(f.sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paneSock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, out, _ := f.run(context.Background(), 5*time.Second)
	if !proxyhelpertest.Exists(paneJSON) || !proxyhelpertest.Exists(paneSock) {
		t.Errorf("files named after the pane pid were removed although nothing proves them ours")
	}
	f.mu.Lock()
	removed := append([]string(nil), f.removed...)
	f.mu.Unlock()
	if len(removed) != 0 {
		t.Errorf("removed %v, want nothing", removed)
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

func TestSelftest_Cleanup_LeftoverProbeFilesRemoved(t *testing.T) {
	f := newStFixture(t)
	f.clock.expireLong = true
	if err := os.MkdirAll(f.sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The helper's Stop "forgot" its files (a SIGKILLed real helper does).
	for _, p := range append([]string{f.peer.sock}, f.peer.files...) {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1 (FAIL: no reply)", code)
	}
	for _, p := range append([]string{f.peer.sock}, f.peer.files...) {
		if proxyhelpertest.Exists(p) {
			t.Errorf("probe leftover %s not removed", p)
		}
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q\nstdout:\n%s", f.lastLine(out), out)
	}
}

func TestSelftest_Cleanup_RemoveErrorIsIncomplete(t *testing.T) {
	f := newStFixture(t)
	f.onWrite = func() { f.peer.frames <- f.replyFromTarget("PONG " + f.writtenNonce()) }
	ownJSON := filepath.Join(f.registryDir, "4242.json")
	writeRegistryFile(t, ownJSON, stTargetProcStart)
	f.removeErr = func(path string) error {
		if path == ownJSON {
			return &os.PathError{Op: "remove", Path: path, Err: syscall.EACCES}
		}
		return nil
	}
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if !strings.Contains(out, "PASS: reply from ") {
		t.Errorf("stdout:\n%s", out)
	}
	if code != 1 {
		t.Errorf("exit = %d, want 1 (cleanup incomplete overrides PASS)", code)
	}
	last := f.lastLine(out)
	if !strings.HasPrefix(last, "cleanup incomplete: ") || !strings.Contains(last, ownJSON) {
		t.Errorf("last line = %q", last)
	}
}

func TestSelftest_Cleanup_KillSessionNoSuchSessionIsFine(t *testing.T) {
	f := newStFixture(t)
	f.onWrite = func() { f.peer.frames <- f.replyFromTarget("PONG " + f.writtenNonce()) }
	f.killSession = func() ([]byte, error) {
		return nil, &exec.ExitError{Stderr: []byte("can't find session: pdx-selftest-abc123\n")}
	}
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 0 {
		t.Errorf("exit = %d, want 0\n%s", code, out)
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

func TestSelftest_Cleanup_KillSessionOtherErrorIsIncomplete(t *testing.T) {
	f := newStFixture(t)
	f.onWrite = func() { f.peer.frames <- f.replyFromTarget("PONG " + f.writtenNonce()) }
	f.killSession = func() ([]byte, error) {
		return nil, &exec.ExitError{Stderr: []byte("error connecting to /tmp/tmux-501/default (Permission denied)\n")}
	}
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1\n%s", code, out)
	}
	last := f.lastLine(out)
	if !strings.HasPrefix(last, "cleanup incomplete: ") || !strings.Contains(last, "kill-session") {
		t.Errorf("last line = %q", last)
	}
}

// --- through a real (in-process) helper -------------------------------------

// TestSelftest_RealHelperRoundTrip backs the spawn seam with
// proxyhelpertest's faithful helper: the probe frame's from address is
// the helper's real socket, and a reply written to that socket reaches
// the selftest through Handle.Frames.
func TestSelftest_RealHelperRoundTrip(t *testing.T) {
	f := newStFixture(t)
	fake := proxyhelpertest.New(proxyhelpertest.Options{})
	var h proxyhelper.Handle
	f.deps.spawn = func(ctx context.Context, cfg proxyhelper.Config) (selftestPeer, error) {
		var err error
		h, err = proxyhelper.Spawn(ctx, fake.Starter(), cfg, 2*time.Second)
		if err != nil {
			return nil, err
		}
		return h, nil
	}
	f.onWrite = func() {
		f.mu.Lock()
		line := f.writes[0].line
		f.mu.Unlock()
		fr, err := ccuds.ParseFrame(line)
		if err != nil {
			t.Errorf("frame: %v", err)
			return
		}
		sock, ok := ccuds.FromSocket(fr.From)
		if !ok || sock != h.Sock() {
			t.Errorf("from = %q, want uds:%s", fr.From, h.Sock())
			return
		}
		proxyhelpertest.WriteToSock(t, sock, f.replyFromTarget("PONG "+f.writtenNonce()))
	}
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 0 {
		t.Fatalf("exit = %d, stdout:\n%s", code, out)
	}
	if !strings.Contains(out, "PASS: reply from "+f.sessionName()+" via helper pid "+strconv.Itoa(h.PID())+" in ") {
		t.Errorf("stdout:\n%s", out)
	}
	for _, p := range append([]string{h.Sock()}, h.Files()...) {
		if proxyhelpertest.Exists(p) {
			t.Errorf("%s left behind after cleanup", p)
		}
	}
	if fake.Stops() != 1 {
		t.Errorf("helper Stops = %d, want 1", fake.Stops())
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

// --- the verb wrapper -------------------------------------------------------

func TestSelftestTimeout(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
		ok   bool
	}{
		{"", 60 * time.Second, true},
		{"30s", 30 * time.Second, true},
		{"2m", 2 * time.Minute, true},
		{"0", 0, false},
		{"-1s", 0, false},
		{"abc", 0, false},
	}
	for _, c := range cases {
		got, err := selftestTimeout(c.raw)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("selftestTimeout(%q) = %v, %v; want %v, ok=%v", c.raw, got, err, c.want, c.ok)
		}
	}
}

func TestSelftestFormatTimeout(t *testing.T) {
	cases := map[time.Duration]string{
		60 * time.Second:        "60s",
		90 * time.Second:        "90s",
		7 * time.Second:         "7s",
		1500 * time.Millisecond: "1.5s",
		500 * time.Millisecond:  "500ms",
	}
	for d, want := range cases {
		if got := selftestFormatTimeout(d); got != want {
			t.Errorf("selftestFormatTimeout(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestSelftest_ProductionDepsAreWired(t *testing.T) {
	var stderr bytes.Buffer
	d, err := newSelftestDeps(&stderr)
	if err != nil {
		t.Fatalf("newSelftestDeps: %v", err)
	}
	if d.tmux == nil || d.readRegistry == nil || d.spawn == nil || d.writeFrame == nil ||
		d.pidAlive == nil || d.procStart == nil || d.signal == nil || d.readPeerFeatures == nil ||
		d.glob == nil || d.registryProcStart == nil || d.remove == nil || d.dialRefused == nil ||
		d.sleep == nil || d.now == nil {
		t.Fatalf("a production seam is nil: %+v", d)
	}
	if d.sockDir != ccuds.DefaultSockDir {
		t.Errorf("sockDir = %q", d.sockDir)
	}
	if !strings.HasSuffix(d.registryDir, filepath.Join(".claude", "sessions")) {
		t.Errorf("registryDir = %q", d.registryDir)
	}
	// The real sleep honours ctx.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("sleep(cancelled) = %v", err)
	}
	if err := d.sleep(context.Background(), time.Millisecond); err != nil {
		t.Errorf("sleep(1ms) = %v", err)
	}
}
