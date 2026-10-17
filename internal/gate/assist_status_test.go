package gate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- X2b-3: what status says about the assist hosts ----

func writeAssistFile(t *testing.T, dir string, hosts map[string]AssistHost) {
	t.Helper()
	_, err := EditAssist(dir, func(f *AssistFile) (bool, string, error) {
		f.Hosts = hosts
		return true, "set", nil
	})
	require.NoError(t, err)
}

// With no assist host the status has no assist part at all (the JSON a host without assist gives is what it was).
func TestStatus_NoAssistHostNoAssistPart(t *testing.T) {
	g, _ := testGate(t)
	st := g.Status()
	assert.Empty(t, st.Assist)
	assert.Empty(t, st.OutHeavy)
	assert.Empty(t, st.OutLight)
}

// The holds in force, the size of the dispatch slots, the runs out there, and the candidates of a waiter.
func TestStatus_AssistHostsHoldsAndCandidates(t *testing.T) {
	g, _ := testGate(t)
	dir := g.Config().Dir
	writeAssistFile(t, dir, map[string]AssistHost{
		"air26": {SSH: "air26", On: true, Slots: 3, Can: []string{"go test", "vitest"}, Gen: 1},
		"mserv": {SSH: "mserv", On: true, Can: []string{"phpunit"}, Gen: 1},
		"cold":  {SSH: "cold", On: false, Can: []string{"go test"}, Gen: 1},
		"new":   {SSH: "new", On: true, Can: []string{"go test"}, Gen: 1}, // on, but no check of it yet
		"full":  {SSH: "full", On: true, Slots: 1, Can: []string{"go test"}, Gen: 1},
	})
	fresh := func(sw string) AssistReady {
		return AssistReady{Gen: 1, At: g.Now(), Answer: AssistAnswer{Assist: true, Switch: sw}}
	}
	require.NoError(t, WriteReady(dir, map[string]AssistReady{"air26": fresh("on"), "mserv": fresh("on"), "cold": fresh("on"), "full": fresh("on")}))
	fullSlot, err := DispatchPool(dir, "full", 1, g.Now).TakeDispatch(WaiterInfo{})
	require.NoError(t, err)
	require.NotNil(t, fullSlot)
	t.Cleanup(func() { fullSlot.Release(nil) })
	require.NoError(t, HoldHost(dir, "air26", "purdex", 5*time.Minute, "busy", g.Now()))
	require.NoError(t, HoldHost(dir, "air26", "old", -time.Minute, "gone", g.Now())) // expired: not listed

	// a run out on air26 (a dispatch slot held) and a waiter that go test could be sent for
	slot, err := DispatchPool(dir, "air26", 3, g.Now).TakeDispatch(WaiterInfo{PID: 42, Argv: []string{"go", "test", "./..."}, Tag: "assist:abc"})
	require.NoError(t, err)
	require.NotNil(t, slot)
	t.Cleanup(func() { slot.Release(nil) })
	tk, err := g.pool(g.Config(), ClassHeavy).EnqueueFor(WaiterInfo{PID: 7, Argv: []string{"go", "test", "./a/"}})
	require.NoError(t, err)
	t.Cleanup(tk.Drop)

	st := g.Status()
	require.Len(t, st.Assist, 5)
	byHost := map[string]AssistHostStatus{}
	for _, h := range st.Assist {
		byHost[h.Host] = h
	}
	assert.Equal(t, 3, byHost["air26"].Size)
	assert.Equal(t, 2, byHost["mserv"].Size, "the default")
	assert.False(t, byHost["cold"].On)
	require.Len(t, byHost["air26"].Held, 1)
	assert.Equal(t, os.Getpid(), byHost["air26"].Held[0].PID, "the holder is the process that holds the slot")
	assert.Equal(t, "assist:abc", byHost["air26"].Held[0].Tag)
	assert.Equal(t, []string{"go", "test", "./..."}, byHost["air26"].Held[0].Argv)
	assert.Empty(t, byHost["mserv"].Held)
	require.Len(t, byHost["air26"].Holds, 1, "only the hold in force")
	assert.Equal(t, "purdex", byHost["air26"].Holds[0].Repo)
	assert.Equal(t, "busy", byHost["air26"].Holds[0].Why)

	require.Len(t, st.QueueHeavy, 1)
	assert.Equal(t, []string{"air26"}, st.QueueHeavy[0].Candidates,
		"on, checked on, a free slot and able to take go test; not mserv (php only), cold (off), new (never checked), full (no free slot)")
}

// A hold's reason is one short line, when it is written and when an old file is read.
func TestHold_TheReasonIsShortAndOneLine(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	long := strings.Repeat("x", 5000) + "\nsecond line\x1b[31m"
	require.NoError(t, HoldHost(dir, "air26", "purdex", time.Minute, long, now))
	h, held := HeldHost(dir, "air26", "purdex", now)
	require.True(t, held)
	assert.LessOrEqual(t, len([]rune(h.Why)), maxWhy+1)
	assert.NotContains(t, h.Why, "\n")
	assert.NotContains(t, h.Why, "\x1b")
	// a file written by something else, with a long reason, is cut at the read
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assist-hold", "mserv@purdex.json"),
		[]byte(`{"until":"`+now.Add(time.Hour).Format(time.RFC3339)+`","why":"`+strings.Repeat("y", 9000)+`"}`), 0o644))
	got := ListHolds(dir, now)
	require.Len(t, got, 2)
	for _, e := range got {
		assert.LessOrEqual(t, len([]rune(e.Why)), maxWhy+1, e.Host)
	}
}

// The queue and the out tickets of one answer are of one look: a ticket is in one of them, never both and never neither.
func TestStatus_TheQueueAndTheOutTicketsAreOfOneLook(t *testing.T) {
	g, _ := testGate(t)
	cfg := g.Config()
	pool := g.pool(cfg, ClassHeavy)
	tk, err := pool.EnqueueFor(WaiterInfo{PID: 5, Argv: []string{"go", "test"}})
	require.NoError(t, err)
	t.Cleanup(tk.Drop)
	dispatch := DispatchPool(cfg.Dir, "air26", 2, g.Now)
	stop := make(chan struct{})
	flipped := make(chan struct{})
	go func() { // the ticket goes out and comes back as fast as it can while status is read
		defer close(flipped)
		for {
			select {
			case <-stop:
				return
			default:
			}
			slot, err := pool.ServeOut(tk.Name, dispatch, WaiterInfo{PID: 5}, OutInfo{Host: "air26", Job: "j", At: g.Now()})
			if err != nil || slot == nil {
				continue
			}
			_ = tk.Return(slot, nil)
		}
	}()
	for i := 0; i < 300; i++ {
		st := g.Status()
		n := len(st.QueueHeavy) + len(st.OutHeavy)
		if n != 1 {
			close(stop)
			<-flipped
			t.Fatalf("look %d: the ticket is listed %d times (queue %d, out %d)", i, n, len(st.QueueHeavy), len(st.OutHeavy))
		}
	}
	close(stop)
	<-flipped
}

// A command that is out stays listed when assist.json cannot be read or has no host any more.
func TestStatus_AnOutCommandIsListedWithoutAnyHostRecord(t *testing.T) {
	g, _ := testGate(t)
	dir := g.Config().Dir
	pool := g.pool(g.Config(), ClassHeavy)
	tk, err := pool.EnqueueFor(WaiterInfo{PID: 9, Argv: []string{"go", "test"}})
	require.NoError(t, err)
	t.Cleanup(tk.Drop)
	slot, err := pool.ServeOut(tk.Name, DispatchPool(dir, "gone", 2, g.Now), WaiterInfo{PID: 9}, OutInfo{Host: "gone", Job: "j1", At: g.Now()})
	require.NoError(t, err)
	require.NotNil(t, slot)
	t.Cleanup(func() { slot.Release(nil) })
	st := g.Status() // no assist.json at all
	require.Len(t, st.OutHeavy, 1)
	assert.Equal(t, "gone", st.OutHeavy[0].Host)
	assert.Empty(t, st.Assist)
}

// A command that is out shows up with its host and job, and not in the queue.
func TestStatus_ACommandThatIsOutIsListedWithItsHost(t *testing.T) {
	g, errs := testGate(t)
	work := workRepo(t)
	h := newAssistHost(t, filepath.Base(work))
	dir := g.Config().Dir
	writeAssistFile(t, dir, map[string]AssistHost{"air26": {SSH: "air26", On: true, Can: []string{"sh"}}})
	release := h.remote("release") // the command waits on the host until the test lets it go
	var out bytes.Buffer
	done := make(chan int, 1)
	go func() {
		code, _ := g.Run(context.Background(), Options{Argv: sh(`while [ ! -e ` + release + ` ]; do sleep 0.05; done`), Class: ClassHeavy, Dir: work, Env: cleanEnv(), Stdout: &out, Stderr: errs,
			Assist: &AssistTarget{Host: "air26", SSH: h.ssh}})
		done <- code
	}()
	eventuallyFor(t, 60*time.Second, "the command to be out", func() bool { return len(g.Status().OutHeavy) == 1 })
	st := g.Status()
	assert.Empty(t, st.QueueHeavy, "an out ticket is not in the queue")
	o := st.OutHeavy[0]
	assert.Equal(t, "air26", o.Host)
	assert.NotEmpty(t, o.Job)
	assert.Equal(t, ClassHeavy, o.Class)
	assert.Equal(t, 1, len(st.Assist[0].Held), "and holds one of air26's dispatch slots")
	writeFile(t, release, "", 0o644)
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the command did not finish")
	}
	assert.Empty(t, g.Status().OutHeavy)
}
