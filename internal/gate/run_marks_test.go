package gate

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests were the second half of out_test.go (X2a-1). They are about Gate.Run, not about out tickets, so they stay when the assist
// periphery goes (WWD-9 9b-2); they go with the gate core (9a-4).

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
	}{{append(cleanEnv(), AdmittedMarkEnv+"=n0nce"), true}, {cleanEnv(), false}} {
		var out, errb bytes.Buffer
		code, _ := g.Run(context.Background(), Options{Argv: sh("echo from-command >&2"), Class: ClassHeavy, Env: tc.env, Stdout: &out, Stderr: &errb})
		require.Equal(t, 0, code)
		if tc.want {
			assert.Equal(t, AdmittedMark("n0nce")+"from-command\n", errb.String())
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
	code, _ := g.Run(context.Background(), Options{Argv: sh("true"), Class: ClassHeavy, Env: append(cleanEnv(), AdmittedMarkEnv+"=n0nce"), WaitMax: 200 * time.Millisecond, Stdout: &bytes.Buffer{}, Stderr: &errb})
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
	code, _ := g.Run(context.Background(), Options{Argv: []string{"notexec"}, Real: bin, Class: ClassHeavy, Env: append(cleanEnv(), AdmittedMarkEnv+"=n0nce"), Stdout: &bytes.Buffer{}, Stderr: &errb})
	assert.Equal(t, 126, code)
	assert.Contains(t, errb.String(), NotStartedMark("n0nce"))
	assert.True(t, strings.HasPrefix(errb.String(), AdmittedMark("n0nce")))
}

// The command cannot learn the nonce, so it cannot print a mark the sender would believe.
func TestRun_TheCommandDoesNotSeeTheNonce(t *testing.T) {
	g, _ := testGate(t)
	var out bytes.Buffer
	code, _ := g.Run(context.Background(), Options{Argv: sh("echo \"[$PDX_GATE_ADMITTED_MARK]\""), Class: ClassHeavy, Env: append(cleanEnv(), AdmittedMarkEnv+"=secret"), Stdout: &out, Stderr: &bytes.Buffer{}})
	require.Equal(t, 0, code)
	assert.Equal(t, "[]\n", out.String())
}

// A slot that is only obtained after the deadline (the look itself waited for the admit lock) is let go and the command is not run.
func TestRun_WaitMaxCountsFromTheStartOfTheWait(t *testing.T) {
	g, _ := testGate(t)
	unlock, err := g.pool(g.Config(), ClassHeavy).lockAdmit() // the first look will wait here, longer than the deadline
	require.NoError(t, err)
	dir := t.TempDir()
	done := make(chan int, 1)
	go func() {
		code, _ := g.Run(context.Background(), Options{Argv: sh("touch " + dir + "/ran"), Class: ClassHeavy, Env: cleanEnv(), WaitMax: 200 * time.Millisecond, Stdout: &bytes.Buffer{}})
		done <- code
	}()
	time.Sleep(500 * time.Millisecond)
	unlock() // a slot is free, but it is past the deadline
	assert.Equal(t, ExitRefused, <-done)
	assert.False(t, exists(dir+"/ran"))
	assert.Empty(t, g.pool(g.Config(), ClassHeavy).Held(), "the slot that came late was let go")
}
