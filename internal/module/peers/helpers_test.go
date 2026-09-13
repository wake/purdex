package peers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	h, err := tm.m.Acquire(ctx, key, "air/"+key.AgentSessionID[len(key.AgentSessionID)-4:])
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
			h, err := tm.m.Acquire(context.Background(), originA, "air/a")
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
	_, err := tm.m.Acquire(context.Background(), originN(HelperCap+1), "air/x")
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

	go tm.m.Acquire(context.Background(), originN(HelperCap), "air/starting")
	eventually(t, time.Second, func() bool { return barrier.Spawns() == 1 }, "starting spawn")

	_, err := tm.m.Acquire(context.Background(), originN(HelperCap+1), "air/x")
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

	_, err := tm.m.Acquire(context.Background(), originN(HelperCap+1), "air/x")
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
			_, err := tm.m.Acquire(context.Background(), originA, "air/a")
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
	_, err := tm.m.Acquire(context.Background(), originA, "air/a")
	if !errors.Is(err, ErrProxySpawnFailed) {
		t.Fatalf("err = %v, want ErrProxySpawnFailed", err)
	}
	if tm.fake.Spawns() != 1 || tm.mapLen() != 0 {
		t.Fatalf("spawns=%d map=%d", tm.fake.Spawns(), tm.mapLen())
	}
	// The caller decides to retry: a fresh Acquire spawns again.
	_, err = tm.m.Acquire(context.Background(), originA, "air/a")
	if !errors.Is(err, ErrProxySpawnFailed) || tm.fake.Spawns() != 2 {
		t.Fatalf("retry: err=%v spawns=%d", err, tm.fake.Spawns())
	}
}

func TestHelperManager_ProcStartFailureRollsBack(t *testing.T) {
	tm := newTestManager(t)
	tm.sweepOK(t)
	pid := proxyhelpertest.PeekPID()
	tm.os.set(func() { tm.os.psErr[pid] = errors.New("ps: boom") })

	_, err := tm.m.Acquire(context.Background(), originA, "air/a")
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

	_, err := tm.m.Acquire(context.Background(), originA, "air/a")
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
		_, err := tm.m.Acquire(ctx, originA, "air/a")
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
		h2, acqErr = tm.m.Acquire(context.Background(), originA, "air/a")
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
	_, err := tm.m.Acquire(context.Background(), originA, "air/a")
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
	_, err := tm.m.Acquire(context.Background(), originA, "air/a")
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
		_, err := tm.m.Acquire(context.Background(), originA, "air/a")
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
	_, err := tm.m.Acquire(context.Background(), originA, "air/a")
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

	_, err := tm.m.Acquire(context.Background(), originA, "air/a")
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("Acquire for the occupied origin: %v, want ErrNotReady", err)
	}
	if tm.fake.Spawns() != 0 {
		t.Fatalf("spawned for an occupied origin")
	}
	// Other origins: HelperCap-1 fit, the next is refused.
	fillHelpers(t, tm, 2, 2+HelperCap-1)
	_, err = tm.m.Acquire(context.Background(), originN(HelperCap+5), "air/x")
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
	_, err := tm.m.Acquire(context.Background(), originN(HelperCap+1), "air/x")
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
	if m.now == nil || m.procStart == nil || m.pidAlive == nil || m.dialRefused == nil || m.signal == nil || m.log == nil || m.liveEntries == nil {
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
