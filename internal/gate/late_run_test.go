package gate

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTool writes an executable that stands for pnpm: it touches started, then waits for the file go (an optional extra line of shell
// goes first), and is run as `pnpm run build` (a packaging script) through Options.Real.
func fakeTool(t *testing.T, dir, extra string) string {
	t.Helper()
	path := filepath.Join(dir, "fakepnpm")
	body := "#!/bin/sh\n" + extra + "\ntouch " + dir + "/started\nwhile [ ! -e " + dir + "/go ]; do sleep 0.02; done\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	return path
}

func packagingGate(t *testing.T, after time.Duration) *Gate {
	t.Helper()
	g, _ := testGate(t)
	g.Holders = nil
	g.SetConfig(func(c *Config) {
		c.Packaging = PackagingConfig{LateSlotAfter: after, LateSlotClass: ClassLight, RecordUnknown: 10 * time.Second}
		c.Slots.Light = 2
	})
	old := lateRecheck
	lateRecheck = 40 * time.Millisecond
	t.Cleanup(func() { lateRecheck = old })
	return g
}

func runPackaging(g *Gate, dir string) <-chan runResult {
	return runAsync(g, context.Background(), Options{Argv: []string{"pnpm", "run", "build"}, Real: filepath.Join(dir, "fakepnpm")})
}

func lightHeld(g *Gate) []Holder { return g.pool(g.Config(), ClassLight).Held() }

func lastDecision(t *testing.T, g *Gate) map[string]any {
	t.Helper()
	recs := readDecisions(t, g)
	require.NotEmpty(t, recs)
	return recs[len(recs)-1]
}

// A packaging script starts at once with no slot, is put under a light slot when it has run for the delay, holds it until it
// ends, and leaves one line saying so.
func TestPackaging_StartsAtOnceThenTakesALateSlot(t *testing.T) {
	g := packagingGate(t, 250*time.Millisecond)
	dir := t.TempDir()
	fakeTool(t, dir, "")
	done := runPackaging(g, dir)
	eventually(t, "the script to start", func() bool { return exists(dir + "/started") })
	assert.Empty(t, lightHeld(g), "no slot at its start: it is never held back")
	assert.Equal(t, 0, g.StatusWith(HoldersOrphaned).WaitLight)

	eventually(t, "the late slot", func() bool { return len(lightHeld(g)) == 1 })
	h := lightHeld(g)[0]
	assert.True(t, h.Late)
	assert.False(t, h.Over)
	assert.Equal(t, []string{"pnpm", "run", "build"}, h.Argv)
	assert.Greater(t, h.PGID, 1, "the record names the command's pid, so a watcher counts its tree as managed")

	require.NoError(t, os.WriteFile(dir+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-done).code)
	assert.Empty(t, lightHeld(g), "the slot is given back when it ends")

	rec := lastDecision(t, g)
	assert.Equal(t, "packaging", rec["outcome"])
	assert.Equal(t, true, rec["late_slot"])
	assert.InDelta(t, 0.25, rec["late_after_s"], 0.2)
	assert.NotNil(t, rec["duration_s"])
}

// A script that ends before the delay never holds a slot, and is written down all the same.
func TestPackaging_ShortOnesAreWrittenDownWithNoSlot(t *testing.T) {
	g := packagingGate(t, time.Hour)
	dir := t.TempDir()
	fakeTool(t, dir, "")
	done := runPackaging(g, dir)
	eventually(t, "start", func() bool { return exists(dir + "/started") })
	require.NoError(t, os.WriteFile(dir+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-done).code)
	rec := lastDecision(t, g)
	assert.Equal(t, "packaging", rec["outcome"])
	assert.NotContains(t, rec, "late_slot")
}

// late_slot_after_s = 0 turns the late slot off; the line is still written.
func TestPackaging_ZeroDelayMeansNoLateSlot(t *testing.T) {
	g := packagingGate(t, 0)
	dir := t.TempDir()
	fakeTool(t, dir, "")
	done := runPackaging(g, dir)
	eventually(t, "start", func() bool { return exists(dir + "/started") })
	time.Sleep(200 * time.Millisecond)
	assert.Empty(t, lightHeld(g))
	require.NoError(t, os.WriteFile(dir+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-done).code)
	assert.Equal(t, "packaging", lastDecision(t, g)["outcome"])
}

// The pool is full: the late holder goes over its size, nothing is stopped, and the people behind queue.
func TestPackaging_AFullPoolIsGoneOverNotStopped(t *testing.T) {
	g := packagingGate(t, 150*time.Millisecond)
	g.SetConfig(func(c *Config) { c.Slots.Light = 1 })
	occupied, rel0 := holdSlot(t, g, ClassLight, "occupant")
	rel := func() { rel0(); <-occupied }
	dir := t.TempDir()
	fakeTool(t, dir, "")
	done := runPackaging(g, dir)
	eventually(t, "the late slot over the pool", func() bool { return len(lightHeld(g)) == 2 })
	var over *Holder
	for _, h := range lightHeld(g) {
		if h.Over {
			h := h
			over = &h
		}
	}
	require.NotNil(t, over)
	assert.True(t, over.Late)

	// someone behind it queues, as for any holder
	waiter := runAsync(g, context.Background(), Options{Argv: sh("true"), Class: ClassLight})
	eventually(t, "the waiter to queue", func() bool { return g.StatusWith(HoldersOrphaned).WaitLight == 1 })
	rel() // the occupant goes: one real slot is free, and the late holder has the place of it
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 1, g.StatusWith(HoldersOrphaned).WaitLight, "still queued: the late holder counts")
	require.NoError(t, os.WriteFile(dir+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-done).code)
	assert.Equal(t, 0, (<-waiter).code, "and goes when the packaging ends")
}

// A tree that holds or waits for a slot below the script is not given a second one; when it lets go, the late slot is taken.
func TestPackaging_NoLateSlotWhileTheTreeHoldsOne(t *testing.T) {
	g := packagingGate(t, 900*time.Millisecond) // long enough to set the stage first on a busy host
	dir := t.TempDir()
	// the script starts a child and says its pid
	fakeTool(t, dir, "sleep 30 & echo $! > "+dir+"/child")
	done := runPackaging(g, dir)
	eventually(t, "the child", func() bool { return exists(dir + "/child") })
	b, err := os.ReadFile(dir + "/child")
	require.NoError(t, err)
	childPID, err := strconv.Atoi(strings.TrimSpace(string(b)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })

	// a slot held by that child (as its own gate would hold it)
	pool := g.pool(g.Config(), ClassHeavy)
	tk, err := pool.Enqueue()
	require.NoError(t, err)
	s, _, err := pool.TryTake(tk)
	require.NoError(t, err)
	tk.Drop()
	require.NotNil(t, s)
	require.NoError(t, s.SetHolder(Holder{PID: childPID, Argv: []string{"go", "test", "./..."}}))

	time.Sleep(1500 * time.Millisecond) // well past the delay
	assert.Empty(t, lightHeld(g), "the tree below holds a slot: no second one for the same work")

	s.Release(nil) // it lets go
	eventually(t, "the late slot once the tree is clear", func() bool { return len(lightHeld(g)) == 1 })
	require.NoError(t, os.WriteFile(dir+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-done).code)
	assert.Equal(t, true, lastDecision(t, g)["late_slot"])
}

func TestTreeBusy(t *testing.T) {
	g, _ := testGate(t)
	g.Holders = nil
	g.Procs = func() (map[int]procInfo, error) {
		return map[int]procInfo{100: {PPID: 5}, 101: {PPID: 100}, 102: {PPID: 101}, 200: {PPID: 5}, 5: {PPID: 1}}, nil
	}
	cfg := g.Config()
	assert.False(t, g.treeBusy(cfg, 100), "nothing holds or waits")

	// a holder below the root
	p := g.pool(cfg, ClassHeavy)
	tk, _ := p.Enqueue()
	s, _, err := p.TryTake(tk)
	require.NoError(t, err)
	tk.Drop()
	require.NoError(t, s.SetHolder(Holder{PID: 102}))
	assert.True(t, g.treeBusy(cfg, 100), "a gate below the root holds a slot")
	assert.False(t, g.treeBusy(cfg, 200), "a tree that is not below it does not count")
	s.Release(nil)

	// a holder whose command (not the gate) is below the root
	l := g.pool(cfg, ClassLight)
	ls, err := l.TakeLate()
	require.NoError(t, err)
	require.NoError(t, ls.SetHolder(Holder{PID: 999, PGID: 101}))
	assert.True(t, g.treeBusy(cfg, 100))
	ls.Release(nil)

	// a waiter below the root
	_, err = l.EnqueueFor(WaiterInfo{PID: 101})
	require.NoError(t, err)
	assert.True(t, g.treeBusy(cfg, 100), "a gate below the root waits for a slot")
}

// A command below a late holder is covered by its slot: it does not wait for a slot of its own, and is recorded as nested.
func TestPackaging_ACommandBelowALateHolderPassesAsNested(t *testing.T) {
	g, _ := testGate(t)
	g.Holders = nil
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	// the only heavy slot is held by someone else
	occupied, rel := holdSlot(t, g, ClassHeavy, "occupant")
	defer func() { rel(); <-occupied }() // the occupant polls a directory that goes away with the test: it must really have ended
	// a packaging script (pid 1001) holds a late light slot
	late, err := g.pool(g.Config(), ClassLight).TakeLate()
	require.NoError(t, err)
	require.NoError(t, late.SetHolder(Holder{PID: os.Getpid(), PGID: 1001, Late: true, Argv: []string{"pnpm", "run", "package"}}))
	defer late.Release(nil)

	ig := *g
	ig.PID = func() int { return 2002 }
	ig.Procs = descendantProcs(2002, 1001)
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	code, _ := ig.Run(ctx, Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy, Env: cleanEnv()})
	assert.Equal(t, 0, code)
	assert.True(t, exists(dir+"/ran"), "it ran at once, though the only heavy slot is taken")
	assert.Equal(t, true, lastDecision(t, g)["nested"])

	// the same command from outside the packaging tree waits (and here is cut off by its deadline)
	og := *g
	og.PID = func() int { return 3003 }
	og.Procs = descendantProcs(3003, 1)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel2()
	dir2 := t.TempDir()
	code, _ = og.Run(ctx2, Options{Argv: sh("touch " + dir2 + "/ran"), Class: ClassHeavy, Env: cleanEnv()})
	assert.Equal(t, 130, code)
	assert.False(t, exists(dir2+"/ran"))
}

// An unknown `run` script is only timed: no slot, ever, and a line only when it ran at least record_unknown_s.
func TestUnknownScript_OnlyTimedAndOnlyWhenLongEnough(t *testing.T) {
	g := packagingGate(t, 100*time.Millisecond)
	g.SetConfig(func(c *Config) { c.Packaging.RecordUnknown = 300 * time.Millisecond })
	dir := t.TempDir()
	fakeTool(t, dir, "")
	run := func() int {
		return (<-runAsync(g, context.Background(), Options{Argv: []string{"pnpm", "run", "e2e"}, Real: filepath.Join(dir, "fakepnpm")})).code
	}
	_ = os.Remove(dir + "/go")
	// short: released at once
	require.NoError(t, os.WriteFile(dir+"/go", nil, 0o644))
	assert.Equal(t, 0, run())
	recs, _ := os.ReadFile(filepath.Join(g.Config().Dir, decisionsFile))
	assert.NotContains(t, string(recs), `"outcome":"none"`, "a short script leaves no line")

	// long: held for 600 ms
	require.NoError(t, os.Remove(dir+"/go"))
	_ = os.Remove(dir + "/started")
	res := runAsync(g, context.Background(), Options{Argv: []string{"pnpm", "run", "e2e"}, Real: filepath.Join(dir, "fakepnpm")})
	eventually(t, "start", func() bool { return exists(dir + "/started") })
	time.Sleep(600 * time.Millisecond)
	assert.Empty(t, lightHeld(g), "never put under a slot, however long it runs")
	assert.Empty(t, g.pool(g.Config(), ClassHeavy).Held())
	require.NoError(t, os.WriteFile(dir+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-res).code)
	rec := lastDecision(t, g)
	assert.Equal(t, "none", rec["outcome"])
	assert.NotContains(t, rec, "late_slot")
}

// Ctrl-C: at a terminal the command is already signalled with the group, so the gate does not pass SIGINT on (it would arrive
// twice); off a terminal it does. SIGTERM is passed on in every case. With a slot and without one alike (#2996: the packaging
// script has no slot at its start).
func TestSignals_TtyDecidesWhetherSIGINTIsPassedOnWhateverTheSlot(t *testing.T) {
	for _, c := range []struct {
		name     string
		tty      bool
		withSlot bool
		wantINT  bool
	}{
		{"terminal, slot", true, true, false},
		{"terminal, no slot", true, false, false},
		{"no terminal, slot", false, true, true},
		{"no terminal, no slot", false, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			g, _ := testGate(t)
			g.Holders = nil
			g.IsTTY = func() bool { return c.tty }
			sigs := make(chan os.Signal, 2)
			g.Signals = sigs
			dir := t.TempDir()
			script := "trap 'echo int >> " + dir + "/got' INT; trap 'echo term >> " + dir + "/got; exit 0' TERM; touch " + dir + "/started; while true; do sleep 0.02; done"
			o := Options{Argv: sh(script)}
			if c.withSlot {
				o.Class = ClassHeavy
			} else {
				g.SetConfig(func(cf *Config) { cf.Enabled = false }) // runs with no slot, as a packaging script does at its start
			}
			res := runAsync(g, context.Background(), o)
			eventually(t, "start", func() bool { return exists(dir + "/started") })
			sigs <- syscall.SIGINT
			time.Sleep(300 * time.Millisecond)
			got, _ := os.ReadFile(dir + "/got")
			if c.wantINT {
				assert.Contains(t, string(got), "int", "SIGINT is passed on off a terminal")
			} else {
				assert.NotContains(t, string(got), "int", "at a terminal SIGINT is the terminal's to deliver, once")
			}
			sigs <- syscall.SIGTERM
			<-res
			got, _ = os.ReadFile(dir + "/got")
			assert.Contains(t, string(got), "term", "SIGTERM is passed on in every case")
			if !c.wantINT {
				assert.NotContains(t, string(got), "int")
			}
		})
	}
}

// After the late slot is taken, what the script started is still followed: the clean-up at its end covers it, and the slot is
// released only after that.
func TestPackaging_LeftoversAreClearedAfterALateSlot(t *testing.T) {
	g := packagingGate(t, 150*time.Millisecond)
	g.SetConfig(func(c *Config) { c.Reap.Enabled = true })
	g.KillGrace = 300 * time.Millisecond
	dir := t.TempDir()
	// started before the slot, and left running when the script ends
	fakeTool(t, dir, "sleep 120 & echo $! > "+dir+"/child")
	done := runPackaging(g, dir)
	eventually(t, "the child", func() bool { return exists(dir + "/child") })
	eventually(t, "the late slot", func() bool { return len(lightHeld(g)) == 1 })
	b, err := os.ReadFile(dir + "/child")
	require.NoError(t, err)
	childPID, err := strconv.Atoi(strings.TrimSpace(string(b)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })
	require.NoError(t, os.WriteFile(dir+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-done).code)
	eventually(t, "the leftover to be cleared", func() bool { return syscall.Kill(childPID, 0) != nil })
	assert.Empty(t, lightHeld(g))
}

// A packaging script that already runs below a held slot (the claim is in its environment) is only written down: its work is
// counted already, so it never takes a late slot of its own.
func TestPackaging_UnderAClaimItTakesNoLateSlot(t *testing.T) {
	g := packagingGate(t, 150*time.Millisecond)
	dir := t.TempDir()
	fakeTool(t, dir, "")
	// a real holder (pid 1001) whose claim this gate's process descends from
	holder, err := g.pool(g.Config(), ClassHeavy).TakeLate()
	require.NoError(t, err)
	defer holder.Release(nil)
	require.NoError(t, holder.SetHolder(Holder{PID: 1001, Token: "tok", Argv: []string{"x"}}))
	g.PID = func() int { return 2002 }
	g.Procs = descendantProcs(2002, 1001)
	done := runAsync(g, context.Background(), Options{
		Argv: []string{"pnpm", "run", "build"}, Real: filepath.Join(dir, "fakepnpm"),
		Env: cleanEnv(HeldEnv + "=" + string(ClassHeavy) + "-" + strconv.Itoa(holder.K) + ":tok"),
	})
	eventually(t, "start", func() bool { return exists(dir + "/started") })
	time.Sleep(500 * time.Millisecond)
	assert.Empty(t, lightHeld(g), "it runs inside someone's slot already")
	require.NoError(t, os.WriteFile(dir+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-done).code)
	rec := lastDecision(t, g)
	assert.Equal(t, "packaging", rec["outcome"])
	assert.NotContains(t, rec, "late_slot")
}

// A claim that no holder stands behind (stale, or typed by hand) does not stop the late slot (R1 of #2996b).
func TestPackaging_AStaleClaimDoesNotSuppressTheLateSlot(t *testing.T) {
	g := packagingGate(t, 150*time.Millisecond)
	dir := t.TempDir()
	fakeTool(t, dir, "")
	done := runAsync(g, context.Background(), Options{
		Argv: []string{"pnpm", "run", "build"}, Real: filepath.Join(dir, "fakepnpm"),
		Env: cleanEnv(HeldEnv + "=heavy-3:stale"),
	})
	eventually(t, "the late slot", func() bool { return len(lightHeld(g)) == 1 })
	require.NoError(t, os.WriteFile(dir+"/go", nil, 0o644))
	assert.Equal(t, 0, (<-done).code)
	assert.Equal(t, true, lastDecision(t, g)["late_slot"])
}

// Only a late holder's pid covers the processes below it; an ordinary holder whose process group is an ancestor does not.
func TestUnderLate_OnlyLateHoldersCoverTheirTree(t *testing.T) {
	g, _ := testGate(t)
	g.Holders = nil
	s, err := g.pool(g.Config(), ClassLight).TakeLate()
	require.NoError(t, err)
	defer s.Release(nil)
	require.NoError(t, s.SetHolder(Holder{PID: os.Getpid(), PGID: 1001, Late: false, Argv: []string{"x"}}))
	ig := *g
	ig.PID = func() int { return 2002 }
	ig.Procs = descendantProcs(2002, 1001)
	assert.False(t, ig.underLate(ig.Config()), "not a late holder")
	require.NoError(t, s.SetHolder(Holder{PID: os.Getpid(), PGID: 1001, Late: true, Argv: []string{"x"}}))
	assert.True(t, ig.underLate(ig.Config()))
}

// The gate runs a `run` script of the project itself, not an `npx` tool, to time it.
func TestWatchKind_RunScriptsAreWatchedNpxToolsAreNot(t *testing.T) {
	g := packagingGate(t, time.Second)
	cfg := g.Config()
	e := EnvOf(nil, false)
	assert.Equal(t, watchUnknown, cfg.watchKind([]string{"pnpm", "run", "e2e"}, e))
	assert.Equal(t, watchPackaging, cfg.watchKind([]string{"pnpm", "run", "build"}, e))
	assert.Empty(t, cfg.watchKind([]string{"npx", "e2e"}, e))
	assert.Empty(t, cfg.watchKind([]string{"npx", "run", "e2e"}, e))
	for _, sub := range []string{"exec", "x", "dlx"} {
		assert.Empty(t, cfg.watchKind([]string{"pnpm", sub, "some-tool"}, e), sub)
		assert.Empty(t, cfg.watchKind([]string{"npm", sub, "some-tool"}, e), sub)
	}
}
