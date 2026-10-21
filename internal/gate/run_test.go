package gate

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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePressure is a PressureReader the test steers.
type fakePressure struct {
	mu    sync.Mutex
	p     Pressure
	err   error
	reads int
}

func (f *fakePressure) set(p Pressure, err error) { f.mu.Lock(); f.p, f.err = p, err; f.mu.Unlock() }
func (f *fakePressure) Read() (Pressure, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.p, f.err
}

// safeBuf is a stderr the gate and the test can both touch.
type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *safeBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// testGate is a gate over a temp directory with two heavy slots, no QoS wrapper, no pressure and quick polling.
func testGate(t *testing.T) (*Gate, *safeBuf) {
	t.Helper()
	cfg := Defaults()
	cfg.Dir = t.TempDir()
	cfg.Slots.Heavy, cfg.Slots.Light = 2, 1
	cfg.Priority.Mode = "none"
	cfg.Admission.DiskMinGB = 0
	cfg.Parallelism = ParallelismConfig{Mode: "fixed", ByClass: map[Class]ParallelismOverride{}, GoP: Param{Auto: true}, VitestWorkers: Param{Auto: true}, PnpmWorkspace: Param{Auto: true}}
	cfg.Cache.Count1 = "off"
	g := New(cfg)
	g.Tools = Tools{}
	g.Pressure = nil
	g.Facts = func() HostFacts { return HostFacts{NCPU: 10, AvailMemMB: 8000} }
	g.Poll = func() time.Duration { return 10 * time.Millisecond }
	g.KillGrace = 300 * time.Millisecond
	g.TreeEvery = 30 * time.Millisecond
	g.IsTTY = func() bool { return false }
	buf := &safeBuf{}
	g.Stderr = buf
	return g, buf
}

// cleanEnv is the environment of a gated command in a test: no gate claim, no GOFLAGS of the host.
func cleanEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, HeldEnv+"=") || strings.HasPrefix(kv, "GOFLAGS=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func sh(script string) []string { return []string{"sh", "-c", script} }

// holdScript marks that it started and waits until the test creates the go file.
func holdScript(dir, n string) string {
	return fmt.Sprintf("touch %s/started-%s; while [ ! -e %s/go-%s ]; do sleep 0.02; done", dir, n, dir, n)
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	eventuallyFor(t, 5*time.Second, what, cond)
}

// eventuallyFor waits up to d for cond: a test whose condition needs a process of another host to get going (a loaded machine takes
// seconds for that) waits for the event, with a long limit that only a hang reaches.
func eventuallyFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func never(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			t.Fatalf("%s happened", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type runResult struct {
	code int
	out  string
}

// runAsync runs the command in a goroutine and gives the result channel.
func runAsync(g *Gate, ctx context.Context, o Options) <-chan runResult {
	ch := make(chan runResult, 1)
	go func() {
		var out bytes.Buffer
		o.Stdout = &out
		if o.Env == nil {
			o.Env = cleanEnv()
		}
		code, _ := g.Run(ctx, o)
		ch <- runResult{code, out.String()}
	}()
	return ch
}

func runSync(t *testing.T, g *Gate, o Options) runResult {
	t.Helper()
	select {
	case r := <-runAsync(g, context.Background(), o):
		return r
	case <-time.After(20 * time.Second):
		t.Fatal("the run did not finish")
		return runResult{}
	}
}

func TestRun_ExitCodeAndOutputPass(t *testing.T) {
	g, _ := testGate(t)
	r := runSync(t, g, Options{Argv: sh("echo hello; exit 7"), Class: ClassHeavy})
	assert.Equal(t, 7, r.code)
	assert.Equal(t, "hello\n", r.out)
	r = runSync(t, g, Options{Argv: sh("kill -TERM $$"), Class: ClassHeavy})
	assert.Equal(t, 128+int(syscall.SIGTERM), r.code, "a command killed by a signal exits 128+n")
}

func TestRun_NoCommandOrNoBinary(t *testing.T) {
	g, _ := testGate(t)
	code, err := g.Run(context.Background(), Options{})
	assert.Equal(t, 2, code)
	assert.Error(t, err)
	code, err = g.Run(context.Background(), Options{Argv: []string{"definitely-not-a-binary-pdx"}, Class: ClassHeavy, Env: cleanEnv()})
	assert.Equal(t, 127, code)
	assert.Error(t, err)
}

func TestRun_HoldsASlotWhileItRunsAndRecordsIt(t *testing.T) {
	g, _ := testGate(t)
	dir := t.TempDir()
	res := runAsync(g, context.Background(), Options{Argv: sh(holdScript(dir, "1")), Class: ClassHeavy})
	eventually(t, "the command to start", func() bool { return exists(dir + "/started-1") })
	st := g.Status()
	require.Len(t, st.Heavy, 1)
	h := st.Heavy[0]
	assert.Equal(t, ClassHeavy, h.Class)
	assert.Equal(t, os.Getpid(), h.PID)
	assert.Equal(t, "sh", h.Argv[0])
	assert.Empty(t, h.Token, "the token is never shown")
	assert.False(t, h.Orphaned)
	assert.Empty(t, st.Light)
	require.NoError(t, os.WriteFile(dir+"/go-1", nil, 0o644))
	assert.Equal(t, 0, (<-res).code)
	assert.Empty(t, g.Status().Heavy, "the slot is free when the command is done")
}

func TestRun_AThirdHeavyCommandWaitsForASlot(t *testing.T) {
	g, buf := testGate(t) // two heavy slots
	dir := t.TempDir()
	var res []<-chan runResult
	for _, n := range []string{"1", "2"} {
		res = append(res, runAsync(g, context.Background(), Options{Argv: sh(holdScript(dir, n)), Class: ClassHeavy}))
		eventually(t, "command "+n, func() bool { return exists(dir + "/started-" + n) })
	}
	res = append(res, runAsync(g, context.Background(), Options{Argv: sh(holdScript(dir, "3")), Class: ClassHeavy}))
	never(t, "the third command starting", 300*time.Millisecond, func() bool { return exists(dir + "/started-3") })
	assert.Equal(t, 1, g.Status().WaitHeavy)
	assert.Contains(t, buf.String(), "waiting for a heavy slot")
	require.NoError(t, os.WriteFile(dir+"/go-1", nil, 0o644))
	eventually(t, "the third command to start", func() bool { return exists(dir + "/started-3") })
	for _, n := range []string{"2", "3"} {
		require.NoError(t, os.WriteFile(dir+"/go-"+n, nil, 0o644))
	}
	for _, c := range res {
		assert.Equal(t, 0, (<-c).code)
	}
}

// The older waiter starts first even when the younger polls faster (nq order).
// (M: let TryTake ignore the tickets before it.)
func TestRun_WaitersStartInArrivalOrder(t *testing.T) {
	g1, _ := testGate(t)
	g1.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	g1.Poll = func() time.Duration { return 60 * time.Millisecond } // the older one looks rarely
	g2 := *g1
	g2.Poll = func() time.Duration { return time.Millisecond } // the younger looks all the time
	dir := t.TempDir()
	occupant := runAsync(g1, context.Background(), Options{Argv: sh(holdScript(dir, "0")), Class: ClassHeavy})
	eventually(t, "the occupant", func() bool { return exists(dir + "/started-0") })
	first := runAsync(g1, context.Background(), Options{Argv: sh(holdScript(dir, "1")), Class: ClassHeavy})
	eventually(t, "the first ticket", func() bool { return g1.Status().WaitHeavy == 1 })
	second := runAsync(&g2, context.Background(), Options{Argv: sh(holdScript(dir, "2")), Class: ClassHeavy})
	eventually(t, "the second ticket", func() bool { return g1.Status().WaitHeavy == 2 })
	require.NoError(t, os.WriteFile(dir+"/go-0", nil, 0o644))
	eventually(t, "someone to start", func() bool { return exists(dir+"/started-1") || exists(dir+"/started-2") })
	assert.True(t, exists(dir+"/started-1"), "the older waiter took the freed slot")
	assert.False(t, exists(dir+"/started-2"))
	require.NoError(t, os.WriteFile(dir+"/go-1", nil, 0o644))
	eventually(t, "the second to start", func() bool { return exists(dir + "/started-2") })
	require.NoError(t, os.WriteFile(dir+"/go-2", nil, 0o644))
	for _, c := range []<-chan runResult{occupant, first, second} {
		assert.Equal(t, 0, (<-c).code)
	}
}

func TestRun_TheEnvironmentCarriesTheCapsAndTheClaim(t *testing.T) {
	g, buf := testGate(t)
	r := runSync(t, g, Options{Argv: sh(`echo "$GOFLAGS|$PDX_GATE_HELD"`), Class: ClassHeavy})
	parts := strings.Split(strings.TrimSpace(r.out), "|")
	require.Len(t, parts, 2)
	assert.Equal(t, "-p=3", parts[0])
	assert.Regexp(t, `^heavy-1:[0-9a-f]{24}$`, parts[1])
	assert.NotContains(t, buf.String(), "limits added", "sh is not a command whose line gets a cap named")
}

func TestRun_TheLockIsInheritedAsFd3(t *testing.T) {
	g, _ := testGate(t)
	// fd 3 is open in the command and is the slot's lock: a second flock on it from the command's side is the same file
	r := runSync(t, g, Options{Argv: sh(`ls -l /dev/fd/3 >/dev/null 2>&1 && echo open || echo closed`), Class: ClassHeavy})
	assert.Equal(t, "open\n", r.out)
}

// ---- passing through ----

func TestRun_OffAndNoneAreNotHeld(t *testing.T) {
	g, _ := testGate(t)
	dir := t.TempDir()
	g.SetConfig(func(c *Config) { c.Enabled = false })
	res := runAsync(g, context.Background(), Options{Argv: sh(holdScript(dir, "1")), Class: ClassHeavy})
	eventually(t, "start", func() bool { return exists(dir + "/started-1") })
	assert.Empty(t, g.Status().Heavy, "a gate that is off takes no slot")
	require.NoError(t, os.WriteFile(dir+"/go-1", nil, 0o644))
	assert.Equal(t, 0, (<-res).code)

	g.SetConfig(func(c *Config) { c.Enabled = true })
	res = runAsync(g, context.Background(), Options{Argv: sh(holdScript(dir, "2"))}) // class from the words: sh is none
	eventually(t, "start", func() bool { return exists(dir + "/started-2") })
	assert.Empty(t, g.Status().Heavy)
	require.NoError(t, os.WriteFile(dir+"/go-2", nil, 0o644))
	assert.Equal(t, 0, (<-res).code)
}

// outer is a gate with a command in a slot, and the claim a command below it carries.
type outer struct {
	g       *Gate
	env     string
	release func()
	done    <-chan runResult
	dir     string
}

func startOuter(t *testing.T, g *Gate, class Class, pid int) *outer {
	t.Helper()
	og := *g
	og.PID = func() int { return pid }
	dir := t.TempDir()
	done := runAsync(&og, context.Background(), Options{Argv: sh(holdScript(dir, "o")), Class: class})
	eventually(t, "the outer command", func() bool { return exists(dir + "/started-o") })
	pool := g.pool(g.Config(), class)
	h, locked, ok := pool.Record(1)
	require.True(t, ok && locked)
	return &outer{g: g, env: HeldEnv + "=" + string(class) + "-1:" + h.Token, dir: dir, done: done,
		release: func() { _ = os.WriteFile(dir+"/go-o", nil, 0o644) }}
}

// descendantProcs describes a tree in which innerPID is a grandchild of outerPID.
func descendantProcs(innerPID, outerPID int) Procs {
	return func() (map[int]procInfo, error) {
		return map[int]procInfo{innerPID: {PPID: 1500}, 1500: {PPID: outerPID}, outerPID: {PPID: 5}, 5: {PPID: 1}}, nil
	}
}

// inner runs a command below the claim in a second gate (its own pid), with a deadline so a command that is held
// back returns 130 instead of waiting.
func (o *outer) inner(t *testing.T, class Class, innerPID int, procs Procs, env string) (code int, started bool) {
	t.Helper()
	ig := *o.g
	ig.PID = func() int { return innerPID }
	ig.Procs = procs
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	c, _ := ig.Run(ctx, Options{Argv: sh("touch " + dir + "/ran"), Class: class, Env: cleanEnv(env), Stdout: &out})
	return c, exists(dir + "/ran")
}

func TestRun_AValidClaimPassesThroughWithoutASlot(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	o := startOuter(t, g, ClassHeavy, 1001)
	defer func() { o.release(); <-o.done }()
	code, ran := o.inner(t, ClassHeavy, 2002, descendantProcs(2002, 1001), o.env)
	assert.True(t, ran, "the only heavy slot is taken, and a command below it still runs")
	assert.Equal(t, 0, code)
	// a heavy slot covers a light command below it
	code, ran = o.inner(t, ClassLight, 2002, descendantProcs(2002, 1001), o.env)
	assert.True(t, ran)
	assert.Equal(t, 0, code)
	assert.Empty(t, g.Status().Light, "heavy to light takes no light slot")
}

// The claim is checked, not believed. Each of these would wait for the held slot (code 130, not run).
// (M: pass through on the variable alone; M: do not compare the token; M: do not look at the ancestors.)
func TestRun_AClaimThatIsNotTrueTakesNoOnePast(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	o := startOuter(t, g, ClassHeavy, 1001)
	defer func() { o.release(); <-o.done }()
	ok := descendantProcs(2002, 1001)
	for name, c := range map[string]struct {
		env   string
		procs Procs
	}{
		"forged token":              {HeldEnv + "=heavy-1:deadbeefdeadbeefdeadbeef", ok},
		"no token":                  {HeldEnv + "=heavy-1", ok},
		"empty token":               {HeldEnv + "=heavy-1:", ok},
		"a slot that is not there":  {HeldEnv + "=heavy-2:" + strings.SplitN(o.env, ":", 2)[1], ok},
		"a class that is not there": {HeldEnv + "=huge-1:" + strings.SplitN(o.env, ":", 2)[1], ok},
		"not a number":              {HeldEnv + "=heavy-x:" + strings.SplitN(o.env, ":", 2)[1], ok},
		"foreign caller":            {o.env, descendantProcs(2002, 4004)}, // 2002 does not descend from 1001
		"the process table fails":   {o.env, func() (map[int]procInfo, error) { return nil, errors.New("no table") }},
	} {
		code, ran := o.inner(t, ClassHeavy, 2002, c.procs, c.env)
		assert.False(t, ran, name)
		assert.Equal(t, 130, code, name)
	}
}

func TestRun_ALightSlotNeverCoversAHeavyCommand(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	lightOuter := startOuter(t, g, ClassLight, 1001)
	defer func() { lightOuter.release(); <-lightOuter.done }()
	// another command holds the only heavy slot
	heavyDir := t.TempDir()
	h := runAsync(g, context.Background(), Options{Argv: sh(holdScript(heavyDir, "h")), Class: ClassHeavy})
	eventually(t, "the heavy holder", func() bool { return exists(heavyDir + "/started-h") })
	defer func() { _ = os.WriteFile(heavyDir+"/go-h", nil, 0o644); <-h }()

	code, ran := lightOuter.inner(t, ClassHeavy, 2002, descendantProcs(2002, 1001), lightOuter.env)
	assert.False(t, ran, "light to heavy waits for a heavy slot of its own")
	assert.Equal(t, 130, code)
}

func TestRun_ALightHolderLetsAHeavyCommandTakeItsOwnSlot(t *testing.T) {
	g, _ := testGate(t)
	lightOuter := startOuter(t, g, ClassLight, 1001)
	defer func() { lightOuter.release(); <-lightOuter.done }()
	ig := *g
	ig.PID = func() int { return 2002 }
	ig.Procs = descendantProcs(2002, 1001)
	dir := t.TempDir()
	res := runAsync(&ig, context.Background(), Options{Argv: sh(holdScript(dir, "i")), Class: ClassHeavy, Env: cleanEnv(lightOuter.env)})
	eventually(t, "the inner command", func() bool { return exists(dir + "/started-i") })
	assert.Len(t, g.Status().Heavy, 1, "it holds a heavy slot of its own")
	require.NoError(t, os.WriteFile(dir+"/go-i", nil, 0o644))
	assert.Equal(t, 0, (<-res).code)
}

// A claim from a job that is over: the token is in no record, and the lock is free.
func TestRun_AStaleClaimFromAFinishedJobIsNotAPass(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	o := startOuter(t, g, ClassHeavy, 1001)
	stale := o.env
	o.release()
	<-o.done
	// the slot is taken again by someone else
	dir := t.TempDir()
	h := runAsync(g, context.Background(), Options{Argv: sh(holdScript(dir, "h")), Class: ClassHeavy})
	eventually(t, "the new holder", func() bool { return exists(dir + "/started-h") })
	defer func() { _ = os.WriteFile(dir+"/go-h", nil, 0o644); <-h }()
	code, ran := (&outer{g: g}).inner(t, ClassHeavy, 2002, descendantProcs(2002, 1001), stale)
	assert.False(t, ran)
	assert.Equal(t, 130, code)
}

// ---- the tree ----

func pidFrom(t *testing.T, path string) int {
	t.Helper()
	var n int
	eventually(t, "pid file "+path, func() bool {
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		n, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && n > 0
	})
	return n
}

func gone(pid int) bool {
	err := syscall.Kill(pid, 0)
	return errors.Is(err, syscall.ESRCH)
}

// What the command leaves behind in its group is cleared before the slot is given back.
// (M: do not reap the group.)
func TestRun_TheGroupIsClearedWhenTheCommandExits(t *testing.T) {
	g, _ := testGate(t)
	dir := t.TempDir()
	r := runSync(t, g, Options{Argv: sh("sleep 300 & echo $! > " + dir + "/pid; exit 0"), Class: ClassHeavy})
	assert.Equal(t, 0, r.code)
	pid := pidFrom(t, dir+"/pid")
	assert.True(t, gone(pid), "the leftover process is gone when the run returns")
}

func TestRun_AProcessThatIgnoresSigtermIsKilled(t *testing.T) {
	g, _ := testGate(t)
	dir := t.TempDir()
	r := runSync(t, g, Options{Argv: sh("trap '' TERM; sleep 300 & echo $! > " + dir + "/pid; exit 0"), Class: ClassHeavy})
	assert.Equal(t, 0, r.code)
	assert.True(t, gone(pidFrom(t, dir+"/pid")))
}

func TestRun_ASignalGoesToTheWholeGroup(t *testing.T) {
	g, _ := testGate(t)
	sigs := make(chan os.Signal, 1)
	g.Signals = sigs
	dir := t.TempDir()
	res := runAsync(g, context.Background(), Options{Argv: sh("trap 'exit 9' TERM; sleep 300 & echo $! > " + dir + "/pid; touch " + dir + "/started; wait"), Class: ClassHeavy})
	eventually(t, "start", func() bool { return exists(dir + "/started") })
	sigs <- syscall.SIGTERM
	r := <-res
	assert.Equal(t, 9, r.code, "the command's own handler ran")
	assert.True(t, gone(pidFrom(t, dir+"/pid")), "and so did the sleep below it get the signal")
}

func TestRun_ASignalWhileWaitingEndsTheWaitWithoutRunning(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	sigs := make(chan os.Signal, 1)
	dir := t.TempDir()
	occ := runAsync(g, context.Background(), Options{Argv: sh(holdScript(dir, "0")), Class: ClassHeavy})
	eventually(t, "occupant", func() bool { return exists(dir + "/started-0") })
	defer func() { _ = os.WriteFile(dir+"/go-0", nil, 0o644); <-occ }()
	w := *g
	w.Signals = sigs
	res := runAsync(&w, context.Background(), Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy})
	eventually(t, "queued", func() bool { return g.Status().WaitHeavy == 1 })
	sigs <- syscall.SIGINT
	r := <-res
	assert.Equal(t, 128+int(syscall.SIGINT), r.code)
	assert.False(t, exists(dir+"/ran"))
	assert.Zero(t, g.Status().WaitHeavy, "its ticket is gone")
}

// At a terminal the command stays in the gate's process group (a group of its own is stopped at the first read of the
// terminal); the processes below it are followed by parentage and cleared. (M: always setpgid.)
func TestRun_AtATerminalTheCommandKeepsTheGatesGroup(t *testing.T) {
	g, _ := testGate(t)
	g.IsTTY = func() bool { return true }
	r := runSync(t, g, Options{Argv: sh("ps -o pgid= -p $$"), Class: ClassHeavy})
	assert.Equal(t, strconv.Itoa(syscall.Getpgrp()), strings.TrimSpace(r.out))

	g.IsTTY = func() bool { return false }
	r = runSync(t, g, Options{Argv: sh("ps -o pgid= -p $$"), Class: ClassHeavy})
	assert.NotEqual(t, strconv.Itoa(syscall.Getpgrp()), strings.TrimSpace(r.out), "off a terminal it leads a group of its own")
}

// (M: do not clear the followed tree.)
func TestRun_AtATerminalTheTreeBelowTheCommandIsCleared(t *testing.T) {
	g, _ := testGate(t)
	g.IsTTY = func() bool { return true }
	dir := t.TempDir()
	r := runSync(t, g, Options{Argv: sh("sleep 300 & echo $! > " + dir + "/pid; sleep 0.4"), Class: ClassHeavy})
	assert.Equal(t, 0, r.code)
	assert.True(t, gone(pidFrom(t, dir+"/pid")))
}

// ---- the gate dies ----

// TestHelperGate is the gate itself, run as a process by the test below.
func TestHelperGate(t *testing.T) {
	dir := os.Getenv("GATE_TEST_DIR")
	if dir == "" {
		t.Skip("helper for TestRun_AKilledGateKeepsItsSlotWhileItsTreeRuns")
	}
	cfg := Defaults()
	cfg.Dir, cfg.Slots.Heavy, cfg.Priority.Mode, cfg.Admission.DiskMinGB = dir+"/gate", 1, "none", 0
	g := New(cfg)
	g.Pressure, g.Tools, g.IsTTY = nil, Tools{}, func() bool { return false }
	code, _ := g.Run(context.Background(), Options{Argv: sh("sleep 300 & echo $! > " + dir + "/pid; touch " + dir + "/started; wait"), Class: ClassHeavy, Env: cleanEnv()})
	os.Exit(code)
}

// SIGKILL on the gate frees nothing under a tree that still runs: the command inherited the lock. (M: do not hand the
// lock to the command.)
func TestRun_AKilledGateKeepsItsSlotWhileItsTreeRuns(t *testing.T) {
	dir := t.TempDir()
	helper := exec.Command(os.Args[0], "-test.run=^TestHelperGate$")
	helper.Env = append(os.Environ(), "GATE_TEST_DIR="+dir)
	require.NoError(t, helper.Start())
	done := make(chan struct{})
	go func() { _ = helper.Wait(); close(done) }()
	eventually(t, "the gated tree", func() bool { return exists(dir + "/started") })
	sleepPID := pidFrom(t, dir+"/pid")
	defer func() { _ = syscall.Kill(sleepPID, syscall.SIGKILL) }()

	require.NoError(t, helper.Process.Kill())
	<-done

	p := &Pool{Dir: dir + "/gate", Class: ClassHeavy, Size: 1, Now: time.Now}
	tk, err := p.Enqueue()
	require.NoError(t, err)
	defer tk.Drop()
	s, _, err := p.TryTake(tk)
	require.NoError(t, err)
	assert.Nil(t, s, "the slot is still taken: its tree is running")
	held := p.Held()
	require.Len(t, held, 1)
	assert.True(t, held[0].Orphaned, "status says the gate is gone and the tree is alive")

	require.NoError(t, syscall.Kill(sleepPID, syscall.SIGKILL))
	eventually(t, "the slot to be free once the tree is gone", func() bool {
		s, _, err := p.TryTake(tk)
		if err == nil && s != nil {
			s.Release(nil)
			return true
		}
		return false
	})
}

// ---- pressure, disk, the directory, QoS ----

func TestRun_PressureHoldsNewWorkUntilItIsOver(t *testing.T) {
	g, buf := testGate(t)
	fp := &fakePressure{}
	fp.set(Pressure{Level: 2}, nil)
	g.Pressure = fp
	dir := t.TempDir()
	res := runAsync(g, context.Background(), Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy})
	never(t, "a command starting at level 2", 300*time.Millisecond, func() bool { return exists(dir + "/ran") })
	assert.Contains(t, buf.String(), "memory pressure")
	fp.set(Pressure{Level: 1, PageRate: 1000}, nil)
	never(t, "a command starting while pages are going out fast", 200*time.Millisecond, func() bool { return exists(dir + "/ran") })
	fp.set(Pressure{Level: 1, PageRate: 1}, nil)
	assert.Equal(t, 0, (<-res).code)
	assert.True(t, exists(dir+"/ran"))
}

func TestRun_PressureRunningWorkIsNotTouched(t *testing.T) {
	g, _ := testGate(t)
	fp := &fakePressure{}
	fp.set(Pressure{Level: 1}, nil)
	g.Pressure = fp
	dir := t.TempDir()
	res := runAsync(g, context.Background(), Options{Argv: sh(holdScript(dir, "1")), Class: ClassHeavy})
	eventually(t, "start", func() bool { return exists(dir + "/started-1") })
	fp.set(Pressure{Level: 4}, nil)
	never(t, "the running command being ended", 200*time.Millisecond, func() bool {
		select {
		case <-res:
			return true
		default:
			return false
		}
	})
	require.NoError(t, os.WriteFile(dir+"/go-1", nil, 0o644))
	assert.Equal(t, 0, (<-res).code)
}

// An unreadable pressure is admitted only after three failures in a row, with a line on stderr.
func TestRun_PressureUnreadableAdmitsAfterThreeFailures(t *testing.T) {
	g, buf := testGate(t)
	fp := &fakePressure{}
	fp.set(Pressure{}, errors.New("sysctl failed"))
	g.Pressure = fp
	dir := t.TempDir()
	r := runSync(t, g, Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy})
	assert.Equal(t, 0, r.code)
	assert.GreaterOrEqual(t, fp.reads, 3, "it looked three times before it let the command go")
	assert.Contains(t, buf.String(), "unreadable")
}

// A good reading is kept through two failures: level 2 still holds.
func TestRun_PressureLastGoodIsKeptThroughTwoFailures(t *testing.T) {
	g, _ := testGate(t)
	g.Poll = func() time.Duration { return 150 * time.Millisecond }
	fp := &fakePressure{}
	fp.set(Pressure{Level: 2}, nil)
	g.Pressure = fp
	dir := t.TempDir()
	res := runAsync(g, context.Background(), Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy})
	eventually(t, "the first read", func() bool { fp.mu.Lock(); defer fp.mu.Unlock(); return fp.reads >= 1 })
	fp.set(Pressure{}, errors.New("x"))
	eventually(t, "two failed reads", func() bool { fp.mu.Lock(); defer fp.mu.Unlock(); return fp.reads >= 3 })
	assert.False(t, exists(dir+"/ran"), "two failures are not enough to let it go")
	assert.Equal(t, 0, (<-res).code, "the third is")
}

func TestRun_TheDiskFloorRefusesAHeavyCommand(t *testing.T) {
	g, buf := testGate(t)
	g.SetConfig(func(c *Config) { c.Admission.DiskMinGB = 3 })
	g.DiskFree = func(path string) (int64, error) { return 1 << 30, nil }
	dir := t.TempDir()
	r := runSync(t, g, Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy})
	assert.Equal(t, ExitRefused, r.code)
	assert.False(t, exists(dir+"/ran"))
	assert.Contains(t, buf.String(), "1.0 GB free")
	assert.Contains(t, buf.String(), "disk_min_gb")

	r = runSync(t, g, Options{Argv: sh("touch " + dir + "/ran"), Class: ClassLight})
	assert.Equal(t, 0, r.code, "a light command is not held to the floor")
	g.DiskFree = func(path string) (int64, error) { return 0, errors.New("statfs failed") }
	r = runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy})
	assert.Equal(t, 0, r.code, "an unreadable disk is not a reason to refuse")
	g.SetConfig(func(c *Config) { c.Admission.DiskMinGB = 0 })
	g.DiskFree = func(path string) (int64, error) { return 0, nil }
	r = runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy})
	assert.Equal(t, 0, r.code, "0 turns the check off")
}

func TestRun_TheDiskIsLookedAtInTheCommandsDirectoryAndTmp(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Admission.DiskMinGB = 3 })
	var seen []string
	g.DiskFree = func(path string) (int64, error) { seen = append(seen, path); return 100 << 30, nil }
	wd := t.TempDir()
	runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy, Dir: wd})
	// before the queue and again once the slot is taken: the command's directory and $TMPDIR each time
	assert.Equal(t, []string{wd, os.TempDir(), wd, os.TempDir()}, seen)
}

// A file where the directory should be is a damaged install: the command is not run.
func TestRun_ABrokenDirectoryFailsClosed(t *testing.T) {
	g, buf := testGate(t)
	file := filepath.Join(t.TempDir(), "afile")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	g.SetConfig(func(c *Config) { c.Dir = filepath.Join(file, "gate") })
	dir := t.TempDir()
	r := runSync(t, g, Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy})
	assert.Equal(t, ExitRefused, r.code)
	assert.False(t, exists(dir+"/ran"))
	assert.Contains(t, buf.String(), "was not run")
}

// A directory the process may not write (a read-only sandbox) runs the command without a slot, with a line.
// (M: fail open on any error.)
func TestRun_ADeniedDirectoryFailsOpen(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may write anywhere")
	}
	g, buf := testGate(t)
	parent := t.TempDir()
	require.NoError(t, os.Chmod(parent, 0o555))
	defer os.Chmod(parent, 0o755)
	g.SetConfig(func(c *Config) { c.Dir = filepath.Join(parent, "gate") })
	dir := t.TempDir()
	r := runSync(t, g, Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy})
	assert.Equal(t, 0, r.code)
	assert.True(t, exists(dir+"/ran"))
	assert.Contains(t, buf.String(), "without a slot")
}

func TestDirError_OnlyTheSandboxErrorsFailOpen(t *testing.T) {
	for _, e := range []error{syscall.EPERM, syscall.EACCES, syscall.EROFS} {
		assert.ErrorIs(t, dirError(e), ErrUnavailable, e.Error())
	}
	for _, e := range []error{syscall.ENOTDIR, syscall.ENOSPC, syscall.EIO, syscall.EEXIST, errors.New("other")} {
		err := dirError(e)
		assert.ErrorIs(t, err, ErrBroken, e.Error())
		assert.NotErrorIs(t, err, ErrUnavailable, e.Error())
	}
}

func TestRun_PriorityWrapsTheCommandUnlessForeground(t *testing.T) {
	g, _ := testGate(t)
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "wrap")
	require.NoError(t, os.WriteFile(wrapper, []byte("#!/bin/sh\necho \"$1 $2\" >> "+dir+"/calls\nshift\nif [ \"$1\" = 10 ]; then shift; fi\nexec \"$@\"\n"), 0o755))
	g.Tools = Tools{Nice: wrapper}
	g.SetConfig(func(c *Config) { c.Priority.Mode = "nice"; c.Priority.Nice = 10 })
	r := runSync(t, g, Options{Argv: sh("echo ran"), Class: ClassHeavy})
	assert.Equal(t, "ran\n", r.out)
	b, _ := os.ReadFile(dir + "/calls")
	assert.Equal(t, "-n 10\n", string(b), "nice -n 10 stands in front of the command")

	r = runSync(t, g, Options{Argv: sh("echo ran"), Class: ClassHeavy, Foreground: true})
	assert.Equal(t, "ran\n", r.out)
	b, _ = os.ReadFile(dir + "/calls")
	assert.Equal(t, "-n 10\n", string(b), "--fg leaves it out")

	g.SetConfig(func(c *Config) { c.Priority.Mode = "none" })
	runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy})
	b, _ = os.ReadFile(dir + "/calls")
	assert.Equal(t, "-n 10\n", string(b), "mode none is not wrapped")
}

func TestTruncateArgv_ScrubsSecretsAndCutsTheRest(t *testing.T) {
	got := truncateArgv([]string{"curl", "-H", "Authorization: Bearer abc", "https://x/?token=1", strings.Repeat("a", 300)})
	assert.Equal(t, "<redacted>", got[2])
	assert.Equal(t, "https://x/?token=<redacted>", got[3])
	assert.Len(t, got[4], 120+len("…"))
	long := make([]string, 20)
	for i := range long {
		long[i] = "w"
	}
	assert.Len(t, truncateArgv(long), 13, "12 words and a mark")
	// the cut comes after the scrub: a secret far into a long word does not survive in its first 120 bytes
	got = truncateArgv([]string{"run", "--token=" + strings.Repeat("s", 400)})
	assert.Equal(t, "--token=<redacted>", got[1])
}

func TestStatus_ASuspectRecordHasAFreeLockAndALivePid(t *testing.T) {
	g, _ := testGate(t)
	require.NoError(t, os.MkdirAll(g.Config().Dir, 0o755))
	h := Holder{Class: ClassHeavy, Slot: 1, PID: os.Getpid(), Token: "t"}
	require.NoError(t, os.WriteFile(filepath.Join(g.Config().Dir, "heavy-1.json"), mustJSON(t, h), 0o644))
	st := g.Status()
	assert.Empty(t, st.Heavy, "a free lock is a free slot")
	require.Len(t, st.Suspect, 1)
	assert.Empty(t, st.Suspect[0].Token)
}

// A setting that changes gate.dir while a command waits is for the next command: the ticket lives in one directory, and the
// waiter keeps looking at that one. (M: re-read the directory with the rest of the settings.)
func TestRun_AWaitingCommandStaysInItsDirectoryWhenTheSettingMoves(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	dirA := g.Config().Dir
	dirB := t.TempDir() + "/other"
	work := t.TempDir()
	var moved atomic.Bool
	g.Load = func() Config {
		c := g.Config()
		if moved.Load() {
			c.Dir = dirB
		}
		return c
	}
	occ := runAsync(g, t.Context(), Options{Argv: sh(holdScript(work, "0")), Class: ClassHeavy})
	eventually(t, "the occupant", func() bool { return exists(work + "/started-0") })
	waiter := runAsync(g, t.Context(), Options{Argv: sh("touch " + work + "/ran"), Class: ClassHeavy})
	eventually(t, "the waiter's ticket", func() bool { return g.Status().WaitHeavy == 1 })

	moved.Store(true) // the setting moves
	never(t, "the waiter running before the slot is free", 200*time.Millisecond, func() bool { return exists(work + "/ran") })
	require.NoError(t, os.WriteFile(work+"/go-0", nil, 0o644))
	assert.Equal(t, 0, (<-waiter).code, "it took the freed slot of the directory it queued in")
	assert.True(t, exists(work+"/ran"))
	<-occ
	_, err := os.Stat(dirB)
	assert.True(t, os.IsNotExist(err), "and never touched the new directory")
	_ = dirA
}

// The disk floor is looked at again once the slot is taken: a wait of hours can end with the disk full. (M: check the disk
// before the queue only.)
func TestRun_TheDiskIsLookedAtAgainAfterTheWait(t *testing.T) {
	g, buf := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1; c.Admission.DiskMinGB = 3 })
	free := int64(100 << 30)
	var mu sync.Mutex
	g.DiskFree = func(string) (int64, error) { mu.Lock(); defer mu.Unlock(); return free, nil }
	work := t.TempDir()
	occ := runAsync(g, t.Context(), Options{Argv: sh(holdScript(work, "0")), Class: ClassHeavy})
	eventually(t, "the occupant", func() bool { return exists(work + "/started-0") })
	waiter := runAsync(g, t.Context(), Options{Argv: sh("touch " + work + "/ran"), Class: ClassHeavy})
	eventually(t, "the waiter's ticket", func() bool { return g.Status().WaitHeavy == 1 })

	mu.Lock()
	free = 1 << 30 // the disk filled up while it waited
	mu.Unlock()
	require.NoError(t, os.WriteFile(work+"/go-0", nil, 0o644))
	r := <-waiter
	assert.Equal(t, ExitRefused, r.code)
	assert.False(t, exists(work+"/ran"))
	assert.Contains(t, buf.String(), "disk_min_gb")
	<-occ
	assert.Empty(t, g.Status().Heavy, "and the slot it had taken is free again")
}

// A child that leaves the process group with setsid is still a child of the command while the command runs; the tree
// remembers it and the clean-up ends it before the slot is given back. (M: clear the group only.) What this cannot catch: a
// child that is forked, leaves the group and is orphaned within one TreeEvery of the last look (the parent exits at once).
func TestRun_AChildThatLeftTheGroupIsEndedToo(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl is needed to call setsid")
	}
	g, _ := testGate(t)
	dir := t.TempDir()
	script := `perl -e 'use POSIX; if (fork == 0) { POSIX::setsid(); open F, ">", $ARGV[0]; print F $$; close F; exec "sleep", "300" } select(undef, undef, undef, 0.5)' ` + dir + `/pid; exit 0`
	r := runSync(t, g, Options{Argv: sh(script), Class: ClassHeavy})
	assert.Equal(t, 0, r.code)
	pid := pidFrom(t, dir+"/pid")
	assert.True(t, gone(pid), "the child that left the group is gone when the run returns")
}

// A cancelled run ends in bounded time even if the command ignores SIGTERM: SIGTERM, the grace, SIGKILL. (M: signal once.)
func TestRun_ACancelledRunEndsEvenIfTheCommandIgnoresSigterm(t *testing.T) {
	g, _ := testGate(t)
	g.KillGrace = 200 * time.Millisecond
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	res := runAsync(g, ctx, Options{Argv: sh("trap '' TERM; touch " + dir + "/started; while :; do sleep 0.05; done"), Class: ClassHeavy})
	eventually(t, "start", func() bool { return exists(dir + "/started") })
	cancel()
	select {
	case r := <-res:
		assert.Equal(t, 128+int(syscall.SIGKILL), r.code)
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not end after the cancel")
	}
	assert.Empty(t, g.Status().Heavy, "and its slot is free")
}

// The holder record is part of the nesting protocol: when it cannot be written the command is not run.
// (M: log the failure and run on.)
func TestRun_ACommandIsNotRunWithoutItsHolderRecord(t *testing.T) {
	g, buf := testGate(t)
	dir := t.TempDir()
	cfgDir := g.Config().Dir
	require.NoError(t, os.MkdirAll(filepath.Join(cfgDir, "heavy-1.json"), 0o755))               // a directory where the record goes: the rename fails
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "heavy-1.json", "keep"), nil, 0o644)) // and not an empty one, which Remove would take away
	r := runSync(t, g, Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy})
	assert.Equal(t, ExitRefused, r.code)
	assert.False(t, exists(dir+"/ran"))
	assert.Contains(t, buf.String(), "was not run")
	assert.Empty(t, g.Status().Heavy)
}

// A child that escaped the clean-up (orphaned at once, so no look at the process table saw it) still holds fd 3, but the
// slot is released when the command ends, and the gate says who was left running. (M: no unlock on release.)
func TestRun_AnEscapedChildDoesNotKeepTheSlotAndIsNamed(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl is needed to call setsid")
	}
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof is needed to name the leftovers")
	}
	g, buf := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	g.TreeEvery = time.Hour // no look at the tree at all: the child is orphaned unseen
	dir := t.TempDir()
	script := `perl -e 'use POSIX; if (fork == 0) { POSIX::setsid(); open F, ">", $ARGV[0]; print F $$; close F; exec "sleep", "300" }' ` + dir + `/pid; exit 0`
	r := runSync(t, g, Options{Argv: sh(script), Class: ClassHeavy})
	assert.Equal(t, 0, r.code)
	pid := pidFrom(t, dir+"/pid")
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()

	assert.Empty(t, g.Status().Heavy, "the slot is free although the escaped child still holds fd 3")
	pool := g.pool(g.Config(), ClassHeavy)
	tk, err := pool.Enqueue()
	require.NoError(t, err)
	s, _, err := pool.TryTake(tk)
	require.NoError(t, err)
	assert.NotNil(t, s, "a second gate can take it")
	s.Release(nil)
	tk.Drop()
	assert.Contains(t, buf.String(), "escaped the clean-up and are still running: "+strconv.Itoa(pid))
	assert.Contains(t, buf.String(), "slot is released")
}

// Release names what else holds the lock file and does not name itself.
func TestSlot_ReleaseReturnsTheOthersWhoHoldTheLock(t *testing.T) {
	p := &Pool{Dir: t.TempDir(), Class: ClassHeavy, Size: 1, Now: time.Now}
	tk, err := p.Enqueue()
	require.NoError(t, err)
	defer tk.Drop()
	s, _, err := p.TryTake(tk)
	require.NoError(t, err)
	require.NotNil(t, s)
	left := s.Release(func(string) []Straggler { return []Straggler{{PID: os.Getpid()}, {PID: 4242, Cmd: "tmux"}} })
	assert.Equal(t, []Straggler{{PID: 4242, Cmd: "tmux"}}, left)
}

// What a waiting command is told: who holds the slot (cut and scrubbed), where, for how long, how many are ahead, that the
// wait is normal; and a line when its turn comes.
func TestRun_AWaitingCommandIsToldWhoHoldsTheSlotAndThatItIsNormal(t *testing.T) {
	g, buf := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	work := t.TempDir()
	// the holder's command line carries a secret that must not reach the message
	holdBin := work + "/holder"
	require.NoError(t, os.WriteFile(holdBin, []byte("#!/bin/sh\n"+holdScript(work, "0")+"\n"), 0o755))
	occ := runAsync(g, t.Context(), Options{Argv: []string{"deploy", "--token", "s3cr3t-value"}, Real: holdBin, Class: ClassHeavy, Dir: work})
	eventually(t, "the occupant", func() bool { return exists(work + "/started-0") })
	waiter := runAsync(g, t.Context(), Options{Argv: sh("true"), Class: ClassHeavy})
	eventually(t, "the waiting line", func() bool { return strings.Contains(buf.String(), "waiting for a heavy slot") })
	line := buf.String()
	assert.Contains(t, line, "1 ahead")
	assert.Contains(t, line, "held ")
	assert.Contains(t, line, "by \"deploy --token <redacted>\"")
	assert.NotContains(t, line, "s3cr3t-value", "a secret in the holder's command line does not reach the message")
	assert.Contains(t, line, projectOf(work), "where it runs")
	assert.Contains(t, line, "This is a normal queue")
	assert.Contains(t, line, "do not interrupt it")

	require.NoError(t, os.WriteFile(work+"/go-0", nil, 0o644))
	assert.Equal(t, 0, (<-waiter).code)
	<-occ
	assert.Contains(t, buf.String(), "got a heavy slot after ")
}

func TestWaitMessages_Formatting(t *testing.T) {
	assert.Equal(t, "45s", fmtDur(45*time.Second))
	assert.Equal(t, "12m", fmtDur(12*time.Minute+20*time.Second))
	assert.Equal(t, "1h05m", fmtDur(65*time.Minute))
	assert.Equal(t, "purdex/spa", projectOf("/Users/wake/Workspace/wake/purdex/spa"))
	assert.Equal(t, "work", projectOf("/work"))
	now := time.Unix(10000, 0)
	h := Holder{Argv: []string{"pnpm", "exec", "vitest", "run"}, Cwd: "/w/purdex/spa", Started: now.Add(-12 * time.Minute)}
	assert.Equal(t,
		"waiting for a heavy slot — 1 ahead; held 12m by \"pnpm exec vitest run\" (purdex/spa). This is a normal queue: it starts by itself when that ends; do not interrupt it or run the command another way.",
		waitingLine(ClassHeavy, 1, []Holder{h}, now))
	older := Holder{Argv: []string{"go", "test"}, Cwd: "/w/x", Started: now.Add(-30 * time.Minute)}
	assert.Contains(t, waitingLine(ClassHeavy, 2, []Holder{h, older}, now), "held 30m by \"go test\"", "the oldest is named")
	assert.Contains(t, waitingLine(ClassHeavy, 2, []Holder{h, older}, now), "(+1 more)")
	assert.Contains(t, waitingLine(ClassLight, 0, nil, now), "no slot is held right now")
	orphan := h
	orphan.Orphaned = true
	assert.Contains(t, waitingLine(ClassHeavy, 1, []Holder{orphan}, now), "its gate is gone")
	assert.Contains(t, waitingLine(ClassHeavy, 1, []Holder{{}}, now), "; another command.")

	a := Defaults().Admission
	pl := pressureLine(Pressure{Level: 2, PageRate: 340.4}, a, true)
	assert.Contains(t, pl, "level 2")
	assert.Contains(t, pl, "340 pages/s")
	assert.Contains(t, pl, "level 1 or below and under 50 pages/s")
	assert.Contains(t, pl, "normal wait")
	assert.Contains(t, pressureLine(Pressure{}, a, false), "could not be read yet")
	assert.Equal(t, "got a heavy slot after 14m", grantedLine(ClassHeavy, 14*time.Minute+5*time.Second))
}
