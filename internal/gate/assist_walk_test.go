package gate

import (
	"bytes"
	"context"
	"path/filepath"
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
