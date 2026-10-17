package gate

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
		"air26": {SSH: "air26", On: true, Slots: 3, Can: []string{"go test", "vitest"}},
		"mserv": {SSH: "mserv", On: true, Can: []string{"phpunit"}},
		"cold":  {SSH: "cold", On: false, Can: []string{"go test"}},
	})
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
	require.Len(t, st.Assist, 3)
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
	assert.Equal(t, []string{"air26"}, st.QueueHeavy[0].Candidates, "on and able to take go test; not mserv (php only), not cold (off)")
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
