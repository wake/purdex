package modeventsmod

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/modevents"
)

// shortDir is a 0700 directory short enough for a socket path.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pdxm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *logs) has(line string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, x := range l.lines {
		if x == line {
			return true
		}
	}
	return false
}

// started inits and starts a module on dataDir and returns it with its core.
func started(t *testing.T, dataDir string) (*Module, *core.Core, *logs) {
	t.Helper()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dataDir}})
	m := New()
	lg := &logs{}
	m.logf = lg.logf
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start must never fail the daemon: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m, c, lg
}

func registry(t *testing.T, c *core.Core) *modevents.Registry {
	t.Helper()
	v, ok := c.Registry.Get(ServiceName)
	if !ok {
		t.Fatal("the registry is not in the ServiceRegistry")
	}
	reg, ok := v.(*modevents.Registry)
	if !ok {
		t.Fatalf("ServiceRegistry %q holds %T", ServiceName, v)
	}
	return reg
}

func postBatch(path string, seq int64) (int, string, error) {
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}}
	defer c.CloseIdleConnections()
	body := fmt.Sprintf(`{"v":1,"stream":"streamAAA","agent":"cc","cc_version":"2.1.293","mod_version":"x","dropped_total":0,`+
		`"events":[{"seq":%d,"at":1,"sid":"11111111-1111-4111-8111-111111111111","type":"heartbeat","data":{}}]}`, seq)
	res, err := c.Post("http://pdx/mod/v1/events", "application/json", strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), nil
}

func gone(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist)
}

// resolvedSock is the socket path in dir with dir's symlinks resolved
// (/tmp is one on macOS): where the module binds.
func resolvedSock(t *testing.T, dir string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(r, modevents.SocketName)
}

func TestModule_StartServesAndStopUnlinks(t *testing.T) {
	dir := shortDir(t)
	m, c, lg := started(t, dir)
	path := resolvedSock(t, dir)
	if st := m.Status(); !st.Enabled || m.SocketPathForInfo() != path {
		t.Fatalf("status = %+v, path = %q", st, m.SocketPathForInfo())
	}
	if !lg.has("[modevents] socket " + path) {
		t.Fatalf("logs = %q", lg.lines)
	}
	if m.Name() != "modevents" || len(m.Dependencies()) != 0 {
		t.Fatal("name modevents, no dependencies")
	}
	code, body, err := postBatch(path, 1)
	if err != nil || code != http.StatusOK || body != `{"ack":1}` {
		t.Fatalf("post: %d %s %v", code, body, err)
	}
	if evs, ok := registry(t, c).Events("streamAAA", 0); !ok || len(evs) != 1 {
		t.Fatal("the batch must land in the published registry")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !gone(path) {
		t.Fatal("Stop must unlink the socket")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("a second Stop must be a no-op: %v", err)
	}
}

// #2420: Stop wakes the mods' parked long polls before it shuts the server down, so a restart does not wait their wait out.
// Mutation gate: Stop does not close stopPolls → red.
func TestModule_StopWakesTheLongPolls(t *testing.T) {
	dir := shortDir(t)
	m, _, _ := started(t, dir)
	select {
	case <-m.stopPolls:
		t.Fatal("closed before Stop")
	default:
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.stopPolls:
	default:
		t.Fatal("Stop did not wake the long polls")
	}
	if err := m.Stop(context.Background()); err != nil { // a second Stop must not close it again (panic)
		t.Fatal(err)
	}
}

func TestModule_StopUnlinksEvenWhenShutdownTimesOut(t *testing.T) {
	dir := shortDir(t)
	m, c, _ := started(t, dir)
	path := m.SocketPathForInfo()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	registry(t, c).Subscribe(func(modevents.StreamInfo, modevents.Event) {
		once.Do(func() { close(entered) })
		<-release
	})
	t.Cleanup(func() { close(release) })
	go func() { _, _, _ = postBatch(path, 1) }() // its handler blocks in the subscriber
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the subscriber")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the shared shutdown budget is already spent
	done := make(chan error, 1)
	go func() { done <- m.Stop(ctx) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop must return when Shutdown hits its deadline")
	}
	if !gone(path) {
		t.Fatal("the socket must be gone when Stop returns")
	}
	l, st := modevents.Listen(path)
	if !st.Enabled {
		t.Fatalf("the next daemon must be able to bind: %+v", st)
	}
	l.Close()
}

// Attacker high: a second Stop racing the first must not return while the
// first is still cleaning up — the caller would go on (exec-self, exit)
// with the server still running.
func TestModule_ConcurrentStopWaitsForCleanup(t *testing.T) {
	m, c, _ := started(t, shortDir(t))
	path := m.SocketPathForInfo()
	entered := make(chan struct{})
	release := make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseHandler) // before the Stop cleanup, which would block
	registry(t, c).Subscribe(func(modevents.StreamInfo, modevents.Event) {
		enterOnce.Do(func() { close(entered) })
		<-release
	})
	go func() { _, _, _ = postBatch(path, 1) }() // its handler blocks in the subscriber
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the subscriber")
	}

	// Stop #1, with a live budget, closes the listener (unlinking the
	// socket) and then waits in Shutdown for the blocked handler.
	done1 := make(chan error, 1)
	go func() { done1 <- m.Stop(context.Background()) }()
	for deadline := time.Now().Add(5 * time.Second); !gone(path); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("Stop #1 never closed the listener")
		}
	}

	done2 := make(chan error, 1)
	go func() { done2 <- m.Stop(context.Background()) }()
	select {
	case err := <-done2:
		t.Fatalf("Stop #2 returned (%v) while Stop #1 is still shutting down", err)
	case err := <-done1:
		t.Fatalf("Stop #1 returned (%v) with a handler still running", err)
	case <-time.After(200 * time.Millisecond):
	}

	releaseHandler()
	for i, done := range []chan error{done1, done2} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Stop #%d: %v", i+1, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Stop #%d never returned", i+1)
		}
		if n := m.running.Load(); n != 0 {
			t.Fatalf("Stop #%d returned with %d goroutines still running", i+1, n)
		}
	}
	if !gone(path) {
		t.Fatal("the socket must be gone")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("a later Stop returns the same result: %v", err)
	}
}

// Codex P1: a later Stop whose budget has run out must not wait forever
// behind a first Stop that has a long budget and a hung handler; it forces
// the server closed (plan Task A4), which lets the first Stop return too.
func TestModule_SecondStopWithExpiredContextForcesClose(t *testing.T) {
	m, c, _ := started(t, shortDir(t))
	path := m.SocketPathForInfo()
	entered := make(chan struct{})
	release := make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	releaseHandler := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseHandler) // before the Stop cleanup, which would block
	registry(t, c).Subscribe(func(modevents.StreamInfo, modevents.Event) {
		enterOnce.Do(func() { close(entered) })
		<-release
	})
	go func() { _, _, _ = postBatch(path, 1) }() // its handler blocks in the subscriber
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never reached the subscriber")
	}

	// Stop #1 has no deadline, so its Shutdown waits for the hung handler.
	done1 := make(chan error, 1)
	go func() { done1 <- m.Stop(context.Background()) }()
	for deadline := time.Now().Add(5 * time.Second); !gone(path); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("Stop #1 never closed the listener")
		}
	}

	// Stop #2's shared budget is already spent.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done2 := make(chan error, 1)
	go func() { done2 <- m.Stop(ctx) }()
	for i, done := range []chan error{done2, done1} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Stop #%d: %v", 2-i, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Stop #%d did not return: an expired Stop must force the server closed", 2-i)
		}
	}
	if n := m.running.Load(); n != 0 {
		t.Fatalf("Stop returned with %d goroutines still running", n)
	}
	if !gone(path) {
		t.Fatal("the socket must be gone")
	}
}

// The module reports the path it actually binds at: the data dir's
// symlinks resolved, as Listen does and as pdx.json names it.
func TestModule_SocketPathIsResolved(t *testing.T) {
	target := shortDir(t)
	link := filepath.Join(shortDir(t), "data")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	m, _, lg := started(t, link)
	want := resolvedSock(t, target)
	if got := m.SocketPathForInfo(); got != want {
		t.Fatalf("SocketPathForInfo = %q, want the resolved %q", got, want)
	}
	if !m.Status().Enabled || !lg.has("[modevents] socket "+want) {
		t.Fatalf("status = %+v, logs = %q", m.Status(), lg.lines)
	}
	if code, body, err := postBatch(want, 1); err != nil || body != `{"ack":1}` {
		t.Fatalf("post: %d %s %v", code, body, err)
	}
}

func TestModule_StopJoinsGoroutines(t *testing.T) {
	m, _, _ := started(t, shortDir(t))
	if n := m.running.Load(); n != 2 {
		t.Fatalf("%d goroutines after Start, want 2 (serve, eviction ticker)", n)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := m.running.Load(); n != 0 {
		t.Fatalf("%d goroutines still running after Stop", n)
	}
}

func TestModule_RestartRebinds(t *testing.T) {
	dir := shortDir(t)
	m1, _, _ := started(t, dir)
	if err := m1.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	m2, _, _ := started(t, dir)
	if !m2.Status().Enabled {
		t.Fatalf("restarted module: %+v", m2.Status())
	}
	if code, body, err := postBatch(m2.SocketPathForInfo(), 7); err != nil || body != `{"ack":7}` {
		t.Fatalf("post after restart: %d %s %v", code, body, err)
	}
}

func TestModule_DisabledPathDoesNotFail(t *testing.T) {
	long := "/tmp/" + strings.Repeat("d", 100)
	m, c, lg := started(t, long)
	if st := m.Status(); st.Enabled || st.Reason != modevents.ReasonPathTooLong {
		t.Fatalf("status = %+v", st)
	}
	if !lg.has("[modevents] disabled: " + modevents.ReasonPathTooLong) {
		t.Fatalf("logs = %q", lg.lines)
	}
	registry(t, c) // published even when the socket is off
	if m.running.Load() != 0 {
		t.Fatal("nothing runs when disabled")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	// An unsafe data dir disables the channel the same way.
	dir := shortDir(t)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	m2, _, _ := started(t, dir)
	if st := m2.Status(); st.Enabled || st.Reason != modevents.ReasonUnsafeDir {
		t.Fatalf("status = %+v", st)
	}
}
