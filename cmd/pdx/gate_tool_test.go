package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolEnv is a temp HOME with the shim directory first on PATH and a real `tool` after it; gateExec records the exec instead of
// replacing the test process. It returns the real binary's path, the config path, the gate directory and the recorded exec.
type execCall struct {
	path string
	argv []string
}

func toolEnv(t *testing.T, tool, gateSection string) (real, cfgPath, gateDir string, got *execCall) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	gateDir = filepath.Join(home, "gatedir")
	t.Setenv("PDX_GATE_DIR", gateDir)
	t.Setenv("PDX_GATE_HELD", "")
	for _, k := range []string{"CI", "AI_AGENT", "CLAUDECODE", "CLAUDE_CODE", "REPL_ID", "GEMINI_CLI", "CODEX_SANDBOX", "CODEX_THREAD_ID", "OPENCODE", "AUGMENT_AGENT", "GOOSE_PROVIDER", "CURSOR_AGENT"} {
		t.Setenv(k, "")
	}
	shimDir := shimDirIn(home)
	require.NoError(t, os.MkdirAll(shimDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(shimDir, tool), []byte(fakeShim(tool)), 0o755))
	realDir := filepath.Join(home, "real")
	require.NoError(t, os.MkdirAll(realDir, 0o755))
	real = filepath.Join(realDir, tool)
	require.NoError(t, os.WriteFile(real, []byte("#!/bin/sh\ntouch \""+home+"/ran-"+tool+"\"\nexit 7\n"), 0o755))
	t.Setenv("PATH", shimDir+":"+realDir+":/usr/bin:/bin")
	cfgPath = filepath.Join(home, "config.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("[gate]\n"+gateSection), 0o600))
	got = &execCall{}
	old := gateExec
	gateExec = func(path string, argv []string, _ []string) error {
		got.path, got.argv = path, argv
		return nil
	}
	t.Cleanup(func() { gateExec = old })
	return real, cfgPath, gateDir, got
}

const quietGate = noAdmission + "[gate.priority]\nmode = \"none\"\n"

// A command the gate does not take in is exec'd at once, by the real binary found behind the shim, before any gate state exists.
func TestGateTool_NoneIsExecedAtOnceAndTouchesNoGateState(t *testing.T) {
	real, cfg, gateDir, got := toolEnv(t, "go", quietGate)
	var out, errb bytes.Buffer
	code := gateMain([]string{"--config", cfg, "--auto", "--tool", "go", "--", "version"}, &out, &errb)
	assert.Equal(t, 0, code, errb.String())
	assert.Equal(t, real, got.path, "the real go, not the shim")
	assert.Equal(t, []string{"go", "version"}, got.argv, "argv[0] stays the name as typed")
	_, err := os.Stat(gateDir)
	assert.True(t, os.IsNotExist(err), "the hot path never makes the gate directory")
}

// A bare `go` (nothing after --) is a command too.
func TestGateTool_NoArgumentsIsStillACall(t *testing.T) {
	real, cfg, _, got := toolEnv(t, "go", quietGate)
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"--config", cfg, "--auto", "--tool", "go", "--"}, &out, &errb), errb.String())
	assert.Equal(t, real, got.path)
	assert.Equal(t, []string{"go"}, got.argv)
}

// A heavy command is exec'd too (S0-a): no slot, no gate directory.
func TestGateTool_HeavyIsExecedAtOnceToo(t *testing.T) {
	real, cfg, gateDir, got := toolEnv(t, "go", quietGate)
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"--config", cfg, "--auto", "--tool", "go", "--", "test", "./..."}, &out, &errb), errb.String())
	assert.Equal(t, real, got.path)
	_, err := os.Stat(gateDir)
	assert.True(t, os.IsNotExist(err), "no slot was taken: the gate directory is not made")
}

// (The intercept rules of the settings file hold nothing now: TestGateDirect_AnInterceptRuleHoldsNothing.)

// With the gate switched off nothing is taken in, whatever the command.
func TestGateTool_ADisabledGateExecsEverything(t *testing.T) {
	real, cfg, gateDir, got := toolEnv(t, "go", "enabled = false\n"+quietGate)
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"--config", cfg, "--auto", "--tool", "go", "--", "test", "./..."}, &out, &errb), errb.String())
	assert.Equal(t, real, got.path)
	_, err := os.Stat(gateDir)
	assert.True(t, os.IsNotExist(err))
}

// The real binary is looked up at every run from PATH: no go but the shim's is a plain failure, not a loop through the shim.
func TestGateTool_NoRealBinaryIs127(t *testing.T) {
	_, cfg, _, got := toolEnv(t, "go", quietGate)
	home := filepath.Dir(cfg)
	t.Setenv("PATH", shimDirIn(home)+":/usr/bin:/bin") // /usr/bin:/bin have no go in this test's reach
	if _, err := os.Stat("/usr/bin/go"); err == nil {
		t.Skip("a go in /usr/bin")
	}
	var out, errb bytes.Buffer
	assert.Equal(t, 127, gateMain([]string{"--config", cfg, "--auto", "--tool", "go", "--", "version"}, &out, &errb))
	assert.Contains(t, errb.String(), "no real binary")
	assert.Empty(t, got.path)
}

func TestGateTool_UsageErrors(t *testing.T) {
	_, cfg, _, _ := toolEnv(t, "go", quietGate)
	var out, errb bytes.Buffer
	for _, args := range [][]string{
		{"--config", cfg, "--tool", "a/b", "--", "x"}, // a name, not a path
		{"--config", cfg, "--tool", "go"},             // no -- at all
	} {
		errb.Reset()
		assert.Equal(t, 2, gateMain(args, &out, &errb), strings.Join(args, " "))
		assert.Contains(t, errb.String(), "usage: pdx gate")
	}
}

// vitest without a subcommand: at a terminal or not, it is exec'd (the terminal used to decide whether it was heavy).
func TestGateTool_ABareVitestIsExecedWithOrWithoutATerminal(t *testing.T) {
	real, cfg, _, got := toolEnv(t, "vitest", quietGate)
	var out, errb bytes.Buffer
	for _, tty := range []bool{true, false} {
		*got = execCall{}
		old := gateStdinIsTTY
		gateStdinIsTTY = func() bool { return tty }
		code := gateMain([]string{"--config", cfg, "--auto", "--tool", "vitest", "--"}, &out, &errb)
		gateStdinIsTTY = old
		assert.Equal(t, 0, code, errb.String())
		assert.Equal(t, real, got.path, "tty=%v", tty)
	}
}

// composer from a directory with a space in its name (Herd): found behind the shim, and exec'd at once whatever the subcommand.
func TestGateTool_ComposerInADirectoryWithASpace(t *testing.T) {
	_, cfg, gateDir, got := toolEnv(t, "composer", quietGate)
	home := filepath.Dir(cfg)
	herd := filepath.Join(home, "Library", "Application Support", "Herd", "bin")
	require.NoError(t, os.MkdirAll(herd, 0o755))
	herdComposer := filepath.Join(herd, "composer")
	require.NoError(t, os.WriteFile(herdComposer, []byte("#!/bin/sh\ntouch \""+home+"/ran-herd\"\nexit 3\n"), 0o755))
	t.Setenv("PATH", shimDirIn(home)+":"+herd+":/usr/bin:/bin")
	var out, errb bytes.Buffer

	assert.Equal(t, 0, gateMain([]string{"--config", cfg, "--auto", "--tool", "composer", "--", "install"}, &out, &errb), errb.String())
	assert.Equal(t, herdComposer, got.path)
	assert.Equal(t, []string{"composer", "install"}, got.argv)
	_, err := os.Stat(gateDir)
	assert.True(t, os.IsNotExist(err), "an install never touches the gate")

	*got = execCall{}
	code := gateMain([]string{"--config", cfg, "--auto", "--tool", "composer", "--", "test:integration"}, &out, &errb)
	assert.Equal(t, 0, code, errb.String())
	assert.Equal(t, herdComposer, got.path, "a test run is exec'd too: no slot")
	_, err = os.Stat(gateDir)
	assert.True(t, os.IsNotExist(err))
}
