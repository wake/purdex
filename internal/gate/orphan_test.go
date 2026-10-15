package gate

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waiting is the number of commands queued for a heavy slot, read without asking lsof who holds the locks (a lsof left behind
// at the end of a test holds the test binary's output open).
func waiting(g *Gate) int { return g.StatusWith(HoldersOrphaned).WaitHeavy }

// parentRig makes the gate's parent a number the test moves.
func parentRig(g *Gate, start int32) *atomic.Int32 {
	var p atomic.Int32
	p.Store(start)
	g.Getppid = func() int { return int(p.Load()) }
	return &p
}

func waiterRig(t *testing.T, g *Gate, o Options) (done <-chan runResult, ran func() bool, release func()) {
	t.Helper()
	g.Holders = nil
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	occupied, rel0 := holdSlot(t, g, ClassHeavy, "occupant")
	// releasing means the occupant has really ended: the directory it polls goes away with the test
	rel := func() { rel0(); <-occupied }
	marker := t.TempDir() + "/ran"
	o.Argv, o.Class = sh("touch "+marker), ClassHeavy
	done = runAsync(g, t.Context(), o)
	eventually(t, "the command to queue", func() bool { return waiting(g) == 1 })
	return done, func() bool { return exists(marker) }, rel
}

// A command that waits for a slot while its caller dies is taken off the queue and not run (#2973).
func TestWait_ACallerThatIsGoneTakesItsTicketAway(t *testing.T) {
	g, buf := testGate(t)
	parent := parentRig(g, 4242)
	done, ran, release := waiterRig(t, g, Options{})
	parent.Store(1) // the shell that called it is gone: init has it now

	select {
	case r := <-done:
		assert.Equal(t, ExitOrphaned, r.code)
	case <-time.After(10 * time.Second):
		t.Fatal("the orphaned command is still queued")
	}
	assert.Equal(t, 0, waiting(g), "its place in the queue is free")
	release()
	time.Sleep(100 * time.Millisecond)
	assert.False(t, ran(), "and it was never run")
	assert.Contains(t, buf.String(), "is gone")

	var orphaned bool
	for _, r := range readDecisions(t, g) {
		orphaned = orphaned || r["outcome"] == "orphaned"
	}
	assert.True(t, orphaned, "the decision record says why it did not run")
}

// Another reaper (not only init) taking the process over is the same thing.
func TestWait_AnyChangeOfParentIsAnOrphan(t *testing.T) {
	g, _ := testGate(t)
	parent := parentRig(g, 4242)
	done, ran, release := waiterRig(t, g, Options{})
	parent.Store(777)
	r := <-done
	assert.Equal(t, ExitOrphaned, r.code)
	release()
	assert.False(t, ran())
}

// A caller that is still there changes nothing: the command waits its turn and runs.
func TestWait_ACallerThatStaysDoesNotLoseItsPlace(t *testing.T) {
	g, _ := testGate(t)
	parentRig(g, 4242)
	done, ran, release := waiterRig(t, g, Options{})
	time.Sleep(150 * time.Millisecond) // several polls
	assert.Equal(t, 1, waiting(g))
	release()
	r := <-done
	assert.Equal(t, 0, r.code)
	assert.True(t, ran())
}

// A command that never had a parent to lose (started by init) keeps waiting.
func TestWait_ACommandStartedByInitKeepsWaiting(t *testing.T) {
	g, _ := testGate(t)
	parent := parentRig(g, 1)
	done, ran, release := waiterRig(t, g, Options{})
	parent.Store(555)
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, 1, waiting(g))
	release()
	assert.Equal(t, 0, (<-done).code)
	assert.True(t, ran())
}

// A job meant to outlive its caller says so, and waits on.
func TestWait_KeepOrphanEnvLetsADetachedJobWait(t *testing.T) {
	g, _ := testGate(t)
	parent := parentRig(g, 4242)
	done, ran, release := waiterRig(t, g, Options{Env: append(cleanEnv(), KeepOrphanEnv+"=1")})
	parent.Store(1)
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, 1, waiting(g))
	release()
	assert.Equal(t, 0, (<-done).code)
	assert.True(t, ran())
}

// KeepOrphanEnv=0 is the default.
func TestOrphanCheckOn(t *testing.T) {
	assert.True(t, orphanCheckOn([]string{"A=1"}))
	assert.True(t, orphanCheckOn([]string{KeepOrphanEnv + "="}))
	assert.True(t, orphanCheckOn([]string{KeepOrphanEnv + "=0"}))
	assert.False(t, orphanCheckOn([]string{KeepOrphanEnv + "=1"}))
	assert.False(t, orphanCheckOn([]string{KeepOrphanEnv + "=yes"}))
	_ = os.Getpid
	require.NotNil(t, New(Defaults()).Getppid)
}
