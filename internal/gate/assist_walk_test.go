package gate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- X2b-2: a waiting ticket offers itself to the assist hosts ----

// walkRun runs a heavy shell script through g.Run with an AssistWalk that names one usable host (air26) on the fake host.
func walkRun(t *testing.T, class Class, host AssistHost, script string, prep func(h *assistHost, g *Gate)) dispatchRun {
	t.Helper()
	g, errs := testGate(t)
	work := workRepo(t)
	h := newAssistHost(t, filepath.Base(work))
	if prep != nil {
		prep(h, g)
	}
	host.Can = append(host.Can, "sh")
	var out bytes.Buffer
	code, err := g.Run(context.Background(), Options{
		Argv: sh(script), Class: class, Dir: work, Env: cleanEnv(), Stdout: &out, Stderr: errs,
		Walk: &AssistWalk{
			Usable:   func() map[string]AssistHost { return map[string]AssistHost{"air26": host} },
			Reserved: func(time.Time) bool { return false },
			SSH:      h.ssh,
		},
	})
	require.NoError(t, err)
	return dispatchRun{code: code, out: out.String(), errs: errs.String(), g: g, dir: g.Config().Dir, h: h, repo: h.repo}
}

// A heavy ticket with a usable, capable, free host goes there although a slot is free here.
func TestWalk_AHeavyTicketGoesToTheHostEvenWithAFreeSlotHere(t *testing.T) {
	r := walkRun(t, ClassHeavy, AssistHost{}, `echo there; exit 4`, nil)
	assert.Equal(t, 4, r.code)
	assert.Contains(t, r.out, "there")
	d := r.lastDecision(t)
	assert.Equal(t, "assist", d.Outcome)
	assert.Equal(t, "air26", d.Host)
	r.nothingLeft(t)
}

// The queue's rules hold for the walk: a ticket on hold and a class with no slot are not offered.
func TestWalk_EligibilityFollowsTheQueuesRules(t *testing.T) {
	g, _ := testGate(t)
	cfg := g.Config()
	pool := g.pool(cfg, ClassHeavy)
	a, err := pool.Enqueue()
	require.NoError(t, err)
	defer a.Drop()
	assert.True(t, g.walkEligible(cfg, ClassHeavy, a), "a normal ticket")

	_, err = pool.SetRank(a.Name, RankHold, "t", "test", time.Time{})
	require.NoError(t, err)
	assert.False(t, g.walkEligible(cfg, ClassHeavy, a), "on hold: not served anywhere")

	_, err = pool.SetRank(a.Name, RankNormal, "t", "test", time.Time{})
	require.NoError(t, err)
	cfg.Slots.Heavy = 0
	assert.False(t, g.walkEligible(cfg, ClassHeavy, a), "a class with no slot waits for the count to be raised")
}

// The serving order holds for the walk, per host: a ticket in front for a command the host takes has the first claim on it; one the
// host does not take holds nobody back; a back ticket is behind the normal ones; front is before normal.
func TestWalk_TheServingOrderIsPerHost(t *testing.T) {
	g, _ := testGate(t)
	cfg := g.Config()
	pool := g.pool(cfg, ClassHeavy)
	enq := func(argv ...string) *Ticket {
		tk, err := pool.EnqueueFor(WaiterInfo{PID: 1, Argv: argv})
		require.NoError(t, err)
		t.Cleanup(tk.Drop)
		return tk
	}
	goTest1 := enq("go", "test", "./a/")
	vitest := enq("vitest", "run")
	goTest2 := enq("go", "test", "./b/")
	snap := pool.peekSnapshot()
	goHost := AssistHost{Can: []string{"go test"}}
	jsHost := AssistHost{Can: []string{"vitest"}}

	assert.False(t, walkAhead(snap, goTest1.Name, goHost), "the first has nobody in front")
	assert.True(t, walkAhead(snap, goTest2.Name, goHost), "a go test in front of it takes the go host's slot first")
	assert.False(t, walkAhead(snap, goTest2.Name, AssistHost{Can: []string{"phpunit"}}), "a host that takes neither is not contested")
	assert.True(t, walkAhead(snap, goTest2.Name, jsHost), "the vitest ticket in front")
	assert.True(t, walkAhead(snap, vitest.Name, goHost), "the first go test is in front of the vitest one")
	assert.False(t, walkAhead(snap, vitest.Name, jsHost), "nothing else for the js host in front of vitest")

	_, err := pool.SetRank(goTest2.Name, RankFront, "t", "test", time.Time{})
	require.NoError(t, err)
	assert.False(t, walkAhead(pool.peekSnapshot(), goTest2.Name, goHost), "front goes before the normal ones")
	_, err = pool.SetRank(goTest1.Name, RankBack, "t", "test", time.Time{})
	require.NoError(t, err)
	assert.True(t, walkAhead(pool.peekSnapshot(), goTest1.Name, goHost), "back is after the others")
}

// A signal while the host is being asked ends the command: nothing is sent, nothing is left, the exit code is 128+n.
func TestWalk_ASignalDuringTheProbeEndsTheCommand(t *testing.T) {
	g, errs := testGate(t)
	sigs := make(chan os.Signal, 1)
	g.Signals = sigs
	work := workRepo(t)
	h := newAssistHost(t, filepath.Base(work))
	writeFile(t, h.remote("fake-wouldwait-sleep"), "", 0o644) // the host's answer takes 20 s
	marker := filepath.Join(t.TempDir(), "ran-here")
	var out bytes.Buffer
	done := make(chan int, 1)
	go func() {
		code, _ := g.Run(context.Background(), Options{Argv: sh(`touch ` + marker), Class: ClassHeavy, Dir: work, Env: cleanEnv(), Stdout: &out, Stderr: errs,
			Walk: &AssistWalk{Usable: func() map[string]AssistHost { return map[string]AssistHost{"air26": {Can: []string{"sh"}}} }, SSH: h.ssh}})
		done <- code
	}()
	eventuallyFor(t, 60*time.Second, "the probe to reach the host", func() bool { return exists(h.remote("ssh-hosts")) })
	sigs <- syscall.SIGINT
	select {
	case code := <-done:
		assert.Equal(t, 130, code, "128 + SIGINT")
	case <-time.After(30 * time.Second):
		t.Fatal("the signal did not end the wait for the host")
	}
	assert.NoFileExists(t, marker, "never run")
	dispatchRun{g: g, dir: g.Config().Dir}.nothingLeft(t)
}

// A signal while the snapshot is being prepared on the host (before the command is started there) ends it: the command is not started,
// and the ticket and the dispatch slot are let go.
func TestWalk_ASignalDuringThePreparationStartsNothing(t *testing.T) {
	g, errs := testGate(t)
	sigs := make(chan os.Signal, 1)
	g.Signals = sigs
	work := workRepo(t)
	h := newAssistHost(t, filepath.Base(work))
	writeFile(t, h.remote("git-add-slow"), "", 0o644) // the worktree is made after 6 s
	marker := filepath.Join(t.TempDir(), "ran-anywhere")
	var out bytes.Buffer
	done := make(chan int, 1)
	go func() {
		code, _ := g.Run(context.Background(), Options{Argv: sh(`touch ` + marker), Class: ClassHeavy, Dir: work, Env: cleanEnv(), Stdout: &out, Stderr: errs,
			Walk: &AssistWalk{Usable: func() map[string]AssistHost { return map[string]AssistHost{"air26": {Can: []string{"sh"}}} }, SSH: h.ssh}})
		done <- code
	}()
	eventuallyFor(t, 60*time.Second, "the preparation to start", func() bool { return exists(h.remote("prepare-started")) })
	sigs <- syscall.SIGINT
	select {
	case code := <-done:
		assert.Equal(t, 130, code, "128 + SIGINT")
	case <-time.After(30 * time.Second):
		t.Fatal("the signal did not end the preparation")
	}
	time.Sleep(8 * time.Second) // longer than the slow step: nothing may start after it
	assert.NoFileExists(t, marker, "never run, here or there")
	assert.Empty(t, h.listDir(t, "Assist/jobs.noindex"), "no job was started")
	dispatchRun{g: g, dir: g.Config().Dir}.nothingLeft(t)
}

// A host whose dispatch slots are all taken is not asked: no ssh is made, the command runs here.
func TestWalk_AHostWithNoFreeDispatchSlotIsNotProbed(t *testing.T) {
	r := walkRun(t, ClassHeavy, AssistHost{Slots: 1}, `echo ran-here`, func(h *assistHost, g *Gate) {
		s, err := DispatchPool(g.Config().Dir, "air26", 1, g.Now).TakeDispatch(WaiterInfo{})
		require.NoError(t, err)
		require.NotNil(t, s)
		t.Cleanup(func() { s.Release(nil) })
	})
	assert.Contains(t, r.out, "ran-here")
	assert.NoFileExists(t, r.h.remote("ssh-hosts"), "no probe, no send")
	_, held := HeldHost(r.dir, "air26", r.repo, r.g.Now())
	assert.False(t, held, "a full host is not held, it is only full")
}

// The send goes to the record's ssh alias; the pool, the holds and the record keep the name.
func TestWalk_TheSendGoesToTheRecordsSshAlias(t *testing.T) {
	r := walkRun(t, ClassHeavy, AssistHost{SSH: "air26-lan"}, `echo there`, nil)
	assert.Contains(t, r.out, "there")
	assert.Contains(t, mustRead(t, r.h.remote("ssh-hosts")), "air26-lan\n", "the probe and the run connect to the alias")
	assert.NotContains(t, mustRead(t, r.h.remote("ssh-hosts")), "air26\n")
	assert.Equal(t, "air26", r.lastDecision(t).Host, "the record names the host, not the alias")
}

// The host's own gate would make it wait: not held, the command runs here.
func TestWalk_AHostThatWouldMakeItWaitIsSkippedWithoutAHold(t *testing.T) {
	r := walkRun(t, ClassHeavy, AssistHost{}, `echo ran-here`, func(h *assistHost, g *Gate) {
		writeFile(t, h.remote("fake-wouldwait"), `{"would_wait":true,"reason":"slots","class":"heavy","ahead":1}`, 0o644)
	})
	assert.Contains(t, r.out, "ran-here")
	_, held := HeldHost(r.dir, "air26", r.repo, r.g.Now())
	assert.False(t, held)
	assert.Equal(t, "slot", r.lastDecision(t).Outcome)
	r.nothingLeft(t)
}

// A host over its load limit is held for HoldMin and the command runs here.
func TestWalk_ABusyHostIsHeldAndTheCommandRunsHere(t *testing.T) {
	r := walkRun(t, ClassHeavy, AssistHost{HoldMin: 7}, `echo ran-here`, func(h *assistHost, g *Gate) {
		writeFile(t, h.remote("fake-loadavg"), "{ 30.00 30.00 30.00 }", 0o644)
	})
	assert.Contains(t, r.out, "ran-here")
	hold, held := HeldHost(r.dir, "air26", r.repo, r.g.Now())
	require.True(t, held)
	assert.WithinDuration(t, r.g.Now().Add(7*time.Minute), hold.Until, 30*time.Second)
	r.nothingLeft(t)
}

// A host whose capability does not take the command is never asked.
func TestWalk_ACommandNoHostTakesStaysHere(t *testing.T) {
	g, errs := testGate(t)
	work := workRepo(t)
	h := newAssistHost(t, filepath.Base(work))
	var out bytes.Buffer
	code, err := g.Run(context.Background(), Options{Argv: sh(`echo ran-here`), Class: ClassHeavy, Dir: work, Env: cleanEnv(), Stdout: &out, Stderr: errs,
		Walk: &AssistWalk{Usable: func() map[string]AssistHost { return map[string]AssistHost{"air26": {Can: []string{"go test"}}} }, SSH: h.ssh}})
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "ran-here")
	assert.NoFileExists(t, h.remote("ssh-hosts"), "no ssh was made")
}

// A light ticket that a slot here takes at once is not offered.
func TestWalk_ALightTicketWithAFreeSlotStaysHere(t *testing.T) {
	g, errs := testGate(t)
	work := workRepo(t)
	h := newAssistHost(t, filepath.Base(work))
	var out bytes.Buffer
	code, err := g.Run(context.Background(), Options{Argv: sh(`echo ran-here`), Class: ClassLight, Dir: work, Env: cleanEnv(), Stdout: &out, Stderr: errs,
		Walk: &AssistWalk{Usable: func() map[string]AssistHost { return map[string]AssistHost{"air26": {Can: []string{"sh"}}} }, SSH: h.ssh}})
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "ran-here")
	assert.NoFileExists(t, h.remote("ssh-hosts"), "no ssh was made")
}

// A light ticket that has to wait here is offered at the first look that finds it waiting, and goes.
func TestWalk_ALightTicketThatWaitsGoesOut(t *testing.T) {
	g, errs := testGate(t)
	g.Poll = func() time.Duration { return 10 * time.Millisecond }
	work := workRepo(t)
	h := newAssistHost(t, filepath.Base(work))
	// the only light slot is held, so the ticket waits
	lp := &Pool{Dir: g.Config().Dir, Class: ClassLight, Size: g.Config().Slots.Size(ClassLight), Now: g.Now}
	slot, err := lp.TakeDispatch(WaiterInfo{})
	require.NoError(t, err)
	require.NotNil(t, slot)
	defer slot.Release(nil)
	var out bytes.Buffer
	done := make(chan int, 1)
	go func() {
		code, _ := g.Run(context.Background(), Options{Argv: sh(`echo there`), Class: ClassLight, Dir: work, Env: cleanEnv(), Stdout: &out, Stderr: errs,
			Walk: &AssistWalk{Usable: func() map[string]AssistHost { return map[string]AssistHost{"air26": {Can: []string{"sh"}}} }, SSH: h.ssh}})
		done <- code
	}()
	select {
	case code := <-done:
		assert.Equal(t, 0, code)
	case <-time.After(60 * time.Second):
		t.Fatal("the waiting light ticket was not offered to the host")
	}
}
