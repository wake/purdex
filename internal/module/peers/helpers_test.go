package peers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/peers/ccuds"
	"github.com/wake/purdex/internal/peers/proxyhelper"
	"github.com/wake/purdex/internal/peers/proxyhelper/proxyhelpertest"
)

// ---------------------------------------------------------------------------
// Scaffolding
// ---------------------------------------------------------------------------

// safeClock is a settable, goroutine-safe clock: limits_test.go's
// manualClock and module_test.go's fakeClock are unguarded, and the
// manager reads the clock from its own goroutines.
type safeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newSafeClock() *safeClock {
	return &safeClock{t: time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)}
}

func (c *safeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *safeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// sentSignal is one signal the manager asked the fake OS to deliver.
type sentSignal struct {
	pid int
	sig os.Signal
}

// fakeOS is the manager's view of the process table and the socket
// namespace: pidAlive / procStart / signal / dialRefused, all driven by
// maps. Pids not listed in ps fall back to proxyhelpertest.ProcStart so
// fake helper processes always have a start time.
type fakeOS struct {
	mu       sync.Mutex
	alive    map[int]bool
	ps       map[int]string
	psErr    map[int]error
	refused  map[string]bool // absent ⇒ true (nobody listens)
	signals  []sentSignal
	onSignal func(pid int, sig os.Signal)
}

func newFakeOS() *fakeOS {
	return &fakeOS{alive: map[int]bool{}, ps: map[int]string{}, psErr: map[int]error{}, refused: map[string]bool{}}
}

func (f *fakeOS) pidAlive(pid int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive[pid]
}

func (f *fakeOS) procStart(pid int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.psErr[pid]; err != nil {
		return "", err
	}
	if ps, ok := f.ps[pid]; ok {
		return ps, nil
	}
	return proxyhelpertest.ProcStart(pid)
}

func (f *fakeOS) dialRefused(sock string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.refused[sock]; ok {
		return v
	}
	return true
}

func (f *fakeOS) signal(pid int, sig os.Signal) error {
	f.mu.Lock()
	f.signals = append(f.signals, sentSignal{pid, sig})
	cb := f.onSignal
	f.mu.Unlock()
	if cb != nil {
		cb(pid, sig)
	}
	return nil
}

func (f *fakeOS) sent() []sentSignal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentSignal(nil), f.signals...)
}

func (f *fakeOS) set(fn func()) {
	f.mu.Lock()
	fn()
	f.mu.Unlock()
}

// frameEvent is one onFrame callback.
type frameEvent struct {
	h    *helper
	line string
}

// logSink collects the manager's log lines.
type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *logSink) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// contains reports whether any logged line contains substr.
func (l *logSink) contains(substr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// switchStarter lets one manager spawn from different Fakes over its
// life (a cap test needs 31 Normal helpers and then one Barrier).
type switchStarter struct {
	cur atomic.Pointer[proxyhelpertest.Fake]
}

func (s *switchStarter) start(ctx context.Context) (proxyhelper.Proc, error) {
	return s.cur.Load().Starter()(ctx)
}

// testManager bundles a manager with every seam it was built from.
type testManager struct {
	m           *helperManager
	fake        *proxyhelpertest.Fake
	starter     *switchStarter
	clock       *safeClock
	os          *fakeOS
	logs        *logSink
	sockDir     string
	registryDir string
	proxiesPath string
	frames      chan frameEvent
	entries     func() []ipeers.Entry
}

type tmOption func(*testManager)

func withVariant(v proxyhelpertest.Variant) tmOption {
	return func(tm *testManager) { tm.fake = proxyhelpertest.New(proxyhelpertest.Options{Variant: v}) }
}

func withEntries(fn func() []ipeers.Entry) tmOption {
	return func(tm *testManager) { tm.entries = fn }
}

const (
	testReadyTimeout = 2 * time.Second
	testTermGrace    = 100 * time.Millisecond
)

// newTestManager builds a manager over fakes. It does NOT sweep; most
// tests call sweepOK first.
func newTestManager(t *testing.T, opts ...tmOption) *testManager {
	t.Helper()
	sockDir, registryDir := proxyhelpertest.TempDirs(t)
	tm := &testManager{
		fake:        proxyhelpertest.New(proxyhelpertest.Options{}),
		starter:     &switchStarter{},
		clock:       newSafeClock(),
		os:          newFakeOS(),
		logs:        &logSink{},
		sockDir:     sockDir,
		registryDir: registryDir,
		proxiesPath: filepath.Join(filepath.Dir(registryDir), "proxies.json"),
		frames:      make(chan frameEvent, 64),
		entries:     func() []ipeers.Entry { return nil },
	}
	for _, o := range opts {
		o(tm)
	}
	tm.starter.cur.Store(tm.fake)
	tm.m = newHelperManager(helperManagerConfig{
		Start:        tm.starter.start,
		ProxiesPath:  tm.proxiesPath,
		RegistryDir:  registryDir,
		SockDir:      sockDir,
		Version:      "2.1.270",
		Now:          tm.clock.Now,
		ProcStart:    tm.os.procStart,
		PidAlive:     tm.os.pidAlive,
		DialRefused:  tm.os.dialRefused,
		Signal:       tm.os.signal,
		LiveEntries:  func() []ipeers.Entry { return tm.entries() },
		ReadyTimeout: testReadyTimeout,
		TermGrace:    testTermGrace,
		OnFrame:      func(h *helper, line string) { tm.frames <- frameEvent{h, line} },
		Log:          tm.logs.logf,
	})
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() { tm.m.Stop(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("cleanup: manager Stop did not return within 10 s")
		}
	})
	return tm
}

func (tm *testManager) sweepOK(t *testing.T) {
	t.Helper()
	if err := tm.m.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
}

// swap points the manager's starter at a fresh Fake of variant v.
func (tm *testManager) swap(v proxyhelpertest.Variant) *proxyhelpertest.Fake {
	f := proxyhelpertest.New(proxyhelpertest.Options{Variant: v})
	tm.starter.cur.Store(f)
	return f
}

func originN(n int) ipeers.OriginKey {
	return ipeers.OriginKey{
		HostID:         "air:abc",
		AgentSessionID: fmt.Sprintf("00000000-0000-4000-8000-%012d", n),
		PID:            1000 + n,
		ProcStart:      "Mon Sep 14 10:00:00 2026",
	}
}

var originA = originN(1)

func acquireOK(t *testing.T, tm *testManager, key ipeers.OriginKey) *helper {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h, err := tm.m.Acquire(ctx, key, "air/"+key.AgentSessionID[len(key.AgentSessionID)-4:], revUnapplied)
	if err != nil {
		t.Fatalf("Acquire(%v): %v", key, err)
	}
	return h
}

// stateOf reads h.state under the manager lock.
func (tm *testManager) stateOf(h *helper) helperState {
	tm.m.mu.Lock()
	defer tm.m.mu.Unlock()
	return h.state
}

func (tm *testManager) lastUsedOf(h *helper) time.Time {
	tm.m.mu.Lock()
	defer tm.m.mu.Unlock()
	return h.lastUsed
}

func (tm *testManager) mapLen() int {
	tm.m.mu.Lock()
	defer tm.m.mu.Unlock()
	return len(tm.m.helpers)
}

func (tm *testManager) unresolvedLen() int {
	tm.m.mu.Lock()
	defer tm.m.mu.Unlock()
	return len(tm.m.unresolved)
}

// eventually polls cond every 5 ms for at most d.
func eventually(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %v: %s", d, msg)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stillBlocked asserts ch has not fired within d.
func stillBlocked(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s returned early", what)
	case <-time.After(d):
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("%s did not finish within %v", what, d)
	}
}

func assertNoGoroutineGrowth(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if runtime.NumGoroutine() <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("goroutines: %d before, %d after 1 s", before, runtime.NumGoroutine())
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readProxies(t *testing.T, path string) []proxyRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var recs []proxyRecord
	if err := json.Unmarshal(data, &recs); err != nil {
		t.Fatalf("decode %s: %v\n%s", path, err, data)
	}
	return recs
}

func writeProxies(t *testing.T, path string, recs []proxyRecord) {
	t.Helper()
	data, err := json.Marshal(recs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeRegistryFiles creates <dir>/<pid>.json and <dir>/<pid>.<sha>.key
// carrying procStart, the way a helper (or Claude Code) would.
func writeRegistryFiles(t *testing.T, dir string, pid int, procStart string, features []string) []string {
	t.Helper()
	created, err := ccuds.WriteRegistry(dir, ccuds.RegistryEntry{
		PID: pid, SessionID: "11111111-2222-4333-8444-555555555555", Name: "x", Cwd: dir,
		ProcStart: procStart, Version: "2.1.270", Inbox: filepath.Join(dir, strconv.Itoa(pid)+".sock"),
		PeerFeatures: features,
	}, "tok-"+strconv.Itoa(pid))
	if err != nil {
		t.Fatalf("WriteRegistry: %v", err)
	}
	return created
}

func touchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func allExist(paths ...string) bool {
	for _, p := range paths {
		if !proxyhelpertest.Exists(p) {
			return false
		}
	}
	return true
}

func noneExist(paths ...string) bool {
	for _, p := range paths {
		if proxyhelpertest.Exists(p) {
			return false
		}
	}
	return true
}

func hasTmp(t *testing.T, dir string) bool {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Acquire
// ---------------------------------------------------------------------------

func TestHelperManager_AcquireConcurrentSameKeySpawnsOnce(t *testing.T) {
	tm := newTestManager(t, withVariant(proxyhelpertest.Barrier))
	tm.sweepOK(t)

	type res struct {
		h   *helper
		err error
	}
	results := make(chan res, 2)
	first := make(chan struct{}) // closed when the first Acquire returns
	var firstOnce sync.Once
	for i := 0; i < 2; i++ {
		go func() {
			h, err := tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
			firstOnce.Do(func() { close(first) })
			results <- res{h, err}
		}()
	}
	eventually(t, time.Second, func() bool { return tm.fake.Spawns() == 1 }, "one spawn")
	// Both are parked on the same starting instance while the Barrier holds.
	stillBlocked(t, first, 30*time.Millisecond, "Acquire while the Barrier holds")
	if n := tm.fake.Spawns(); n != 1 {
		t.Fatalf("spawns = %d, want 1", n)
	}
	tm.fake.Release()

	var got [2]res
	for i := range got {
		select {
		case got[i] = <-results:
		case <-time.After(3 * time.Second):
			t.Fatal("Acquire did not return")
		}
	}
	for _, r := range got {
		if r.err != nil {
			t.Fatalf("Acquire: %v", r.err)
		}
	}
	if got[0].h != got[1].h {
		t.Fatalf("different helpers: %p vs %p", got[0].h, got[1].h)
	}
	if n := tm.fake.Spawns(); n != 1 {
		t.Fatalf("spawns = %d, want 1", n)
	}
	if st := tm.stateOf(got[0].h); st != helperReady {
		t.Fatalf("state = %v, want ready", st)
	}
}

func TestHelperManager_AcquireAfterReadyReturnsSameInstance(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	h1 := acquireOK(t, tm, originA)
	h2 := acquireOK(t, tm, originA)
	if h1 != h2 {
		t.Fatalf("Acquire returned a different instance")
	}
	if h1.gen != h2.gen || h1.gen == 0 {
		t.Fatalf("gen = %d / %d", h1.gen, h2.gen)
	}
	if tm.fake.Spawns() != 1 {
		t.Fatalf("spawns = %d, want 1", tm.fake.Spawns())
	}
	if !proxyhelpertest.Exists(h1.sock) {
		t.Fatalf("sock %s missing", h1.sock)
	}
	if h1.pid == 0 || h1.procStart == "" || len(h1.files) != 2 {
		t.Fatalf("helper fields not filled: %+v", h1)
	}
}

func fillHelpers(t *testing.T, tm *testManager, from, to int) []*helper {
	t.Helper()
	var hs []*helper
	for i := from; i < to; i++ {
		hs = append(hs, acquireOK(t, tm, originN(i)))
	}
	return hs
}

func TestHelperManager_CapRefuses33rd(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	fillHelpers(t, tm, 1, HelperCap+1)
	if tm.mapLen() != HelperCap {
		t.Fatalf("map len = %d", tm.mapLen())
	}
	_, err := tm.m.Acquire(context.Background(), originN(HelperCap+1), "air/x", revUnapplied)
	if !errors.Is(err, ErrProxyLimit) {
		t.Fatalf("err = %v, want ErrProxyLimit", err)
	}
	// An existing key is still served.
	acquireOK(t, tm, originN(1))
}

func TestHelperManager_CapCountsStartingHelper(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	fillHelpers(t, tm, 1, HelperCap)
	barrier := tm.swap(proxyhelpertest.Barrier)
	defer barrier.Release()

	go tm.m.Acquire(context.Background(), originN(HelperCap), "air/starting", revUnapplied)
	eventually(t, time.Second, func() bool { return barrier.Spawns() == 1 }, "starting spawn")

	_, err := tm.m.Acquire(context.Background(), originN(HelperCap+1), "air/x", revUnapplied)
	if !errors.Is(err, ErrProxyLimit) {
		t.Fatalf("err = %v, want ErrProxyLimit while one helper is starting", err)
	}
	barrier.Release()
}

func TestHelperManager_CapCountsStoppingHelper(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	hold := make(chan struct{})
	tm.fake.HoldStop(hold)
	hs := fillHelpers(t, tm, 1, HelperCap+1)

	released := make(chan struct{})
	go func() { tm.m.Release(hs[0], "test"); close(released) }()
	eventually(t, time.Second, func() bool { return tm.stateOf(hs[0]) == helperStopping }, "stopping")

	_, err := tm.m.Acquire(context.Background(), originN(HelperCap+1), "air/x", revUnapplied)
	if !errors.Is(err, ErrProxyLimit) {
		t.Fatalf("err = %v, want ErrProxyLimit while one helper is stopping", err)
	}
	close(hold)
	waitClosed(t, released, 3*time.Second, "Release")
	acquireOK(t, tm, originN(HelperCap+1))
}

func TestHelperManager_BrokenSpawnFailsEveryWaiterOnce(t *testing.T) {
	tm := newTestManager(t, withVariant(proxyhelpertest.Broken))
	tm.m.readyTimeout = 100 * time.Millisecond
	tm.sweepOK(t)

	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() {
			_, err := tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
			errs <- err
		}()
	}
	for i := 0; i < 3; i++ {
		select {
		case err := <-errs:
			if !errors.Is(err, ErrProxySpawnFailed) {
				t.Fatalf("waiter %d: err = %v, want ErrProxySpawnFailed", i, err)
			}
			if !errors.Is(err, proxyhelper.ErrNotReady) {
				t.Errorf("waiter %d: err %v does not wrap proxyhelper.ErrNotReady", i, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("waiter did not return")
		}
	}
	if n := tm.fake.Spawns(); n != 1 {
		t.Fatalf("spawns = %d, want exactly 1 (no automatic retry)", n)
	}
	if tm.mapLen() != 0 {
		t.Fatalf("map not empty after failed startup")
	}
	sock := filepath.Join(tm.sockDir, strconv.Itoa(tm.fake.LastPID())+".sock")
	if proxyhelpertest.Exists(sock) {
		t.Fatalf("socket %s left behind", sock)
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v, want empty", recs)
	}
}

func TestHelperManager_RefusingSpawnFails(t *testing.T) {
	tm := newTestManager(t, withVariant(proxyhelpertest.Refusing))
	tm.sweepOK(t)
	_, err := tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
	if !errors.Is(err, ErrProxySpawnFailed) {
		t.Fatalf("err = %v, want ErrProxySpawnFailed", err)
	}
	if tm.fake.Spawns() != 1 || tm.mapLen() != 0 {
		t.Fatalf("spawns=%d map=%d", tm.fake.Spawns(), tm.mapLen())
	}
	// The caller decides to retry: a fresh Acquire spawns again.
	_, err = tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
	if !errors.Is(err, ErrProxySpawnFailed) || tm.fake.Spawns() != 2 {
		t.Fatalf("retry: err=%v spawns=%d", err, tm.fake.Spawns())
	}
}

func TestHelperManager_ProcStartFailureRollsBack(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	pid := proxyhelpertest.PeekPID()
	tm.os.set(func() { tm.os.psErr[pid] = errors.New("ps: boom") })

	_, err := tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
	if !errors.Is(err, ErrProxySpawnFailed) {
		t.Fatalf("err = %v, want ErrProxySpawnFailed", err)
	}
	if tm.fake.LastPID() != pid {
		t.Fatalf("pid %d spawned, expected %d", tm.fake.LastPID(), pid)
	}
	if tm.fake.Stops() != 1 {
		t.Fatalf("stops = %d, want 1 (helper stopped)", tm.fake.Stops())
	}
	jsonPath := filepath.Join(tm.registryDir, strconv.Itoa(pid)+".json")
	sock := filepath.Join(tm.sockDir, strconv.Itoa(pid)+".sock")
	eventually(t, time.Second, func() bool { return noneExist(jsonPath, sock) }, "files removed")
	keys, _ := filepath.Glob(filepath.Join(tm.registryDir, strconv.Itoa(pid)+".*.key"))
	if len(keys) != 0 {
		t.Fatalf("key files left: %v", keys)
	}
	if tm.mapLen() != 0 {
		t.Fatalf("map not empty")
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v, want empty", recs)
	}
}

func TestHelperManager_WriteProxiesFailureRollsBack(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	tm.m.mu.Lock()
	tm.m.proxiesPath = filepath.Join(tm.registryDir, "no-such-dir", "proxies.json")
	tm.m.mu.Unlock()
	pid := proxyhelpertest.PeekPID()

	_, err := tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
	if !errors.Is(err, ErrProxySpawnFailed) {
		t.Fatalf("err = %v, want ErrProxySpawnFailed", err)
	}
	if tm.fake.Stops() != 1 {
		t.Fatalf("stops = %d, want 1", tm.fake.Stops())
	}
	jsonPath := filepath.Join(tm.registryDir, strconv.Itoa(pid)+".json")
	sock := filepath.Join(tm.sockDir, strconv.Itoa(pid)+".sock")
	eventually(t, time.Second, func() bool { return noneExist(jsonPath, sock) }, "files removed")
	if tm.mapLen() != 0 {
		t.Fatalf("map not empty")
	}
}

// leftoverSockProc wraps a fake helper process so that, once it has
// exited (Wait returned), its socket path reappears on disk — what a
// helper that died without running its own cleanup leaves behind.
type leftoverSockProc struct {
	proxyhelper.Proc
	sock string
}

func (p leftoverSockProc) Wait() error {
	err := p.Proc.Wait()
	_ = os.WriteFile(p.sock, nil, 0o600)
	return err
}

// TestHelperManager_RollbackRemovesLeftoverSocket pins that the startup
// rollback (after a failed procStart / proxies.json write) removes the
// helper's socket path as well as its registry files — but only when
// nobody listens on it any more, exactly like Release.
func TestHelperManager_RollbackRemovesLeftoverSocket(t *testing.T) {
	for _, c := range []struct {
		name    string
		refused bool // dialRefused answer for the leftover path
		wantIn  bool // the path is expected to survive
	}{
		{"dead socket removed", true, false},
		{"live socket kept", false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			tm := newTestManager(t)
			tm.sweepOK(t)
			pid := proxyhelpertest.PeekPID()
			sock := filepath.Join(tm.sockDir, strconv.Itoa(pid)+".sock")
			inner := tm.m.start
			tm.m.start = func(ctx context.Context) (proxyhelper.Proc, error) {
				p, err := inner(ctx)
				if err != nil {
					return nil, err
				}
				return leftoverSockProc{Proc: p, sock: filepath.Join(tm.sockDir, strconv.Itoa(p.PID())+".sock")}, nil
			}
			tm.os.set(func() {
				tm.os.psErr[pid] = errors.New("ps: boom")
				tm.os.refused[sock] = c.refused
			})

			_, err := tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
			if !errors.Is(err, ErrProxySpawnFailed) {
				t.Fatalf("err = %v, want ErrProxySpawnFailed", err)
			}
			if tm.fake.Stops() != 1 {
				t.Fatalf("stops = %d, want 1", tm.fake.Stops())
			}
			jsonPath := filepath.Join(tm.registryDir, strconv.Itoa(pid)+".json")
			eventually(t, time.Second, func() bool { return noneExist(jsonPath) }, "registry file removed")
			if got := proxyhelpertest.Exists(sock); got != c.wantIn {
				t.Errorf("socket %s exists = %v, want %v", sock, got, c.wantIn)
			}
			if tm.mapLen() != 0 {
				t.Fatalf("map not empty")
			}
		})
	}
}

func TestHelperManager_ProxiesJSONShapeAfterTwoSpawns(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	h1 := acquireOK(t, tm, originN(1))
	h2 := acquireOK(t, tm, originN(2))

	data, err := os.ReadFile(tm.proxiesPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode: %v\n%s", err, data)
	}
	if len(raw) != 2 {
		t.Fatalf("records = %d, want 2\n%s", len(raw), data)
	}
	wantKeys := []string{"pid", "proc_start", "sock", "files", "origin"}
	for i, rec := range raw {
		for _, k := range wantKeys {
			if _, ok := rec[k]; !ok {
				t.Errorf("record %d lacks key %q: %s", i, k, data)
			}
		}
		if len(rec) != len(wantKeys) {
			t.Errorf("record %d has extra keys: %s", i, data)
		}
		var origin map[string]json.RawMessage
		if err := json.Unmarshal(rec["origin"], &origin); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"host_id", "agent_session_id", "pid", "proc_start"} {
			if _, ok := origin[k]; !ok {
				t.Errorf("origin lacks %q: %s", k, rec["origin"])
			}
		}
	}
	recs := readProxies(t, tm.proxiesPath)
	byPID := map[int]proxyRecord{}
	for _, r := range recs {
		byPID[r.PID] = r
	}
	for _, h := range []*helper{h1, h2} {
		r, ok := byPID[h.pid]
		if !ok {
			t.Fatalf("pid %d not recorded", h.pid)
		}
		if r.ProcStart != h.procStart || r.Sock != h.sock || len(r.Files) != 2 || r.Origin != h.key {
			t.Errorf("record %+v does not match helper %+v", r, h)
		}
	}
	if hasTmp(t, filepath.Dir(tm.proxiesPath)) {
		t.Fatalf(".tmp left in %s", filepath.Dir(tm.proxiesPath))
	}
	st, err := os.Stat(tm.proxiesPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Errorf("proxies.json mode = %v, want no group/other bits", st.Mode().Perm())
	}
}

func TestHelperManager_CreatorWaitCtxCancelledStartupCompletes(t *testing.T) {
	tm := newTestManager(t, withVariant(proxyhelpertest.Barrier))
	tm.sweepOK(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := tm.m.Acquire(ctx, originA, "air/a", revUnapplied)
		done <- err
	}()
	eventually(t, time.Second, func() bool { return tm.fake.Spawns() == 1 }, "spawn")
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Acquire did not return on ctx cancel")
	}
	if tm.mapLen() != 1 {
		t.Fatalf("starting instance vanished with its creator")
	}
	tm.fake.Release()
	h := acquireOK(t, tm, originA)
	if tm.fake.Spawns() != 1 {
		t.Fatalf("spawns = %d, want 1", tm.fake.Spawns())
	}
	if tm.stateOf(h) != helperReady {
		t.Fatalf("not ready")
	}
}

// ---------------------------------------------------------------------------
// Release
// ---------------------------------------------------------------------------

func TestHelperManager_ReleaseInstanceBound(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	hold := make(chan struct{})
	tm.fake.HoldStop(hold)
	h1 := acquireOK(t, tm, originA)

	released := make(chan struct{})
	go func() { tm.m.Release(h1, "test"); close(released) }()
	eventually(t, time.Second, func() bool { return tm.stateOf(h1) == helperStopping }, "stopping")

	// ProxyPIDs still lists the stopping instance.
	if !tm.m.ProxyPIDs()[h1.pid] {
		t.Fatalf("ProxyPIDs lost the stopping pid")
	}

	var (
		h2     *helper
		acqErr error
	)
	acqDone := make(chan struct{})
	go func() {
		h2, acqErr = tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
		close(acqDone)
	}()
	stillBlocked(t, acqDone, 50*time.Millisecond, "Acquire during stopping")
	if tm.fake.Spawns() != 1 {
		t.Fatalf("a second process was spawned while h1 was still alive")
	}
	close(hold)
	waitClosed(t, released, 3*time.Second, "Release(h1)")
	waitClosed(t, acqDone, 3*time.Second, "Acquire after exit")
	if acqErr != nil {
		t.Fatalf("Acquire after exit: %v", acqErr)
	}
	if h2 == h1 {
		t.Fatalf("Acquire returned the released instance")
	}
	if h2.gen != h1.gen+1 {
		t.Fatalf("gen: h1=%d h2=%d", h1.gen, h2.gen)
	}
	if tm.fake.Spawns() != 2 {
		t.Fatalf("spawns = %d, want 2", tm.fake.Spawns())
	}
	if proxyhelpertest.Exists(h1.sock) {
		t.Fatalf("h1's sock still on disk while h2 is ready")
	}
	if tm.stateOf(h1) != helperExited {
		t.Fatalf("h1 state = %v, want exited", tm.stateOf(h1))
	}
	select {
	case <-h1.exited:
	default:
		t.Fatalf("h1.exited not closed")
	}

	// A late Release(h1) is a no-op: h2 stays, no extra Stop.
	stops := tm.fake.Stops()
	tm.m.Release(h1, "late")
	if tm.stateOf(h2) != helperReady || tm.fake.Stops() != stops {
		t.Fatalf("late Release(h1) touched h2 or stopped something")
	}
	if got := acquireOK(t, tm, originA); got != h2 {
		t.Fatalf("h2 replaced")
	}
}

func TestHelperManager_ConcurrentReleaseStopsOnce(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	h := acquireOK(t, tm, originA)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); tm.m.Release(h, "test") }()
	}
	wg.Wait()
	if tm.fake.Stops() != 1 {
		t.Fatalf("stops = %d, want 1", tm.fake.Stops())
	}
	if tm.mapLen() != 0 {
		t.Fatalf("map not empty")
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v", recs)
	}
	if !noneExist(append([]string{h.sock}, h.files...)...) {
		t.Fatalf("files left behind")
	}
}

func TestHelperManager_ReapIdleReleasesOnlyIdle(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	idle := acquireOK(t, tm, originN(1))
	busy := acquireOK(t, tm, originN(2))

	tm.clock.Advance(5 * time.Minute)
	tm.m.Touch(originN(2))
	if got := tm.lastUsedOf(busy); !got.Equal(tm.clock.Now()) {
		t.Fatalf("Touch did not update lastUsed: %v", got)
	}
	tm.clock.Advance(26 * time.Minute) // idle: 31 min, busy: 26 min

	tm.m.ReapIdle()
	if tm.stateOf(idle) != helperExited {
		t.Fatalf("idle helper not released: %v", tm.stateOf(idle))
	}
	if tm.stateOf(busy) != helperReady {
		t.Fatalf("busy helper released")
	}
	if tm.fake.Stops() != 1 {
		t.Fatalf("stops = %d, want 1", tm.fake.Stops())
	}
	// Acquire refreshes lastUsed too.
	tm.clock.Advance(10 * time.Minute)
	acquireOK(t, tm, originN(2))
	if !tm.lastUsedOf(busy).Equal(tm.clock.Now()) {
		t.Fatalf("Acquire did not touch lastUsed")
	}
}

// TestHelperManager_ReapIdleRechecksUnderLock (R2-B): ReapIdle's idle
// snapshot is stale by the time it reaches its later candidates — an
// earlier candidate's Stop waits its grace meanwhile, and Acquire/Touch
// may have refreshed lastUsed. The release must re-check idleness under
// the lock right before flipping to stopping: a helper touched between
// the snapshot and its turn survives; the untouched one is released.
func TestHelperManager_ReapIdleRechecksUnderLock(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	hold := make(chan struct{})
	tm.fake.HoldStop(hold)
	h1 := acquireOK(t, tm, originN(1))
	h2 := acquireOK(t, tm, originN(2))
	tm.clock.Advance(HelperIdleReap + time.Minute) // both well past the reap threshold

	reaped := make(chan struct{})
	go func() { tm.m.ReapIdle(); close(reaped) }()

	// Whichever candidate the reaper took first is parked in Stop (Wait is
	// held); the other is still ready and gets traffic before its turn.
	var first, other *helper
	eventually(t, time.Second, func() bool {
		switch {
		case tm.stateOf(h1) == helperStopping:
			first, other = h1, h2
		case tm.stateOf(h2) == helperStopping:
			first, other = h2, h1
		default:
			return false
		}
		return true
	}, "one candidate stopping")
	if got := acquireOK(t, tm, other.key); got != other {
		t.Fatalf("Acquire(other) returned a different instance")
	}
	if !tm.lastUsedOf(other).Equal(tm.clock.Now()) {
		t.Fatalf("Acquire did not refresh lastUsed")
	}
	close(hold)
	waitClosed(t, reaped, 3*time.Second, "ReapIdle")

	if tm.stateOf(first) != helperExited {
		t.Errorf("first candidate state = %v, want exited", tm.stateOf(first))
	}
	if tm.stateOf(other) != helperReady {
		t.Errorf("touched candidate state = %v, want ready (stale snapshot released it)", tm.stateOf(other))
	}
	if tm.fake.Stops() != 1 {
		t.Errorf("stops = %d, want 1", tm.fake.Stops())
	}
	if got := acquireOK(t, tm, other.key); got != other {
		t.Errorf("touched helper replaced")
	}
	// Untouched, it goes on the next pass — idle for exactly HelperIdleReap
	// is idle enough (>=).
	tm.clock.Advance(HelperIdleReap)
	tm.m.ReapIdle()
	if tm.stateOf(other) != helperExited || tm.fake.Stops() != 2 {
		t.Errorf("state/stops after the next pass = %v/%d, want exited/2", tm.stateOf(other), tm.fake.Stops())
	}
}

// TestHelperManager_ReleaseUnlinksOnlyOwned (R2-C): between the helper's
// exit and Release's cleanup a same-UID process (or a reused pid) may
// recreate the registry file or bind the socket path. Release mirrors
// Sweep: a registry file is unlinked only while it still carries the
// helper's procStart, the socket only while nobody listens on it; a
// mismatch is logged and left, and is not a cleanup failure.
func TestHelperManager_ReleaseUnlinksOnlyOwned(t *testing.T) {
	const foreign = "Mon Jan  1 00:00:00 2001"
	t.Run("recreated registry file with another procStart survives", func(t *testing.T) {
		tm := newTestManager(t)
		tm.sweepOK(t)
		hold := make(chan struct{})
		tm.fake.HoldStop(hold)
		h := acquireOK(t, tm, originA)
		jsonPath := filepath.Join(tm.registryDir, strconv.Itoa(h.pid)+".json")

		released := make(chan struct{})
		go func() { tm.m.Release(h, "test"); close(released) }()
		// The helper's own cleanup (on stdin close) removes its files;
		// Stop is then parked on the held Wait. Someone else recreates
		// the path with their identity.
		eventually(t, time.Second, func() bool { return noneExist(h.files...) }, "helper removed its own files")
		if err := os.WriteFile(jsonPath, []byte(`{"pid":`+strconv.Itoa(h.pid)+`,"procStart":"`+foreign+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		close(hold)
		waitClosed(t, released, 3*time.Second, "Release")

		if !proxyhelpertest.Exists(jsonPath) {
			t.Errorf("a registry file carrying another procStart was unlinked")
		}
		if got := ccuds.RegistryProcStart(jsonPath); got != foreign {
			t.Errorf("registry file procStart = %q, want the foreign %q untouched", got, foreign)
		}
		if tm.unresolvedLen() != 0 {
			t.Errorf("unresolved = %d, want 0 (a foreign file is not a cleanup failure)", tm.unresolvedLen())
		}
		if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
			t.Errorf("proxies.json = %+v, want empty", recs)
		}
		if !strings.Contains(strings.Join(tm.logs.all(), "\n"), "not ours") {
			t.Errorf("no log line about the foreign file: %v", tm.logs.all())
		}
	})
	t.Run("recreated registry file with our procStart is unlinked", func(t *testing.T) {
		tm := newTestManager(t)
		tm.sweepOK(t)
		hold := make(chan struct{})
		tm.fake.HoldStop(hold)
		h := acquireOK(t, tm, originA)
		jsonPath := filepath.Join(tm.registryDir, strconv.Itoa(h.pid)+".json")

		released := make(chan struct{})
		go func() { tm.m.Release(h, "test"); close(released) }()
		eventually(t, time.Second, func() bool { return noneExist(h.files...) }, "helper removed its own files")
		if err := os.WriteFile(jsonPath, []byte(`{"pid":`+strconv.Itoa(h.pid)+`,"procStart":"`+h.procStart+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		close(hold)
		waitClosed(t, released, 3*time.Second, "Release")
		if proxyhelpertest.Exists(jsonPath) {
			t.Errorf("a leftover carrying our procStart was not unlinked")
		}
	})
	t.Run("live foreign listener at the sock path survives", func(t *testing.T) {
		tm := newTestManager(t)
		tm.sweepOK(t)
		hold := make(chan struct{})
		tm.fake.HoldStop(hold)
		h := acquireOK(t, tm, originA)

		released := make(chan struct{})
		go func() { tm.m.Release(h, "test"); close(released) }()
		eventually(t, time.Second, func() bool { return noneExist(h.sock) }, "helper closed its own socket")
		ln, err := net.Listen("unix", h.sock)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		tm.os.set(func() { tm.os.refused[h.sock] = false })
		close(hold)
		waitClosed(t, released, 3*time.Second, "Release")

		if !proxyhelpertest.Exists(h.sock) {
			t.Errorf("a socket somebody listens on was unlinked")
		}
		if tm.unresolvedLen() != 0 {
			t.Errorf("unresolved = %d, want 0 (a live listener is not a cleanup failure)", tm.unresolvedLen())
		}
		if !strings.Contains(strings.Join(tm.logs.all(), "\n"), "live listener") {
			t.Errorf("no log line about the live listener: %v", tm.logs.all())
		}
	})
}

// TestHelperManager_RollbackUnlinksOnlyOwned (R2-C): the startup rollback
// after a failed proxies.json write (the helper's procStart is known)
// applies the same ownership rules as Release to what the dead helper
// left behind.
func TestHelperManager_RollbackUnlinksOnlyOwned(t *testing.T) {
	const foreign = "Mon Jan  1 00:00:00 2001"
	for _, c := range []struct {
		name      string
		procStart func(pid int) string // what the leftover json carries
		wantKept  bool
	}{
		{"leftover with our procStart unlinked", func(pid int) string { s, _ := proxyhelpertest.ProcStart(pid); return s }, false},
		{"leftover with another procStart kept", func(int) string { return foreign }, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			tm := newTestManager(t)
			tm.sweepOK(t)
			tm.m.mu.Lock()
			tm.m.proxiesPath = filepath.Join(tm.registryDir, "no-such-dir", "proxies.json")
			tm.m.mu.Unlock()
			pid := proxyhelpertest.PeekPID()
			jsonPath := filepath.Join(tm.registryDir, strconv.Itoa(pid)+".json")
			inner := tm.m.start
			tm.m.start = func(ctx context.Context) (proxyhelper.Proc, error) {
				p, err := inner(ctx)
				if err != nil {
					return nil, err
				}
				return leftoverProc{Proc: p, path: jsonPath, content: `{"pid":` + strconv.Itoa(p.PID()) + `,"procStart":"` + c.procStart(p.PID()) + `"}`}, nil
			}

			_, err := tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
			if !errors.Is(err, ErrProxySpawnFailed) {
				t.Fatalf("err = %v, want ErrProxySpawnFailed", err)
			}
			if got := proxyhelpertest.Exists(jsonPath); got != c.wantKept {
				t.Errorf("leftover %s exists = %v, want %v", jsonPath, got, c.wantKept)
			}
			if tm.mapLen() != 0 {
				t.Fatalf("map not empty")
			}
		})
	}
}

// leftoverProc wraps a fake helper process so that, once it has exited
// (Wait returned), a file reappears at path with content — a registry
// file a dead helper (or someone else, at its pid) left behind.
type leftoverProc struct {
	proxyhelper.Proc
	path, content string
}

func (p leftoverProc) Wait() error {
	err := p.Proc.Wait()
	_ = os.WriteFile(p.path, []byte(p.content), 0o600)
	return err
}

func TestHelperManager_PumpDeliversFramesAndTouches(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	h := acquireOK(t, tm, originA)
	t0 := tm.lastUsedOf(h)
	tm.clock.Advance(time.Minute)

	proxyhelpertest.WriteToSock(t, h.sock, `{"v":1,"type":"message","text":"hi"}`)
	select {
	case ev := <-tm.frames:
		if ev.h != h || ev.line != `{"v":1,"type":"message","text":"hi"}` {
			t.Fatalf("frame = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame delivered")
	}
	if got := tm.lastUsedOf(h); !got.After(t0) || !got.Equal(tm.clock.Now()) {
		t.Fatalf("lastUsed not touched by pump: %v (t0 %v)", got, t0)
	}
	if got, ok := tm.m.FindBySock(h.sock); !ok || got != h {
		t.Fatalf("FindBySock: %v %v", got, ok)
	}
	if _, ok := tm.m.FindBySock("/nope.sock"); ok {
		t.Fatalf("FindBySock found an unknown sock")
	}
}

func TestHelperManager_HelperExitOnItsOwnRemovedAndRewritten(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	h := acquireOK(t, tm, originA)
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 1 {
		t.Fatalf("records = %d", len(recs))
	}
	tm.fake.ExitOnItsOwn()
	waitClosed(t, h.exited, 3*time.Second, "h.exited")
	if tm.mapLen() != 0 {
		t.Fatalf("map not empty after the helper died")
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v, want empty", recs)
	}
	if tm.fake.Stops() != 1 {
		t.Fatalf("stops = %d", tm.fake.Stops())
	}
	// A new Acquire spawns a fresh instance.
	h2 := acquireOK(t, tm, originA)
	if h2 == h || h2.gen <= h.gen {
		t.Fatalf("no fresh instance")
	}
}

func TestHelperManager_ReleaseUnlinkFailureKeepsRecord(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	tm := newTestManager(t)
	tm.sweepOK(t)
	h := acquireOK(t, tm, originA)
	if err := os.Chmod(tm.sockDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(tm.sockDir, 0o700) })

	tm.m.Release(h, "test")
	if tm.mapLen() != 0 {
		t.Fatalf("map not empty")
	}
	recs := readProxies(t, tm.proxiesPath)
	if len(recs) != 1 || recs[0].PID != h.pid || recs[0].Sock != h.sock {
		t.Fatalf("proxies.json = %+v, want the leftover record", recs)
	}
	if !proxyhelpertest.Exists(h.sock) {
		t.Fatalf("sock unexpectedly gone")
	}
	// The dead helper's leftover occupies nothing: the origin is free.
	tm.m.mu.Lock()
	occ := tm.m.unresolvedOccupies(originA)
	alive := tm.m.unresolvedAlive()
	tm.m.mu.Unlock()
	if occ || alive != 0 {
		t.Fatalf("dead leftover occupies: %v / %d", occ, alive)
	}
	os.Chmod(tm.sockDir, 0o700)
	h2 := acquireOK(t, tm, originA)
	// The leftover record survives the rewrite alongside the new instance.
	recs = readProxies(t, tm.proxiesPath)
	if len(recs) != 2 {
		t.Fatalf("records = %+v, want leftover + new", recs)
	}
	_ = h2
}

// ---------------------------------------------------------------------------
// Admission / Stop
// ---------------------------------------------------------------------------

func TestHelperManager_AcquireBeforeSweepNotReady(t *testing.T) {
	tm := newTestManager(t)
	_, err := tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v, want ErrNotReady", err)
	}
	if tm.fake.Spawns() != 0 {
		t.Fatalf("spawned before sweep")
	}
}

func TestHelperManager_AcquireAfterStopNotReady(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	h := acquireOK(t, tm, originA)
	tm.m.Stop()
	if tm.stateOf(h) != helperExited || tm.fake.Stops() != 1 {
		t.Fatalf("Stop did not release the helper")
	}
	_, err := tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v, want ErrNotReady", err)
	}
	if tm.fake.Spawns() != 1 {
		t.Fatalf("spawned after Stop")
	}
	tm.m.Stop() // idempotent
}

func TestHelperManager_StopWaitsForBarrierStartup(t *testing.T) {
	before := runtime.NumGoroutine()
	tm := newTestManager(t, withVariant(proxyhelpertest.Barrier))
	tm.sweepOK(t)

	acq := make(chan error, 1)
	go func() {
		_, err := tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
		acq <- err
	}()
	eventually(t, time.Second, func() bool { return tm.fake.Spawns() == 1 }, "spawn")

	stopped := make(chan struct{})
	go func() { tm.m.Stop(); close(stopped) }()
	stillBlocked(t, stopped, 50*time.Millisecond, "Stop with a startup in flight")

	tm.fake.Release()
	waitClosed(t, stopped, 5*time.Second, "Stop")
	select {
	case err := <-acq:
		// The creator wakes on h.ready and re-enters the loop, where the
		// closed check comes first: admission was shut before the helper
		// became ready, so it is refused rather than handed a helper that
		// Stop is about to release.
		if !errors.Is(err, ErrNotReady) {
			t.Fatalf("creator's Acquire: err = %v, want ErrNotReady", err)
		}
	case <-time.After(time.Second):
		t.Fatal("creator's Acquire hung")
	}
	if tm.fake.Stops() != 1 {
		t.Fatalf("stops = %d, want 1 (Stop released the started helper)", tm.fake.Stops())
	}
	pid := tm.fake.LastPID()
	if !noneExist(filepath.Join(tm.sockDir, strconv.Itoa(pid)+".sock"), filepath.Join(tm.registryDir, strconv.Itoa(pid)+".json")) {
		t.Fatalf("helper files left after Stop")
	}
	if tm.mapLen() != 0 {
		t.Fatalf("map not empty after Stop")
	}
	assertNoGoroutineGrowth(t, before)
}

func TestHelperManager_StopWaitsForHeldRelease(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	hold := make(chan struct{})
	tm.fake.HoldStop(hold)
	h := acquireOK(t, tm, originA)

	go tm.m.Release(h, "reap")
	eventually(t, time.Second, func() bool { return tm.stateOf(h) == helperStopping }, "stopping")

	stopped := make(chan struct{})
	go func() { tm.m.Stop(); close(stopped) }()
	stillBlocked(t, stopped, 50*time.Millisecond, "Stop with a Release held")

	close(hold)
	waitClosed(t, stopped, 5*time.Second, "Stop")
	if tm.stateOf(h) != helperExited {
		t.Fatalf("state = %v", tm.stateOf(h))
	}
	if !noneExist(append([]string{h.sock}, h.files...)...) {
		t.Fatalf("helper files left after Stop")
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v, want empty", recs)
	}
}

func TestHelperManager_StopReleasesEveryReadyHelperInParallel(t *testing.T) {
	before := runtime.NumGoroutine()
	tm := newTestManager(t)
	tm.sweepOK(t)
	hs := fillHelpers(t, tm, 1, 6)
	start := time.Now()
	tm.m.Stop()
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Stop took %v", d)
	}
	for _, h := range hs {
		if tm.stateOf(h) != helperExited {
			t.Fatalf("helper %d not exited", h.pid)
		}
	}
	if tm.fake.Stops() != 5 {
		t.Fatalf("stops = %d", tm.fake.Stops())
	}
	assertNoGoroutineGrowth(t, before)
}

// ---------------------------------------------------------------------------
// peerFeatures
// ---------------------------------------------------------------------------

func TestHelperManager_PeerFeaturesCopiedFromNewestLiveEntry(t *testing.T) {
	var (
		entriesMu sync.Mutex
		entries   []ipeers.Entry
	)
	setEntries := func(es []ipeers.Entry) { entriesMu.Lock(); entries = es; entriesMu.Unlock() }
	tm := newTestManager(t, withEntries(func() []ipeers.Entry {
		entriesMu.Lock()
		defer entriesMu.Unlock()
		return entries
	}))
	tm.sweepOK(t)
	older := writeRegistryFiles(t, tm.registryDir, 501, "Mon Sep 14 09:00:00 2026", []string{"old_feature"})
	newer := writeRegistryFiles(t, tm.registryDir, 502, "Mon Sep 14 10:00:00 2026", []string{"new_feature", "x"})
	proxy := writeRegistryFiles(t, tm.registryDir, 503, "Mon Sep 14 11:00:00 2026", []string{"proxy_feature"})
	t.Cleanup(func() { ccuds.RemoveRegistry(append(append(older, newer...), proxy...)) })
	setEntries([]ipeers.Entry{
		{PID: 501, ProcStart: "Mon Sep 14 09:00:00 2026"},
		{PID: 502, ProcStart: "Mon Sep 14 10:00:00 2026"},
		{PID: 503, ProcStart: "Mon Sep 14 11:00:00 2026", IsProxy: true},
		{PID: 504, ProcStart: "garbage"},
	})
	if got := tm.m.peerFeatures(); strings.Join(got, ",") != "new_feature,x" {
		t.Fatalf("peerFeatures = %v", got)
	}
	h := acquireOK(t, tm, originA)
	got, ok := ccuds.ReadPeerFeatures(tm.registryDir, h.pid)
	if !ok || strings.Join(got, ",") != "new_feature,x" {
		t.Fatalf("helper registry peerFeatures = %v %v", got, ok)
	}
	// No live entry ⇒ defaults.
	setEntries(nil)
	if got := tm.m.peerFeatures(); strings.Join(got, ",") != strings.Join(ccuds.DefaultPeerFeatures, ",") {
		t.Fatalf("default peerFeatures = %v", got)
	}
	// Ties on start time ⇒ highest pid wins.
	setEntries([]ipeers.Entry{
		{PID: 501, ProcStart: "Mon Sep 14 10:00:00 2026"},
		{PID: 502, ProcStart: "Mon Sep 14 10:00:00 2026"},
	})
	if got := tm.m.peerFeatures(); strings.Join(got, ",") != "new_feature,x" {
		t.Fatalf("tie peerFeatures = %v", got)
	}
}

// ---------------------------------------------------------------------------
// Sweep
// ---------------------------------------------------------------------------

const (
	sweepPID = 4242
	sweepPS  = "Mon Sep 14 08:00:00 2026"
)

// sweepRecord writes registry files + a dead socket path for sweepPID and
// returns the record naming them.
func sweepRecord(t *testing.T, tm *testManager, pid int, ps string) proxyRecord {
	t.Helper()
	files := writeRegistryFiles(t, tm.registryDir, pid, ps, nil)
	if err := os.MkdirAll(tm.sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(tm.sockDir, strconv.Itoa(pid)+".sock")
	touchFile(t, sock)
	return proxyRecord{PID: pid, ProcStart: ps, Sock: sock, Files: files, Origin: originA}
}

func TestSweep_LiveRecordTermAndUnlink(t *testing.T) {
	tm := newTestManager(t)
	rec := sweepRecord(t, tm, sweepPID, sweepPS)
	writeProxies(t, tm.proxiesPath, []proxyRecord{rec})
	tm.os.set(func() {
		tm.os.alive[sweepPID] = true
		tm.os.ps[sweepPID] = sweepPS
	})
	tm.os.onSignal = func(pid int, sig os.Signal) {
		if sig == syscall.SIGTERM {
			tm.os.set(func() { tm.os.alive[pid] = false })
		}
	}

	if err := tm.m.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	sent := tm.os.sent()
	if len(sent) != 1 || sent[0] != (sentSignal{sweepPID, syscall.SIGTERM}) {
		t.Fatalf("signals = %v, want one SIGTERM", sent)
	}
	if !noneExist(append([]string{rec.Sock}, rec.Files...)...) {
		t.Fatalf("files not unlinked")
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v, want empty", recs)
	}
	acquireOK(t, tm, originA)
}

func TestSweep_PidReusedAfterTermNoKill(t *testing.T) {
	tm := newTestManager(t)
	rec := sweepRecord(t, tm, sweepPID, sweepPS)
	// The reused pid rewrote <pid>.json with its own procStart; the key
	// still carries ours.
	jsonPath := rec.Files[0]
	if err := os.WriteFile(jsonPath, []byte(`{"pid":4242,"procStart":"Mon Sep 14 09:30:00 2026"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeProxies(t, tm.proxiesPath, []proxyRecord{rec})
	tm.os.set(func() {
		tm.os.alive[sweepPID] = true
		tm.os.ps[sweepPID] = sweepPS
	})
	tm.os.onSignal = func(pid int, sig os.Signal) {
		if sig == syscall.SIGTERM {
			tm.os.set(func() { tm.os.ps[pid] = "Mon Sep 14 09:30:00 2026" }) // still alive, new identity
		}
	}

	if err := tm.m.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	sent := tm.os.sent()
	if len(sent) != 1 || sent[0].sig != syscall.SIGTERM {
		t.Fatalf("signals = %v, want exactly one SIGTERM and no SIGKILL", sent)
	}
	if proxyhelpertest.Exists(rec.Files[1]) {
		t.Fatalf("our key file kept")
	}
	if !proxyhelpertest.Exists(jsonPath) {
		t.Fatalf("the new process's json was unlinked")
	}
	if proxyhelpertest.Exists(rec.Sock) {
		t.Fatalf("dead sock kept")
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v, want empty", recs)
	}
	os.Remove(jsonPath)
}

func TestSweep_UnknownIdentityRetainedUntouched(t *testing.T) {
	tm := newTestManager(t)
	rec := sweepRecord(t, tm, sweepPID, sweepPS)
	writeProxies(t, tm.proxiesPath, []proxyRecord{rec})
	tm.os.set(func() {
		tm.os.alive[sweepPID] = true
		tm.os.psErr[sweepPID] = errors.New("ps: permission denied")
	})

	if err := tm.m.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if sent := tm.os.sent(); len(sent) != 0 {
		t.Fatalf("signals sent to an unclassifiable pid: %v", sent)
	}
	if !allExist(append([]string{rec.Sock}, rec.Files...)...) {
		t.Fatalf("files touched")
	}
	recs := readProxies(t, tm.proxiesPath)
	if len(recs) != 1 || recs[0].PID != sweepPID {
		t.Fatalf("proxies.json = %+v, want the record retained", recs)
	}
	if len(tm.logs.all()) == 0 {
		t.Fatalf("nothing logged")
	}
	tm.m.mu.Lock()
	occ := tm.m.unresolvedOccupies(originA)
	tm.m.mu.Unlock()
	if !occ {
		t.Fatalf("record does not occupy its origin")
	}
	t.Cleanup(func() { ccuds.RemoveRegistry(rec.Files) })
}

func TestSweep_IgnoresSignalsRetained(t *testing.T) {
	tm := newTestManager(t)
	rec := sweepRecord(t, tm, sweepPID, sweepPS)
	writeProxies(t, tm.proxiesPath, []proxyRecord{rec})
	tm.os.set(func() {
		tm.os.alive[sweepPID] = true
		tm.os.ps[sweepPID] = sweepPS
	})

	start := time.Now()
	if err := tm.m.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if d := time.Since(start); d < 2*testTermGrace || d > 2*testTermGrace+time.Second {
		t.Fatalf("Sweep took %v, want ≈ 2×termGrace", d)
	}
	sent := tm.os.sent()
	if len(sent) != 2 || sent[0].sig != syscall.SIGTERM || sent[1].sig != syscall.SIGKILL {
		t.Fatalf("signals = %v, want TERM then KILL", sent)
	}
	if !allExist(append([]string{rec.Sock}, rec.Files...)...) {
		t.Fatalf("files touched although the process is still alive")
	}
	recs := readProxies(t, tm.proxiesPath)
	if len(recs) != 1 {
		t.Fatalf("proxies.json = %+v, want retained", recs)
	}
	t.Cleanup(func() { ccuds.RemoveRegistry(rec.Files) })
}

func TestSweep_DeadPidMatchingFilesUnlinked(t *testing.T) {
	tm := newTestManager(t)
	rec := sweepRecord(t, tm, sweepPID, sweepPS)
	writeProxies(t, tm.proxiesPath, []proxyRecord{rec})
	// pidAlive false; procStart errors (no such process).
	tm.os.set(func() { tm.os.psErr[sweepPID] = errors.New("no such process") })

	if err := tm.m.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if sent := tm.os.sent(); len(sent) != 0 {
		t.Fatalf("signals sent to a dead pid: %v", sent)
	}
	if !noneExist(append([]string{rec.Sock}, rec.Files...)...) {
		t.Fatalf("files not unlinked")
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v", recs)
	}
}

func TestSweep_EmptyProcStartNeverUnlinksFiles(t *testing.T) {
	tm := newTestManager(t)
	rec := sweepRecord(t, tm, sweepPID, sweepPS)
	// An unparsable file next to a real one: RegistryProcStart yields ""
	// for it, which must not "match" an empty recorded proc_start.
	garbage := filepath.Join(tm.registryDir, "garbage.key")
	touchFile(t, garbage)
	rec.Files = append(rec.Files, garbage)
	rec.ProcStart = ""
	writeProxies(t, tm.proxiesPath, []proxyRecord{rec})
	t.Cleanup(func() { ccuds.RemoveRegistry(rec.Files) })

	tm.sweepOK(t)
	if !allExist(rec.Files...) {
		t.Fatalf("a record without proc_start unlinked files")
	}
	if proxyhelpertest.Exists(rec.Sock) {
		t.Fatalf("dead sock kept") // the sock is still only guarded by dialRefused
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v", recs)
	}
}

func TestSweep_ReusedPidLiveListenerKept(t *testing.T) {
	tm := newTestManager(t)
	rec := sweepRecord(t, tm, sweepPID, sweepPS)
	// Every recorded file was rewritten by the new owner of the pid.
	for _, f := range rec.Files {
		if err := os.WriteFile(f, []byte(`{"procStart":"Mon Sep 14 09:30:00 2026"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeProxies(t, tm.proxiesPath, []proxyRecord{rec})
	tm.os.set(func() {
		tm.os.alive[sweepPID] = true
		tm.os.ps[sweepPID] = "Mon Sep 14 09:30:00 2026" // reused: different identity
		tm.os.refused[rec.Sock] = false                 // somebody listens
	})

	if err := tm.m.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if sent := tm.os.sent(); len(sent) != 0 {
		t.Fatalf("signals sent to a reused pid: %v", sent)
	}
	if !allExist(append([]string{rec.Sock}, rec.Files...)...) {
		t.Fatalf("the new owner's files or live sock were unlinked")
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v, want empty (nothing of ours is left)", recs)
	}
	t.Cleanup(func() { ccuds.RemoveRegistry(rec.Files) })
}

func TestSweep_MissingFileWritesEmpty(t *testing.T) {
	tm := newTestManager(t)
	if proxyhelpertest.Exists(tm.proxiesPath) {
		t.Fatal("precondition")
	}
	if err := tm.m.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	data, err := os.ReadFile(tm.proxiesPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "[]" {
		t.Fatalf("proxies.json = %q, want []", data)
	}
	if hasTmp(t, filepath.Dir(tm.proxiesPath)) {
		t.Fatalf(".tmp left")
	}
}

func TestSweep_UnparsableFileTreatedAsEmpty(t *testing.T) {
	tm := newTestManager(t)
	if err := os.WriteFile(tm.proxiesPath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tm.m.Sweep(); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v", recs)
	}
	if len(tm.logs.all()) == 0 {
		t.Fatalf("unparsable file not logged")
	}
	acquireOK(t, tm, originA)
}

func TestSweep_SecondCallIsNoOp(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	h := acquireOK(t, tm, originA)
	// The daemon's own helper is now in proxies.json and "alive" as far
	// as a re-entrant sweep could tell.
	tm.os.set(func() { tm.os.alive[h.pid] = true })

	if err := tm.m.Sweep(); err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if sent := tm.os.sent(); len(sent) != 0 {
		t.Fatalf("second Sweep signalled our own helper: %v", sent)
	}
	if tm.stateOf(h) != helperReady || tm.fake.Stops() != 0 {
		t.Fatalf("second Sweep disturbed the live helper")
	}
	if !allExist(append([]string{h.sock}, h.files...)...) {
		t.Fatalf("second Sweep unlinked the live helper's files")
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 1 || recs[0].PID != h.pid {
		t.Fatalf("proxies.json = %+v", recs)
	}
}

func TestSweep_UnwritablePathErrors(t *testing.T) {
	tm := newTestManager(t)
	tm.m.proxiesPath = filepath.Join(tm.registryDir, "missing", "proxies.json")
	if err := tm.m.Sweep(); err == nil {
		t.Fatalf("Sweep succeeded with an unwritable path")
	}
	_, err := tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("Acquire after failed Sweep: %v, want ErrNotReady", err)
	}
}

func TestSweep_UnresolvedOccupiesBlocksOriginAndCap(t *testing.T) {
	tm := newTestManager(t)
	rec := sweepRecord(t, tm, sweepPID, sweepPS)
	t.Cleanup(func() { ccuds.RemoveRegistry(rec.Files) })
	writeProxies(t, tm.proxiesPath, []proxyRecord{rec})
	tm.os.set(func() {
		tm.os.alive[sweepPID] = true
		tm.os.ps[sweepPID] = sweepPS // ignores both signals
	})
	tm.sweepOK(t)

	_, err := tm.m.Acquire(context.Background(), originA, "air/a", revUnapplied)
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("Acquire for the occupied origin: %v, want ErrNotReady", err)
	}
	if tm.fake.Spawns() != 0 {
		t.Fatalf("spawned for an occupied origin")
	}
	// Other origins: HelperCap-1 fit, the next is refused.
	fillHelpers(t, tm, 2, 2+HelperCap-1)
	_, err = tm.m.Acquire(context.Background(), originN(HelperCap+5), "air/x", revUnapplied)
	if !errors.Is(err, ErrProxyLimit) {
		t.Fatalf("err = %v, want ErrProxyLimit at HelperCap-1 + 1 occupying record", err)
	}
	// The occupying record survives every rewrite.
	recs := readProxies(t, tm.proxiesPath)
	found := false
	for _, r := range recs {
		if r.PID == sweepPID {
			found = true
		}
	}
	if !found || len(recs) != HelperCap {
		t.Fatalf("proxies.json has %d records, occupied record present=%v", len(recs), found)
	}
}

func TestSweep_DeadLeftoverUnlinkErrorOccupiesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	tm := newTestManager(t)
	leftover := filepath.Join(filepath.Dir(tm.registryDir), "leftover")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	files := writeRegistryFiles(t, leftover, sweepPID, sweepPS, nil)
	sock := filepath.Join(leftover, strconv.Itoa(sweepPID)+".sock")
	touchFile(t, sock)
	if err := os.Chmod(leftover, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(leftover, 0o700) })
	rec := proxyRecord{PID: sweepPID, ProcStart: sweepPS, Sock: sock, Files: files, Origin: originA}
	writeProxies(t, tm.proxiesPath, []proxyRecord{rec})
	// dead pid (alive map empty), procStart unknown
	tm.os.set(func() { tm.os.psErr[sweepPID] = errors.New("no such process") })

	tm.sweepOK(t)
	recs := readProxies(t, tm.proxiesPath)
	if len(recs) != 1 || recs[0].PID != sweepPID {
		t.Fatalf("proxies.json = %+v, want the leftover retained", recs)
	}
	if !allExist(append([]string{sock}, files...)...) {
		t.Fatalf("files vanished despite the read-only dir")
	}
	// Occupies nothing: the origin is free and the cap is untouched.
	acquireOK(t, tm, originA)
	fillHelpers(t, tm, 2, HelperCap+1)
	if tm.mapLen() != HelperCap {
		t.Fatalf("map len = %d", tm.mapLen())
	}
	_, err := tm.m.Acquire(context.Background(), originN(HelperCap+1), "air/x", revUnapplied)
	if !errors.Is(err, ErrProxyLimit) {
		t.Fatalf("err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Production seams
// ---------------------------------------------------------------------------

func TestDefaultPidAlive(t *testing.T) {
	if !defaultPidAlive(os.Getpid()) {
		t.Errorf("own pid reported dead")
	}
	if defaultPidAlive(1<<30 - 1) {
		t.Errorf("absurd pid reported alive")
	}
}

func TestNewHelperManagerDefaults(t *testing.T) {
	m := newHelperManager(helperManagerConfig{ProxiesPath: "/nope/proxies.json"})
	if m.now == nil || m.procStart == nil || m.pidAlive == nil || m.dialRefused == nil || m.signal == nil || m.log == nil || m.liveEntries == nil || m.rewriteName == nil {
		t.Fatalf("nil seam after defaults: %+v", m)
	}
	if m.readyTimeout != HelperReadyTimeout || m.termGrace != HelperTermGrace {
		t.Fatalf("timeouts = %v / %v", m.readyTimeout, m.termGrace)
	}
	if m.helpers == nil || m.procCtx == nil {
		t.Fatalf("map or ctx not initialised")
	}
	m.Stop()
	select {
	case <-m.procCtx.Done():
	default:
		t.Fatalf("procCtx not cancelled by Stop")
	}
}

// ---------------------------------------------------------------------------
// ApplyAddress: the helper's name follows the sender's address in place
// (Peer Address v2 spec §3.5), gated by the monotonic address_rev.
// ---------------------------------------------------------------------------

var applyKey = originN(50)

// registryName reads the "name" field of <dir>/<pid>.json; "<missing>"
// when the file is gone.
func registryName(t *testing.T, dir string, pid int) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(pid)+".json"))
	if err != nil {
		return "<missing>"
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("registry file %d.json: %v", pid, err)
	}
	var name string
	if err := json.Unmarshal(m["name"], &name); err != nil {
		t.Fatalf("registry file %d.json name: %v", pid, err)
	}
	return name
}

// appliedRevOf reads h.appliedRev under the manager lock.
func (tm *testManager) appliedRevOf(h *helper) int64 {
	tm.m.mu.Lock()
	defer tm.m.mu.Unlock()
	return h.appliedRev
}

func TestApplyAddress_RenameInPlaceAndMonotonic(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	h, err := tm.m.Acquire(context.Background(), applyKey, "a/purdex-x:s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	sock, pid := h.sock, h.pid
	if got := tm.appliedRevOf(h); got != 10 {
		t.Fatalf("appliedRev after spawn = %d, want 10 (stored at admission)", got)
	}
	if got := registryName(t, tm.registryDir, pid); got != "a/purdex-x:s1" {
		t.Fatalf("spawned registry name = %q, want a/purdex-x:s1", got)
	}
	if got := tm.m.Name(h); got != "a/purdex-x:s1" {
		t.Errorf("Name = %q, want a/purdex-x:s1", got)
	}

	// Same rev again: no-op.
	if got := tm.m.ApplyAddress(h, "a/purdex-x:s1", 10); got != "a/purdex-x:s1" {
		t.Errorf("same rev: got %q", got)
	}
	// Newer rev, new name: rewritten in place, same socket and pid.
	if got := tm.m.ApplyAddress(h, "a/purdex-y:s1", 20); got != "a/purdex-y:s1" || registryName(t, tm.registryDir, pid) != "a/purdex-y:s1" {
		t.Errorf("rename: got %q file %q", got, registryName(t, tm.registryDir, pid))
	}
	if got := tm.m.Name(h); got != "a/purdex-y:s1" {
		t.Errorf("Name after rename = %q, want a/purdex-y:s1", got)
	}
	if h.sock != sock || h.pid != pid {
		t.Error("instance changed")
	}
	if got := tm.appliedRevOf(h); got != 20 {
		t.Errorf("appliedRev after rename = %d, want 20", got)
	}
	// Older rev: ignored.
	if got := tm.m.ApplyAddress(h, "a/purdex-z:s1", 15); got != "a/purdex-y:s1" {
		t.Errorf("older rev applied: %q", got)
	}
	if got := registryName(t, tm.registryDir, pid); got != "a/purdex-y:s1" {
		t.Errorf("older rev rewrote the file: %q", got)
	}
	// A→B→A: (A,30) same name advances rev; late (B,25) must not win.
	if got := tm.m.ApplyAddress(h, "a/purdex-y:s1", 30); got != "a/purdex-y:s1" {
		t.Errorf("same name at rev 30: %q", got)
	}
	if got := tm.appliedRevOf(h); got != 30 {
		t.Errorf("appliedRev after same-name apply = %d, want 30 (advances even when the name is equal)", got)
	}
	if got := tm.m.ApplyAddress(h, "a/purdex-b:s1", 25); got != "a/purdex-y:s1" {
		t.Errorf("A→B→A hole: %q", got)
	}
	// Released ⇒ skipped, and the file cleanup is not undone.
	tm.m.Release(h, "test")
	if got := tm.m.ApplyAddress(h, "a/purdex-q:s1", 99); got != "a/purdex-y:s1" {
		t.Errorf("rename on a released instance: %q", got)
	}
	if got := registryName(t, tm.registryDir, pid); got != "<missing>" {
		t.Errorf("rename after release recreated the registry file: %q", got)
	}
	if got := tm.appliedRevOf(h); got != 30 {
		t.Errorf("appliedRev advanced on a released instance: %d", got)
	}
}

func TestApplyAddress_LegacySpawnTakesFirstV2AddressEvenAtRevZero(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	h, err := tm.m.Acquire(context.Background(), applyKey, "a/mt1", revUnapplied)
	if err != nil {
		t.Fatal(err)
	}
	if got := tm.appliedRevOf(h); got != revUnapplied {
		t.Fatalf("appliedRev of a v1 spawn = %d, want revUnapplied", got)
	}
	// A v2 sender whose session has never had a label row (rev 0) must
	// still be able to name the helper once.
	if got := tm.m.ApplyAddress(h, "a/_k3x9qz:mt1-n", 0); got != "a/_k3x9qz:mt1-n" {
		t.Errorf("first v2 address at rev 0 not applied: %q", got)
	}
	if got := registryName(t, tm.registryDir, h.pid); got != "a/_k3x9qz:mt1-n" {
		t.Errorf("registry name = %q, want a/_k3x9qz:mt1-n", got)
	}
	// From then on the monotonic rule holds: rev 0 again is ignored.
	if got := tm.m.ApplyAddress(h, "a/other:x", 0); got != "a/_k3x9qz:mt1-n" {
		t.Errorf("second rev-0 request applied: %q", got)
	}
}

// TestApplyAddress_SuffixChangeAtSameRevIsIgnored pins the limit the
// address design accepts on purpose, so that changing it is a deliberate
// decision rather than an accident.
//
// The canonical head never moves. The SUFFIX does: it renders the live
// tmux session name, so renaming the sender's tmux session takes its
// address from "a/_1c4m7dkz:mt1-n" to "a/_1c4m7dkz:mt2-n" — at rev 0
// throughout, because address_rev never follows a display change.
// ApplyAddress ignores any rev <= appliedRev, so the remote host keeps
// showing the OLD helper display name until the helper is rebuilt. Only
// that display name goes stale: the head a sender is reached at is
// unchanged, and addressing, resolution and delivery all read the live
// listing anyway.
func TestApplyAddress_SuffixChangeAtSameRevIsIgnored(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	// The sender lives in tmux "mt1", so its suffix is built from that
	// session name; its head is its canonical id.
	h, err := tm.m.Acquire(context.Background(), applyKey, "a/_1c4m7dkz:mt1-n", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := tm.appliedRevOf(h); got != 0 {
		t.Fatalf("appliedRev after spawn = %d, want 0", got)
	}

	// The tmux session is renamed to "mt2". The suffix changes with it;
	// the rev does not. The accepted limit: the helper keeps the old name.
	if got := tm.m.ApplyAddress(h, "a/_1c4m7dkz:mt2-n", 0); got != "a/_1c4m7dkz:mt1-n" {
		t.Errorf("suffix change at rev 0 = %q, want the stale %q", got, "a/_1c4m7dkz:mt1-n")
	}
	if got := registryName(t, tm.registryDir, h.pid); got != "a/_1c4m7dkz:mt1-n" {
		t.Errorf("registry name = %q, want the stale %q", got, "a/_1c4m7dkz:mt1-n")
	}

	// A strictly newer rev is still the one thing that does rewrite the
	// name — the gate itself is unchanged, only the inputs that can move
	// underneath it.
	if got := tm.m.ApplyAddress(h, "a/_1c4m7dkz:mt2-n", 1); got != "a/_1c4m7dkz:mt2-n" {
		t.Errorf("rev 1 = %q, want it applied", got)
	}
	if got := registryName(t, tm.registryDir, h.pid); got != "a/_1c4m7dkz:mt2-n" {
		t.Errorf("registry name after the newer rev = %q", got)
	}
}

func TestApplyAddress_SpawnKeepsRevWhenWaiterCancels(t *testing.T) {
	// The waiter that admitted the spawn (rev 10, name X) leaves before
	// the helper is ready; a later request at rev 5 that joined the same
	// spawn must not rename it: the revision is stored at admission.
	tm := newTestManager(t, withVariant(proxyhelpertest.Barrier))
	tm.sweepOK(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tm.m.Acquire(ctx, applyKey, "a/x:s", 10); err == nil {
		t.Fatal("expected the cancelled waiter to fail")
	}
	if n := tm.mapLen(); n != 1 {
		t.Fatalf("instances = %d, want the admitted spawn still starting", n)
	}
	go tm.fake.Release()
	h, err := tm.m.Acquire(context.Background(), applyKey, "a/y:s", 5) // joins the in-flight spawn
	if err != nil {
		t.Fatal(err)
	}
	if got := tm.m.Name(h); got != "a/x:s" {
		t.Errorf("spawned name = %q, want the admitting request's a/x:s", got)
	}
	if got := tm.appliedRevOf(h); got != 10 {
		t.Errorf("appliedRev = %d, want the admitting request's 10", got)
	}
	if got := tm.m.ApplyAddress(h, "a/y:s", 5); got != "a/x:s" {
		t.Errorf("rev 5 renamed a rev-10 instance: %q", got)
	}
	if got := registryName(t, tm.registryDir, h.pid); got != "a/x:s" {
		t.Errorf("registry name = %q, want a/x:s", got)
	}
}

func TestApplyAddress_RewriteFailureRollsBack(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	h, err := tm.m.Acquire(context.Background(), applyKey, "a/x:s", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(tm.registryDir, strconv.Itoa(h.pid)+".json")); err != nil { // make the rewrite fail
		t.Fatal(err)
	}
	if got := tm.m.ApplyAddress(h, "a/y:s", 5); got != "a/x:s" {
		t.Errorf("got %q", got)
	}
	if got := tm.m.Name(h); got != "a/x:s" {
		t.Errorf("Name after a failed rewrite = %q, want a/x:s", got)
	}
	if got := tm.appliedRevOf(h); got != 1 {
		t.Errorf("appliedRev = %d after a failed rewrite, want 1", got)
	}
	if !tm.logs.contains("rename") {
		t.Errorf("no log line for the failed rewrite; logs=%q", tm.logs.all())
	}
	// The instance is untouched: once the file is back, the same newer
	// request renames it (the rolled-back revision did not burn rev 5).
	if _, err := ccuds.WriteRegistry(tm.registryDir, ccuds.RegistryEntry{
		PID: h.pid, SessionID: "restored", Name: "a/x:s", Cwd: tm.registryDir,
		ProcStart: h.procStart, Version: "2.1.270", Inbox: h.sock,
	}, "restored-token"); err != nil {
		t.Fatal(err)
	}
	if got := tm.m.ApplyAddress(h, "a/y:s", 5); got != "a/y:s" {
		t.Errorf("rename after the file came back: %q", got)
	}
	if got := registryName(t, tm.registryDir, h.pid); got != "a/y:s" {
		t.Errorf("registry name = %q, want a/y:s", got)
	}
}

// ---------------------------------------------------------------------------
// ApplyAddress: the registry rewrite runs outside the manager lock (PR
// #1028 R2 Y1). The rewrite seam (m.rewriteName) lets a test park one
// rename mid-I/O and watch what the rest of the manager does meanwhile.
// ---------------------------------------------------------------------------

// gatedRewrite is a rewriteName seam whose FIRST call parks on gate after
// signalling entered (carrying the requested name); every call, once
// through the gate, runs after (the real rewrite by default). calls
// counts entries.
type gatedRewrite struct {
	entered chan string
	gate    chan struct{}
	after   func(dir string, pid int, name string, since int64) error
	first   atomic.Bool
	calls   atomic.Int32
}

func newGatedRewrite() *gatedRewrite {
	return &gatedRewrite{entered: make(chan string, 1), gate: make(chan struct{}), after: ccuds.RewriteRegistryName}
}

func (g *gatedRewrite) fn(dir string, pid int, name string, since int64) error {
	g.calls.Add(1)
	if g.first.CompareAndSwap(false, true) {
		g.entered <- name
		<-g.gate
	}
	return g.after(dir, pid, name, since)
}

// awaitEntered returns the name the parked rewrite was asked for.
func (g *gatedRewrite) awaitEntered(t *testing.T) string {
	t.Helper()
	select {
	case name := <-g.entered:
		return name
	case <-time.After(3 * time.Second):
		t.Fatalf("the rewrite was not entered within 3 s")
		return ""
	}
}

// promptly runs fn on its own goroutine and fails unless it returns within d.
func promptly(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	waitClosed(t, done, d, what)
}

func TestApplyAddress_RewriteDoesNotBlockOtherOrigins(t *testing.T) {
	tm := newTestManager(t)
	g := newGatedRewrite()
	tm.m.rewriteName = g.fn
	tm.sweepOK(t)
	hA, err := tm.m.Acquire(context.Background(), applyKey, "a/x:s", 1)
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan string, 1)
	resultDone := make(chan struct{})
	go func() { result <- tm.m.ApplyAddress(hA, "a/y:s", 5); close(resultDone) }()
	if got := g.awaitEntered(t); got != "a/y:s" {
		t.Fatalf("rewrite entered with %q, want a/y:s", got)
	}

	// While A's rewrite is stuck on "disk", every other manager operation
	// — another origin's spawn included — must go through.
	const bound = 2 * time.Second
	other := originN(51)
	promptly(t, bound, "Acquire of another origin during a stuck rewrite", func() {
		ctx, cancel := context.WithTimeout(context.Background(), bound)
		defer cancel()
		if _, err := tm.m.Acquire(ctx, other, "a/other:s", 1); err != nil {
			t.Errorf("Acquire(other): %v", err)
		}
	})
	promptly(t, bound, "ProxyPIDs during a stuck rewrite", func() {
		if pids := tm.m.ProxyPIDs(); len(pids) != 2 {
			t.Errorf("ProxyPIDs = %v, want 2 entries", pids)
		}
	})
	promptly(t, bound, "Name during a stuck rewrite", func() {
		if got := tm.m.Name(hA); got != "a/x:s" {
			t.Errorf("Name mid-rewrite = %q, want the current a/x:s", got)
		}
	})
	promptly(t, bound, "Touch/ReapIdle during a stuck rewrite", func() {
		tm.m.Touch(applyKey)
		tm.m.ReapIdle()
	})
	stillBlocked(t, resultDone, 100*time.Millisecond, "ApplyAddress with the rewrite parked")

	close(g.gate)
	select {
	case got := <-result:
		if got != "a/y:s" {
			t.Errorf("ApplyAddress = %q, want a/y:s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ApplyAddress did not return after the rewrite was released")
	}
	if got := registryName(t, tm.registryDir, hA.pid); got != "a/y:s" {
		t.Errorf("registry name = %q, want a/y:s", got)
	}
	if got := tm.m.Name(hA); got != "a/y:s" {
		t.Errorf("Name = %q, want a/y:s", got)
	}
	if got := tm.appliedRevOf(hA); got != 5 {
		t.Errorf("appliedRev = %d, want 5", got)
	}
}

// TestApplyAddress_ReleaseWaitsForInFlightRewrite: a rename whose
// rename(2) lands after the helper's own cleanup would recreate
// <pid>.json. Release therefore waits for an in-flight rewrite
// (h.renameMu) after Stop and before unlinkOwned, so the recreated file
// is unlinked as ours — never left behind as an orphan.
func TestApplyAddress_ReleaseWaitsForInFlightRewrite(t *testing.T) {
	tm := newTestManager(t)
	g := newGatedRewrite()
	tm.m.rewriteName = g.fn
	tm.sweepOK(t)
	h, err := tm.m.Acquire(context.Background(), applyKey, "a/x:s", 1)
	if err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(tm.registryDir, strconv.Itoa(h.pid)+".json")
	// Once through the gate the rewrite "lands": the helper's cleanup has
	// removed the file by then, so the rename recreates it with our
	// identity (what os.Rename of the temp over a vanished path does).
	g.after = func(dir string, pid int, name string, since int64) error {
		return os.WriteFile(jsonPath, []byte(`{"pid":`+strconv.Itoa(pid)+`,"procStart":"`+h.procStart+`","name":"`+name+`"}`), 0o644)
	}

	result := make(chan string, 1)
	go func() { result <- tm.m.ApplyAddress(h, "a/y:s", 5) }()
	g.awaitEntered(t)
	if !proxyhelpertest.Exists(jsonPath) {
		t.Fatal("registry file missing while the instance is ready")
	}

	released := make(chan struct{})
	go func() { tm.m.Release(h, "test"); close(released) }()
	// Stop ran (the helper removed its own files); Release is now parked
	// on the rename lock: the instance is still stopping, still in the
	// map, and the exit has not been announced.
	eventually(t, 3*time.Second, func() bool { return noneExist(h.files...) }, "helper removed its own files")
	stillBlocked(t, released, 200*time.Millisecond, "Release with a rewrite in flight")
	if st := tm.stateOf(h); st != helperStopping || tm.mapLen() != 1 {
		t.Errorf("state=%v map=%d while the in-flight rewrite is parked, want stopping/1", st, tm.mapLen())
	}
	select {
	case <-h.exited:
		t.Error("exited closed before the in-flight rewrite finished")
	default:
	}

	close(g.gate)
	select {
	case got := <-result:
		if got != "a/x:s" {
			t.Errorf("ApplyAddress on an instance that stopped mid-rewrite = %q, want the current a/x:s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ApplyAddress did not return")
	}
	waitClosed(t, released, 3*time.Second, "Release")
	if proxyhelpertest.Exists(jsonPath) {
		t.Errorf("registry file recreated by the in-flight rewrite was left behind")
	}
	if hasTmp(t, tm.registryDir) {
		t.Errorf("temp file left behind")
	}
	if got := tm.m.Name(h); got != "a/x:s" {
		t.Errorf("Name after release = %q, want a/x:s", got)
	}
	if tm.unresolvedLen() != 0 || tm.mapLen() != 0 {
		t.Errorf("unresolved=%d map=%d after release", tm.unresolvedLen(), tm.mapLen())
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Errorf("proxies.json = %+v, want empty", recs)
	}
	// A rename that starts after the release skips at the state check and
	// touches no file.
	if got := tm.m.ApplyAddress(h, "a/z:s", 9); got != "a/x:s" {
		t.Errorf("rename after release = %q", got)
	}
	if proxyhelpertest.Exists(jsonPath) || g.calls.Load() != 1 {
		t.Errorf("rename after release touched the disk (calls=%d, exists=%v)", g.calls.Load(), proxyhelpertest.Exists(jsonPath))
	}
}

// TestApplyAddress_ConcurrentRevsConverge: two requests for one instance
// (rev 20 and rev 30) racing through ApplyAddress end with the rev-30
// name on the instance and on disk, whichever enters first.
func TestApplyAddress_ConcurrentRevsConverge(t *testing.T) {
	for _, c := range []struct {
		name       string
		first      int64 // the rev whose rewrite is parked first
		second     int64
		wantWrites int32 // 30-then-20: the late 20 never rewrites
	}{
		{"20 then 30", 20, 30, 2},
		{"30 then 20", 30, 20, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			tm := newTestManager(t)
			g := newGatedRewrite()
			tm.m.rewriteName = g.fn
			tm.sweepOK(t)
			h, err := tm.m.Acquire(context.Background(), applyKey, "a/x:s", 1)
			if err != nil {
				t.Fatal(err)
			}
			nameOf := func(rev int64) string { return fmt.Sprintf("a/r%d:s", rev) }

			var wg sync.WaitGroup
			var rmu sync.Mutex
			results := make(map[int64]string)
			apply := func(rev int64) {
				defer wg.Done()
				got := tm.m.ApplyAddress(h, nameOf(rev), rev)
				rmu.Lock()
				results[rev] = got
				rmu.Unlock()
			}
			wg.Add(1)
			go apply(c.first)
			if got := g.awaitEntered(t); got != nameOf(c.first) {
				t.Fatalf("first rewrite entered with %q", got)
			}
			wg.Add(1)
			go apply(c.second) // serialised behind the parked one on h.renameMu
			time.Sleep(50 * time.Millisecond)
			close(g.gate)
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			waitClosed(t, done, 3*time.Second, "both ApplyAddress calls")

			if got := tm.m.Name(h); got != nameOf(30) {
				t.Errorf("Name = %q, want %s", got, nameOf(30))
			}
			if got := registryName(t, tm.registryDir, h.pid); got != nameOf(30) {
				t.Errorf("registry name = %q, want %s", got, nameOf(30))
			}
			if got := tm.appliedRevOf(h); got != 30 {
				t.Errorf("appliedRev = %d, want 30", got)
			}
			if results[30] != nameOf(30) {
				t.Errorf("rev-30 request got %q", results[30])
			}
			if results[20] != nameOf(c.first) { // 20-first: its own name; 30-first: the newer name
				t.Errorf("rev-20 request got %q, want %s", results[20], nameOf(c.first))
			}
			if n := g.calls.Load(); n != c.wantWrites {
				t.Errorf("rewrites = %d, want %d", n, c.wantWrites)
			}
		})
	}
}
