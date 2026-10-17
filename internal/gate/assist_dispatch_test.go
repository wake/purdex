package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- X2a-3: a command sent out and what a failure, a 75 or a reclaim does to its ticket, the dispatch slot and the host ----

type dispatchRun struct {
	code int
	out  string
	errs string
	g    *Gate
	dir  string // the gate directory
	h    *assistHost
	repo string
}

// sendOut runs argv through g.Run with the assist host set; the command is a shell script.
func sendOut(t *testing.T, mode string, script string, prep func(h *assistHost, g *Gate)) dispatchRun {
	t.Helper()
	g, errs := testGate(t)
	work := workRepo(t)
	h := newAssistHost(t, filepath.Base(work))
	if mode != "" {
		writeFile(t, h.remote("fake-pdx-mode"), mode, 0o644)
	}
	if prep != nil {
		prep(h, g)
	}
	var out bytes.Buffer
	code, err := g.Run(context.Background(), Options{
		Argv: sh(script), Class: ClassHeavy, Dir: work, Env: cleanEnv(), Stdout: &out, Stderr: errs,
		Assist: &AssistTarget{Host: "air26", SSH: h.ssh, Repo: h.repo},
	})
	require.NoError(t, err)
	return dispatchRun{code: code, out: out.String(), errs: errs.String(), g: g, dir: g.Config().Dir, h: h, repo: h.repo}
}

// nothingLeft: no ticket in the queue, no .out, no dispatch slot held (a slot a failed run kept would stop the next one).
func (r dispatchRun) nothingLeft(t *testing.T) {
	t.Helper()
	snap, err := (&Pool{Dir: r.dir, Class: ClassHeavy, Size: 2, Now: r.g.Now}).Snapshot()
	require.NoError(t, err)
	assert.Empty(t, snap.Queue, "the ticket is gone")
	assert.Empty(t, snap.Out, "and no longer out")
	assert.Empty(t, DispatchPool(r.dir, "air26", 2, r.g.Now).Held(), "the dispatch slot is let go")
}

func (r dispatchRun) lastDecision(t *testing.T) Decision {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, decisionsFile))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var d Decision
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &d))
	return d
}

// A command that started on the host: its exit code is the result, the host is in the record, nothing is held, nothing is left.
func TestDispatch_TheCommandsResultIsTheResult(t *testing.T) {
	r := sendOut(t, "", `echo there; exit 7`, nil)
	assert.Equal(t, 7, r.code)
	assert.Contains(t, r.out, "there")
	r.nothingLeft(t)
	_, held := HeldHost(r.dir, "air26", r.repo, r.g.Now())
	assert.False(t, held)
	d := r.lastDecision(t)
	assert.Equal(t, "assist", d.Outcome)
	assert.Equal(t, "air26", d.Host)
	assert.Equal(t, 7, d.Exit)
	assert.Greater(t, d.TransferS, 0.0)
	assert.False(t, d.Reclaimed)
}

// An exit 75 from a command that did start is the command's, not a "not admitted".
func TestDispatch_A75OfTheCommandItselfIsNotAFailure(t *testing.T) {
	r := sendOut(t, "", `exit 75`, nil)
	assert.Equal(t, 75, r.code)
	_, held := HeldHost(r.dir, "air26", r.repo, r.g.Now())
	assert.False(t, held)
	assert.Equal(t, "assist", r.lastDecision(t).Outcome)
}

// ssh failing before anything started: the command runs here, as if it had never been sent, the host is held 5 minutes.
func TestDispatch_AFailureBeforeTheStartRunsItHereAndHoldsTheHost(t *testing.T) {
	r := sendOut(t, "", `echo ran-here`, func(h *assistHost, g *Gate) { t.Setenv("FAKE_SSH_FAIL", "1") })
	assert.Equal(t, 0, r.code)
	assert.Contains(t, r.out, "ran-here")
	assert.Contains(t, r.errs, "waiting on this host")
	hold, held := HeldHost(r.dir, "air26", r.repo, r.g.Now())
	require.True(t, held)
	assert.WithinDuration(t, r.g.Now().Add(holdAfterFailure), hold.Until, 30*time.Second)
	assert.NotEmpty(t, hold.Why)
	r.nothingLeft(t)
	assert.Equal(t, "slot", r.lastDecision(t).Outcome)
}

// The host's gate not admitting in time (75) holds it for 2 minutes only.
func TestDispatch_ANotAdmittedHoldsTheHostForLess(t *testing.T) {
	r := sendOut(t, "wait75", `echo ran-here`, nil)
	assert.Contains(t, r.out, "ran-here")
	hold, held := HeldHost(r.dir, "air26", r.repo, r.g.Now())
	require.True(t, held)
	assert.WithinDuration(t, r.g.Now().Add(holdAfterNoSlot), hold.Until, 30*time.Second)
	r.nothingLeft(t)
}

// The host took the run back: the message, no hold, and the command runs again here.
func TestDispatch_AReclaimSendsItBackToTheQueue(t *testing.T) {
	script := `if [ -n "$PDX_GATE_TAG" ]; then touch "$HOME/Assist/jobs.noindex/${PDX_GATE_TAG#assist:}.reclaimed"; exit 143; fi; echo ran-here`
	r := sendOut(t, "", script, nil)
	assert.Equal(t, 0, r.code)
	assert.Contains(t, r.out, "ran-here")
	assert.Contains(t, r.errs, "air26 was taken back; waiting again on mlab")
	_, held := HeldHost(r.dir, "air26", r.repo, r.g.Now())
	assert.False(t, held, "a reclaim is not a failure of the host")
	r.nothingLeft(t)
	d := r.lastDecision(t)
	assert.True(t, d.Reclaimed)
	assert.Equal(t, "air26", d.Host)
	assert.Equal(t, "slot", d.Outcome, "it ran here in the end")
}

// A held host is not tried: no ssh at all.
func TestDispatch_AHeldHostIsNotTried(t *testing.T) {
	g, errs := testGate(t)
	work := workRepo(t)
	h := newAssistHost(t, filepath.Base(work))
	t.Setenv("FAKE_SSH_FAIL", "")
	require.NoError(t, HoldHost(g.Config().Dir, "air26", h.repo, time.Minute, "earlier failure", g.Now()))
	var out bytes.Buffer
	code, err := g.Run(context.Background(), Options{Argv: sh(`echo ran-here`), Class: ClassHeavy, Dir: work, Env: cleanEnv(), Stdout: &out, Stderr: errs,
		Assist: &AssistTarget{Host: "air26", SSH: h.ssh, Repo: h.repo}})
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, errs.String(), "held off")
	assert.NotContains(t, errs.String(), "running on air26")
	assert.Empty(t, h.listDir(t, "Assist/jobs.noindex"))
}

// All dispatch slots taken: the ticket keeps its place and the command waits here.
func TestDispatch_NoFreeDispatchSlotKeepsTheTicketHere(t *testing.T) {
	g, errs := testGate(t)
	work := workRepo(t)
	h := newAssistHost(t, filepath.Base(work))
	dp := DispatchPool(g.Config().Dir, "air26", 1, g.Now)
	s, err := dp.TakeDispatch(WaiterInfo{})
	require.NoError(t, err)
	require.NotNil(t, s)
	defer s.Release(nil)
	var out bytes.Buffer
	code, err := g.Run(context.Background(), Options{Argv: sh(`echo ran-here`), Class: ClassHeavy, Dir: work, Env: cleanEnv(), Stdout: &out, Stderr: errs,
		Assist: &AssistTarget{Host: "air26", SSH: h.ssh, Repo: h.repo, Slots: 1}})
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, errs.String(), "dispatch slots are taken")
	assert.Contains(t, out.String(), "ran-here")
}

// Holds keep the later until, and expire.
func TestHoldHost_KeepsTheLaterUntilAndExpires(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	require.NoError(t, HoldHost(dir, "air26", "purdex", 5*time.Minute, "long", now))
	require.NoError(t, HoldHost(dir, "air26", "purdex", 2*time.Minute, "short", now))
	h, held := HeldHost(dir, "air26", "purdex", now)
	require.True(t, held)
	assert.Equal(t, "long", h.Why)
	_, held = HeldHost(dir, "air26", "purdex", now.Add(6*time.Minute))
	assert.False(t, held)
	_, held = HeldHost(dir, "air26", "other", now)
	assert.False(t, held, "per repo")
}
