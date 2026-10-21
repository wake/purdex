package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"
)

// WWD-9 S0-a: the gate lets everything through. Every form that used to take a slot (the script form, --auto, the shims' --tool) runs the
// command by exec at once, with the gate full and a queue in front; every would-wait answers "it will not wait". The entry table is in the PR.

// The gate now ends in exec, which would replace the test process: a test that has not put its own gateExec in (toolEnv does) gets an error
// it can see instead.
func init() {
	gateExec = func(path string, _ []string, _ []string) error {
		return errors.New("gateExec was not replaced by the test: it would have replaced the test process")
	}
}

// fullGate takes the one heavy slot and the light slots of the gate at dir and holds them until the test ends: a command that asked for a
// slot now would wait (this is what the tests below do not do any more).
func fullGate(t *testing.T, gateDir string) {
	t.Helper()
	for _, c := range []struct {
		class gate.Class
		size  int
	}{{gate.ClassHeavy, 1}, {gate.ClassLight, 3}} {
		pool := &gate.Pool{Dir: gateDir, Class: c.class, Size: c.size, Now: time.Now}
		for i := 0; i < c.size; i++ {
			tk, err := pool.Enqueue()
			require.NoError(t, err)
			s, _, err := pool.TryTake(tk)
			require.NoError(t, err)
			require.NotNil(t, s)
			tk.Drop()
			t.Cleanup(func() { s.Release(nil) })
		}
	}
}

// returnsWithin runs f and fails the test if it has not returned in d: the old gate waited for a slot, without end.
func returnsWithin(t *testing.T, d time.Duration, f func() int) int {
	t.Helper()
	done := make(chan int, 1)
	go func() { done <- f() }()
	select {
	case code := <-done:
		return code
	case <-time.After(d):
		t.Fatal("the command was held for a slot: the gate is not a pass-through")
		return -1
	}
}

func gateRun(t *testing.T, args ...string) (code int, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = returnsWithin(t, 5*time.Second, func() int { return gateMain(args, &out, &errb) })
	return code, errb.String()
}

// R1: the script form, `pdx gate [--class C] -- <cmd>`.
func TestGateDirect_TheScriptFormRunsAtOnceWhateverTheClassAndTheState(t *testing.T) {
	_, cfg, gateDir, got := toolEnv(t, "go", quietGate)
	fullGate(t, gateDir)
	for _, class := range [][]string{nil, {"--class", "heavy"}, {"--class", "light"}} {
		*got = execCall{}
		args := append(append([]string{"--config", cfg}, class...), "--real", "/bin/echo", "--", "echo", "hi")
		code, errb := gateRun(t, args...)
		assert.Equal(t, 0, code, "%v: %s", class, errb)
		assert.Equal(t, "/bin/echo", got.path, "%v", class)
		assert.Equal(t, []string{"echo", "hi"}, got.argv, "%v", class)
	}
}

// R2: `pdx gate --auto -- <cmd>` (the mod's rewrite): a heavy command, a light one, one the gate would not take in: all run at once.
func TestGateDirect_AutoRunsAtOnce(t *testing.T) {
	_, cfg, gateDir, got := toolEnv(t, "go", quietGate)
	fullGate(t, gateDir)
	for _, argv := range [][]string{{"go", "test", "./..."}, {"pnpm", "install"}, {"ls"}} {
		*got = execCall{}
		code, errb := gateRun(t, append([]string{"--config", cfg, "--auto", "--real", "/bin/echo", "--"}, argv...)...)
		assert.Equal(t, 0, code, "%v: %s", argv, errb)
		assert.Equal(t, "/bin/echo", got.path, "%v", argv)
		assert.Equal(t, argv, got.argv, "%v", argv)
	}
}

// R3: a shim's call. The real binary is found behind the shim, as before; a heavy command is exec'd, not run under a slot; the gate directory
// is not made.
func TestGateDirect_AShimCallRunsTheRealBinaryAtOnce(t *testing.T) {
	real, cfg, gateDir, got := toolEnv(t, "go", quietGate)
	fullGate(t, gateDir)
	code, errb := gateRun(t, "--config", cfg, "--auto", "--tool", "go", "--", "test", "./...")
	assert.Equal(t, 0, code, errb)
	assert.Equal(t, real, got.path, "the real go, not the shim, exec'd")
	assert.Equal(t, []string{"go", "test", "./..."}, got.argv, "argv[0] stays the name as typed")
}

// R3: the rules of the settings file no longer make a command heavy: they classify nothing that is held.
func TestGateDirect_AnInterceptRuleHoldsNothing(t *testing.T) {
	real, cfg, gateDir, got := toolEnv(t, "go", quietGate)
	require.NoError(t, os.WriteFile(cfg, []byte("[gate]\n"+quietGate+"[[gate.intercept.rule]]\nmatch = \"go version\"\nclass = \"heavy\"\n"), 0o600))
	fullGate(t, gateDir)
	code, errb := gateRun(t, "--config", cfg, "--auto", "--tool", "go", "--", "version")
	assert.Equal(t, 0, code, errb)
	assert.Equal(t, real, got.path)
}

// R3: the fuse against a shim that comes back (#3671) is still there: the call that resolves to the shim again is refused, not looped.
// (gate_tool_recursion_test.go has the whole matrix; this one stands at the new place of the decision.)
func TestGateDirect_TheShimFuseIsStillThere(t *testing.T) {
	_, cfg, _, got := toolEnv(t, "go", quietGate)
	t.Setenv(gateToolDepthEnv, "go=8")
	code, errb := gateRun(t, "--config", cfg, "--auto", "--tool", "go", "--", "version")
	assert.Equal(t, 127, code)
	assert.Contains(t, errb, "shim recursion")
	assert.Empty(t, got.path)
}

// R4: the flags that used to steer the slot are still read and have no effect; a mistake in them is still a usage error.
func TestGateDirect_TheOldFlagsAreAcceptedAndIgnored(t *testing.T) {
	_, cfg, gateDir, got := toolEnv(t, "go", quietGate)
	fullGate(t, gateDir)
	code, errb := gateRun(t, "--config", cfg, "--wait-max", "0.01", "--class-source", "kind", "--fg", "--assist-host", "air26", "--assist-ssh", "/nonexistent/ssh",
		"--class", "heavy", "--real", "/bin/echo", "--", "echo", "x")
	assert.Equal(t, 0, code, "--wait-max no longer ends a wait with 75: %s", errb)
	assert.Equal(t, "/bin/echo", got.path)

	for _, bad := range [][]string{
		{"--wait-max", "-1", "--", "true"},
		{"--class-source", "bogus", "--", "true"},
		{"--class", "huge", "--", "true"},
		{"--bogus", "--", "true"},
		{},
	} {
		*got = execCall{}
		code, _ := gateRun(t, append([]string{"--config", cfg}, bad...)...)
		assert.Equal(t, 2, code, "%v", bad)
		assert.Empty(t, got.path, "%v", bad)
	}
}

// A command that cannot be started is still 127 (not found) or 126, as the shell says.
func TestGateDirect_ACommandThatCannotBeStartedIsStill127(t *testing.T) {
	_, cfg, _, _ := toolEnv(t, "go", quietGate)
	code, errb := gateRun(t, "--config", cfg, "--auto", "--", "definitely-not-a-command-xyz")
	assert.Equal(t, 127, code, errb)
}

// W1/W2/W3: would-wait never waits, with the gate full and a queue in front. The JSON keeps its shape (class, ahead = 0).
func TestGateDirect_WouldWaitIsAlwaysNo(t *testing.T) {
	_, cfg, gateDir, _ := toolEnv(t, "go", quietGate)
	fullGate(t, gateDir)

	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"would-wait", "--config", cfg}, &out, &errb))
	assert.Equal(t, "no\n", out.String(), "single, no command")

	for _, class := range []string{"heavy", "light"} {
		out.Reset()
		require.Equal(t, 0, gateMain([]string{"would-wait", "--class", class, "--json", "--config", cfg}, &out, &errb))
		var w gate.WouldWait
		require.NoError(t, json.Unmarshal(out.Bytes(), &w))
		assert.False(t, w.Wait, class)
		assert.Equal(t, gate.Class(class), w.Class)
		assert.Equal(t, 0, w.Ahead)
		assert.Empty(t, w.Reason)
		assert.Empty(t, w.Note)
	}

	out.Reset()
	require.Equal(t, 0, gateMain([]string{"would-wait", "--auto", "--json", "--config", cfg, "--", "go", "test", "./..."}, &out, &errb))
	var w gate.WouldWait
	require.NoError(t, json.Unmarshal(out.Bytes(), &w))
	assert.False(t, w.Wait)
	assert.Equal(t, gate.ClassHeavy, w.Class, "classified by its words, as before")
	assert.Equal(t, 0, w.Ahead)

	out.Reset()
	require.Equal(t, 0, gateMain([]string{"would-wait", "--auto", "--config", cfg, "--", "go", "test", "./..."}, &out, &errb))
	assert.Equal(t, "no\n", out.String())

	out.Reset()
	batch := `[["go","test","./..."],["ls"],["pnpm","install"]]`
	require.Equal(t, 0, gateMain([]string{"would-wait", "--auto-batch", batch, "--json", "--config", cfg}, &out, &errb))
	var ws []gate.WouldWait
	require.NoError(t, json.Unmarshal(out.Bytes(), &ws))
	require.Len(t, ws, 3, "one answer per command, in order")
	for i, w := range ws {
		assert.False(t, w.Wait, "%d", i)
	}
	assert.Equal(t, gate.ClassHeavy, ws[0].Class)
	assert.Equal(t, gate.ClassNone, ws[1].Class)

	out.Reset()
	require.Equal(t, 0, gateMain([]string{"would-wait", "--auto-batch", batch, "--config", cfg}, &out, &errb))
	assert.Equal(t, "no\nno\nno\n", out.String())
}

// The usage errors of would-wait are the same as before.
func TestGateDirect_WouldWaitStillChecksItsArguments(t *testing.T) {
	_, cfg, _, _ := toolEnv(t, "go", quietGate)
	var out, errb bytes.Buffer
	for _, bad := range [][]string{
		{"--class", "huge"},
		{"--auto"},
		{"--auto-batch", "[]"},
		{"--auto-batch", `[[]]`},
		{"--auto-batch", "not json"},
		{"--auto-batch", `[["ls"]]`, "--auto", "--", "ls"},
	} {
		out.Reset()
		errb.Reset()
		assert.Equal(t, 2, gateMain(append([]string{"would-wait", "--config", cfg}, bad...), &out, &errb), "%v", bad)
		assert.Contains(t, errb.String(), "usage: pdx gate would-wait", "%v", bad)
	}
}

// would-wait reads no gate state: it does not make the gate directory.
func TestGateDirect_WouldWaitMakesNoGateDirectory(t *testing.T) {
	_, cfg, gateDir, _ := toolEnv(t, "go", quietGate)
	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"would-wait", "--auto", "--json", "--config", cfg, "--", "go", "build"}, &out, &errb))
	assert.NoDirExists(t, gateDir)
	assert.NoDirExists(t, filepath.Join(filepath.Dir(cfg), "gate"))
}

// slotGateMain takes a slot the way `pdx gate [--class C] [--fg] [--config P] -- <cmd>` did before S0-a: through the gate's own queue. The CLI does
// not do that any more; the tests of the commands that look at the queue (status, queue, conflict) still need a holder and a waiter to look at,
// and make them with this. It goes with those commands (9a-3).
func slotGateMain(args []string, stdout, stderr io.Writer) int {
	flags, command, _ := splitRunArgs(args)
	fs := flag.NewFlagSet("slot", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	class := fs.String("class", "heavy", "")
	fg := fs.Bool("fg", false, "")
	cfgPath := fs.String("config", "", "")
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	cfg := loadGateConfig(*cfgPath, stderr)
	g := newGate(cfg)
	g.Stderr = stderr
	code, _ := g.Run(context.Background(), gate.Options{Argv: command, Class: gate.Class(*class), Foreground: *fg, Stdout: stdout, Stderr: stderr})
	return code
}

// The usage says what the gate does now: it does not promise a queue, a slot, a 75 or a priority.
func TestGateDirect_TheUsageDoesNotPromiseWhatIsGone(t *testing.T) {
	var out, errb bytes.Buffer
	require.Equal(t, 2, gateMain(nil, &out, &errb))
	u := errb.String()
	assert.Contains(t, u, "pass-through")
	assert.Contains(t, u, "run at once, by exec")
	assert.Contains(t, u, "no effect")
	for _, gone := range []string{"take a slot from", "past them nothing is run", "no background QoS", "served after every normal one", "wait for a slot right now", "queued at rank"} {
		assert.NotContains(t, u, gone)
	}
}
