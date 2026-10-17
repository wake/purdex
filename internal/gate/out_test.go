package gate

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- X2a-1: out tickets, dispatch slots, --wait-max, the admitted mark, the tag (assist-dispatch plan X2a) ----

// An out ticket is not in the serving order: nobody is behind it, it is never served, it is listed as out; back at its place it is
// served where its name and rank put it. Mutation gates: Snapshot lists an out ticket in Queue → red; ClearOut makes a new ticket → the
// name changes (red).
func TestOut_LeavesTheServingOrderAndComesBackAtItsPlace(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 3)
	require.NoError(t, rp.MarkOut(ts[0].Name, OutInfo{Host: "air26", Job: "j1"}))
	assert.Equal(t, nameOf(ts, 1, 2), rp.order(t), "the out ticket is not in the serving order")
	snap, err := rp.Snapshot()
	require.NoError(t, err)
	require.Len(t, snap.Out, 1)
	assert.Equal(t, ts[0].Name, snap.Out[0].Name)
	assert.Equal(t, OutInfo{Host: "air26", Job: "j1", At: snap.Out[0].Out.At}, snap.Out[0].Out)
	assert.False(t, snap.Out[0].Out.At.IsZero())
	assert.True(t, rp.IsOut(ts[0].Name))
	assert.Equal(t, 2, rp.PeekWaiting(), "an out ticket is not waiting for a slot here")

	require.NoError(t, rp.ClearOut(ts[0].Name))
	assert.Equal(t, nameOf(ts, 0, 1, 2), rp.order(t), "back at its own place, ahead of the later ones")
	assert.False(t, rp.IsOut(ts[0].Name))
}

// A ticket that is out is not served, whatever is free.
func TestOut_IsNotServed(t *testing.T) {
	rp := newRankPool(t, 2)
	ts := rp.enqueue(t, 1)
	require.NoError(t, rp.MarkOut(ts[0].Name, OutInfo{Host: "air26"}))
	tk, err := rp.TryTakeRanked(ts[0])
	require.NoError(t, err)
	assert.Nil(t, tk.Slot)
	assert.True(t, tk.Out)
	require.NoError(t, rp.ClearOut(ts[0].Name))
	tk, err = rp.TryTakeRanked(ts[0])
	require.NoError(t, err)
	assert.NotNil(t, tk.Slot)
	tk.Slot.Release(nil)
}

// The rank set while it is out is kept for its return.
func TestOut_KeepsItsRank(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 3)
	require.NoError(t, rp.MarkOut(ts[2].Name, OutInfo{Host: "air26"}))
	rp.set(t, ts[2], RankFront, time.Time{})
	require.NoError(t, rp.ClearOut(ts[2].Name))
	assert.Equal(t, nameOf(ts, 2, 0, 1), rp.order(t))
}

// Marking or clearing a ticket that is not in the queue says so and leaves no file behind.
func TestOut_NotWaiting(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 1)
	name := ts[0].Name
	ts[0].Drop()
	assert.ErrorIs(t, rp.MarkOut(name, OutInfo{Host: "air26"}), ErrNotWaiting)
	assert.ErrorIs(t, rp.ClearOut(name), ErrNotWaiting)
	assert.NoFileExists(t, rp.outPath(name))
}

// Drop removes the ticket with its .out and its .rank in one step; a dead gate's ticket is cleaned up with its sidecars.
func TestOut_DropAndADeadGatesTicketTakeTheSidecarsWithThem(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 2)
	for _, tk := range ts {
		require.NoError(t, rp.MarkOut(tk.Name, OutInfo{Host: "air26"}))
		rp.set(t, tk, RankBack, time.Time{})
	}
	ts[0].Drop()
	assert.NoFileExists(t, rp.outPath(ts[0].Name))
	assert.NoFileExists(t, rp.rankPath(ts[0].Name))
	// the second gate dies: its lock is freed, the next scan under the admit lock removes ticket, rank and out
	require.NoError(t, ts[1].f.Close())
	_, err := rp.Snapshot()
	require.NoError(t, err)
	assert.NoFileExists(t, rp.outPath(ts[1].Name))
	assert.NoFileExists(t, rp.rankPath(ts[1].Name))
	assert.NoFileExists(t, ts[1].path)
}

// Snapshot, SetRank, MarkOut / ClearOut and Drop at once leave no sidecar behind (run under -race).
func TestOut_RacingOperationsLeaveNoOrphanSidecar(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 8)
	var wg sync.WaitGroup
	for i, tk := range ts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 20; r++ {
				_ = rp.MarkOut(tk.Name, OutInfo{Host: "air26"})
				_, _ = rp.Snapshot()
				_, _ = rp.SetRank(tk.Name, RankBack, "t", "", time.Time{})
				_ = rp.ClearOut(tk.Name)
			}
			if i%2 == 0 {
				_ = rp.MarkOut(tk.Name, OutInfo{Host: "air26"})
				tk.Drop()
			}
		}()
	}
	wg.Wait()
	ents, err := os.ReadDir(rp.queueDir())
	require.NoError(t, err)
	live := map[string]bool{}
	for _, tk := range ts {
		if tk.f != nil {
			live[tk.Name] = true
		}
	}
	for _, e := range ents {
		base := strings.SplitN(e.Name(), ".", 2)[0]
		assert.True(t, live[base], "%s outlived its ticket", e.Name())
	}
}

// ---- dispatch slots ----

func TestDispatch_SlotsAreTakenLowestFirstAndLimitedToTheSize(t *testing.T) {
	dir := t.TempDir()
	now := time.Now
	dp := DispatchPool(dir, "air26", 2, now)
	a, err := dp.TakeDispatch(WaiterInfo{PID: 1, Argv: []string{"x"}, Tag: "assist:j1"})
	require.NoError(t, err)
	require.NotNil(t, a)
	b, err := dp.TakeDispatch(WaiterInfo{})
	require.NoError(t, err)
	require.NotNil(t, b)
	assert.Equal(t, 1, a.K)
	assert.Equal(t, 2, b.K)
	assert.FileExists(t, dir+"/assist-air26-1.lock")
	c, err := dp.TakeDispatch(WaiterInfo{})
	require.NoError(t, err)
	assert.Nil(t, c, "mlab never has more runs out on a host than the size")
	a.Release(nil)
	c, err = dp.TakeDispatch(WaiterInfo{})
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Equal(t, 1, c.K)
	b.Release(nil)
	c.Release(nil)
	// another host's pool is another set of slots; a size of 0 sends nothing
	other := DispatchPool(dir, "mserv", 1, now)
	o, _ := other.TakeDispatch(WaiterInfo{})
	assert.NotNil(t, o)
	o.Release(nil)
	none, _ := DispatchPool(dir, "air26", 0, now).TakeDispatch(WaiterInfo{})
	assert.Nil(t, none)
}

// The holder record of a dispatch slot says what it is for.
func TestDispatch_RecordCarriesTheTag(t *testing.T) {
	dp := DispatchPool(t.TempDir(), "air26", 1, time.Now)
	s, err := dp.TakeDispatch(WaiterInfo{PID: 7, Argv: []string{"vitest"}, Tag: "assist:j9"})
	require.NoError(t, err)
	defer s.Release(nil)
	held := dp.Held()
	require.Len(t, held, 1)
	assert.Equal(t, "assist:j9", held[0].Tag)
}

// ServeOut takes the dispatch slot and marks the ticket together; with no slot free the ticket keeps its place unmarked; Return lets
// the slot go BEFORE the ticket is back. Mutation gates: ServeOut marks without the slot → red; Return clears .out first → red.
func TestDispatch_ServeOutAndReturn(t *testing.T) {
	rp := newRankPool(t, 1)
	ts := rp.enqueue(t, 2)
	dp := DispatchPool(rp.Dir, "air26", 1, rp.Now)
	s1, err := rp.ServeOut(ts[0].Name, dp, WaiterInfo{PID: 1}, OutInfo{Host: "air26", Job: "j1"})
	require.NoError(t, err)
	require.NotNil(t, s1)
	assert.True(t, rp.IsOut(ts[0].Name))
	s2, err := rp.ServeOut(ts[1].Name, dp, WaiterInfo{PID: 2}, OutInfo{Host: "air26", Job: "j2"})
	require.NoError(t, err)
	assert.Nil(t, s2, "the host's dispatch slots are all out")
	assert.False(t, rp.IsOut(ts[1].Name), "an unserved ticket is not marked")
	_, err = rp.ServeOut(ts[0].Name, dp, WaiterInfo{}, OutInfo{})
	assert.ErrorIs(t, err, ErrNotWaiting, "a ticket that is out is not sent again")

	var slotFreeWhenBack, outWhenReleased bool
	require.NoError(t, ts[0].Return(s1, func(string) []Straggler { // called while the slot is let go
		outWhenReleased = rp.IsOut(ts[0].Name)
		return nil
	}))
	assert.True(t, outWhenReleased, "the dispatch slot is let go while the ticket is still out")
	if free, err := dp.TakeDispatch(WaiterInfo{}); err == nil && free != nil {
		slotFreeWhenBack = true
		free.Release(nil)
	}
	assert.True(t, slotFreeWhenBack, "the dispatch slot is let go when the ticket is back")
	assert.False(t, rp.IsOut(ts[0].Name))
	assert.Equal(t, nameOf(ts, 0, 1), rp.order(t))
}

// ---- --wait-max, the admitted mark, the tag (through the gate) ----

// A command that is not admitted within WaitMax is not run, exits 75 and leaves no ticket. Mutation gate: no deadline → it waits for
// the holder (red).
func TestRun_WaitMaxEndsTheWaitWithoutRunning(t *testing.T) {
	g, errs := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	release, holder := holderOf(t, g)
	defer func() { release(); <-holder }()
	dir := t.TempDir()
	start := time.Now()
	code, err := g.Run(context.Background(), Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy, Env: cleanEnv(), WaitMax: 300 * time.Millisecond, Stdout: &bytes.Buffer{}})
	require.NoError(t, err)
	assert.Equal(t, ExitRefused, code)
	assert.Less(t, time.Since(start), 3*time.Second)
	assert.False(t, exists(dir+"/ran"))
	assert.Contains(t, errs.String(), "not admitted within")
	ws, _ := g.Queue()
	assert.Empty(t, ws, "the ticket is gone")
	assert.Equal(t, "wait-max", lastDecisionOf(t, g, "touch")["outcome"])
}

// Admitted in time, the command runs as always.
func TestRun_WaitMaxDoesNotStopAnAdmittedCommand(t *testing.T) {
	g, _ := testGate(t)
	code, _ := g.Run(context.Background(), Options{Argv: sh("true"), Class: ClassHeavy, Env: cleanEnv(), WaitMax: time.Minute, Stdout: &bytes.Buffer{}})
	assert.Equal(t, 0, code)
}

// With PDX_GATE_ADMITTED_MARK=1 the mark is the first thing on stderr, before the command prints; without it there is none.
// Mutation gate: the mark written after the start → the command's own line can come first (red).
func TestRun_AdmittedMarkComesBeforeTheCommandsOutput(t *testing.T) {
	g, _ := testGate(t)
	for _, tc := range []struct {
		env  []string
		want bool
	}{{append(cleanEnv(), AdmittedMarkEnv+"=1"), true}, {cleanEnv(), false}} {
		var out, errb bytes.Buffer
		code, _ := g.Run(context.Background(), Options{Argv: sh("echo from-command >&2"), Class: ClassHeavy, Env: tc.env, Stdout: &out, Stderr: &errb})
		require.Equal(t, 0, code)
		if tc.want {
			assert.Equal(t, AdmittedMark+"from-command\n", errb.String())
		} else {
			assert.Equal(t, "from-command\n", errb.String())
		}
	}
}

// A command refused because the wait ran out never prints the mark.
func TestRun_NoMarkWhenNothingStarted(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	release, holder := holderOf(t, g)
	defer func() { release(); <-holder }()
	var errb bytes.Buffer
	code, _ := g.Run(context.Background(), Options{Argv: sh("true"), Class: ClassHeavy, Env: append(cleanEnv(), AdmittedMarkEnv+"=1"), WaitMax: 200 * time.Millisecond, Stdout: &bytes.Buffer{}, Stderr: &errb})
	assert.Equal(t, ExitRefused, code)
	assert.NotContains(t, errb.String(), "pdx-gate-admitted")
}

// PDX_GATE_TAG is on the waiting ticket and on the slot's record, so `pdx gate status` can show the run as an assist holder.
func TestRun_TagIsOnTheTicketAndTheHolder(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	dir := t.TempDir()
	env := append(cleanEnv(), TagEnv+"=assist:job-7")
	holderDone := runAsync(g, context.Background(), Options{Argv: sh(holdScript(dir, "h")), Class: ClassHeavy, Env: env})
	eventually(t, "the holder", func() bool { return exists(dir + "/started-h") })
	waiter := runAsync(g, context.Background(), Options{Argv: sh("true"), Class: ClassHeavy, Env: env})
	ws := queuedTickets(t, g, 1)
	assert.Equal(t, "assist:job-7", ws[0].Tag, "the ticket carries the tag")
	held := g.pool(g.Config(), ClassHeavy).Held()
	require.Len(t, held, 1)
	assert.Equal(t, "assist:job-7", held[0].Tag, "so does the holder")
	require.NoError(t, os.WriteFile(dir+"/go-h", nil, 0o644))
	<-holderDone
	<-waiter
}

// A start that fails after the mark says so with the second mark, so the sender knows nothing ran.
func TestRun_AFailedStartTakesTheMarkBack(t *testing.T) {
	g, _ := testGate(t)
	bin := t.TempDir() + "/notexec"
	require.NoError(t, os.WriteFile(bin, []byte("x"), 0o644))
	var errb bytes.Buffer
	code, _ := g.Run(context.Background(), Options{Argv: []string{"notexec"}, Real: bin, Class: ClassHeavy, Env: append(cleanEnv(), AdmittedMarkEnv+"=1"), Stdout: &bytes.Buffer{}, Stderr: &errb})
	assert.Equal(t, 126, code)
	assert.Contains(t, errb.String(), NotStartedMark)
	assert.True(t, strings.HasPrefix(errb.String(), AdmittedMark))
}
