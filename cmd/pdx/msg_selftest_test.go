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
	stTargetPID       = 4242
	stTargetProcStart = "Mon Sep 14 01:02:03 2026"
	stForeignStart    = "Mon Jan  1 00:00:00 2001"
)

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
	signals     []os.Signal
	writes      []stWrite
	removed     []string
	pidAlive    bool
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
		pidAlive:    false,
	}
	f.peer = &fakePeer{
		pid:    900001,
		sock:   filepath.Join(sockDir, "900001.sock"),
		files:  []string{filepath.Join(registryDir, "900001.json"), filepath.Join(registryDir, "900001.abcd.key")},
		frames: make(chan string, 16),
	}
	f.listPanes = func() ([]byte, error) { return []byte(strconv.Itoa(stTargetPID) + "\n"), nil }
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
			return pid == stTargetPID && f.pidAlive
		},
		procStart: func(pid int) (string, error) {
			if pid == stTargetPID {
				return stTargetProcStart, nil
			}
			return "", fmt.Errorf("ps -p %d: no such process", pid)
		},
		signal: func(pid int, sig os.Signal) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if pid != stTargetPID {
				t.Errorf("signal sent to pid %d, want %d", pid, stTargetPID)
			}
			f.signals = append(f.signals, sig)
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

	wantNew := []string{"new-session", "-d", "-s", name, "--", "claude", "-p", "--verbose",
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
	if got := f.calls("kill-session"); len(got) != 0 {
		t.Errorf("kill-session called for a session that never started: %v", got)
	}
	if f.spawned {
		t.Errorf("helper spawned after new-session failed")
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

func TestSelftest_EntryWithOtherPIDIgnored(t *testing.T) {
	f := newStFixture(t)
	f.entries = func(n int) []ipeers.Entry {
		other := f.targetEntry()
		other.PID = 9999
		other.Inbox = filepath.Join(f.sockDir, "9999.sock")
		if n < 2 {
			return []ipeers.Entry{other}
		}
		proxy := f.targetEntry()
		proxy.IsProxy = true
		return []ipeers.Entry{other, proxy, f.targetEntry()}
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
		t.Errorf("polls = %d, want 2 (the other-pid entry must not match)", polls)
	}
	if len(writes) != 1 || writes[0].sock != f.inbox {
		t.Errorf("frame written to %v, want the target inbox %s", writes, f.inbox)
	}
}

func TestSelftest_NeverRegisters_KillsAndSignals(t *testing.T) {
	f := newStFixture(t)
	f.entries = func(int) []ipeers.Entry { return nil }
	f.pidAlive = true // the claude process ignores kill-session
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
	signals := append([]os.Signal(nil), f.signals...)
	f.mu.Unlock()
	// 15 s at 250 ms: the first poll at t=0, then one per sleep.
	if polls < 60 || polls > 62 {
		t.Errorf("polls = %d, want ≈ 61 (15 s / 250 ms)", polls)
	}
	if len(signals) != 2 || signals[0] != syscall.SIGTERM || signals[1] != syscall.SIGKILL {
		t.Errorf("signals = %v, want [SIGTERM SIGKILL]", signals)
	}
	if !strings.Contains(out, "target pid 4242 still alive") {
		t.Errorf("stdout should report the surviving pid:\n%s", out)
	}
	last := f.lastLine(out)
	if !strings.HasPrefix(last, "cleanup incomplete: ") || !strings.Contains(last, "target pid 4242 still alive") {
		t.Errorf("last line = %q", last)
	}
}

func TestSelftest_TargetDiesAfterSIGTERM(t *testing.T) {
	f := newStFixture(t)
	f.entries = func(int) []ipeers.Entry { return nil }
	f.pidAlive = true
	origSignal := f.deps.signal
	f.deps.signal = func(pid int, sig os.Signal) error {
		err := origSignal(pid, sig)
		f.mu.Lock()
		f.pidAlive = false
		f.mu.Unlock()
		return err
	}
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1 (the selftest failed even though cleanup succeeded)", code)
	}
	f.mu.Lock()
	signals := append([]os.Signal(nil), f.signals...)
	f.mu.Unlock()
	if len(signals) != 1 || signals[0] != syscall.SIGTERM {
		t.Errorf("signals = %v, want [SIGTERM]", signals)
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

func TestSelftest_PIDReusedIsNotSignalled(t *testing.T) {
	f := newStFixture(t)
	f.entries = func(int) []ipeers.Entry { return nil }
	f.pidAlive = true
	f.deps.procStart = func(pid int) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.tmuxCalls) < 3 { // before kill-session: the real target
			return stTargetProcStart, nil
		}
		return stForeignStart, nil // after: another process holds the pid
	}
	code, out, _ := f.run(context.Background(), 5*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	f.mu.Lock()
	signals := append([]os.Signal(nil), f.signals...)
	f.mu.Unlock()
	if len(signals) != 0 {
		t.Errorf("a reused pid was signalled: %v", signals)
	}
	if f.lastLine(out) != "cleanup: ok" {
		t.Errorf("last line = %q", f.lastLine(out))
	}
}

// --- step 4: helper spawn ---------------------------------------------------

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

func TestSelftest_Timeout(t *testing.T) {
	f := newStFixture(t)
	f.clock.expireLong = true
	code, out, _ := f.run(context.Background(), 7*time.Second)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, "FAIL: no reply within 7s\n") {
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
	f.pidAlive = true
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
	signals := append([]os.Signal(nil), f.signals...)
	f.mu.Unlock()
	if len(signals) != 2 {
		t.Errorf("signals = %v, want [SIGTERM SIGKILL]", signals)
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

func TestSelftest_ProductionDepsAreWired(t *testing.T) {
	var stderr bytes.Buffer
	d := newSelftestDeps(&stderr)
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
