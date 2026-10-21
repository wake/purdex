package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #3671: with HOME swapped for a fake directory the shim directory worked out from $HOME is not the one on PATH. The shim on
// PATH is still told by its header, so the real go behind it is found and one level runs.
func TestGateTool_FakeHomeStillFindsTheRealBinaryBehindTheShim(t *testing.T) {
	real, cfg, _, got := toolEnv(t, "go", quietGate)
	shimDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(shimDir, "go"), []byte(fakeShim("go")), 0o755))
	t.Setenv("PATH", shimDir+":"+filepath.Dir(real)+":/usr/bin:/bin")
	t.Setenv("HOME", t.TempDir()) // swapped after the shim directory was made
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"--config", cfg, "--auto", "--tool", "go", "--", "version"}, &out, &errb), errb.String())
	assert.Equal(t, real, got.path)
}

// Only the shim on PATH: a plain 127, nothing started.
func TestGateTool_OnlyAShimOnPathIs127UnderAFakeHome(t *testing.T) {
	_, cfg, _, got := toolEnv(t, "go", quietGate)
	shimDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(shimDir, "go"), []byte(fakeShim("go")), 0o755))
	t.Setenv("PATH", shimDir)
	t.Setenv("HOME", t.TempDir())
	var out, errb bytes.Buffer
	assert.Equal(t, 127, gateMain([]string{"--config", cfg, "--auto", "--tool", "go", "--", "version"}, &out, &errb))
	assert.Empty(t, got.path)
}

// The fuse: a chain of gates that keeps coming back (a depth already past the limit in the environment) stops at once, and an
// ordinary nested call passes with the depth of its tool raised by one for the child.
func TestGateTool_RecursionFuse(t *testing.T) {
	_, cfg, _, got := toolEnv(t, "go", quietGate)
	t.Setenv("PDX_GATE_TOOL_DEPTH", "go=8")
	var out, errb bytes.Buffer
	assert.Equal(t, 127, gateMain([]string{"--config", cfg, "--auto", "--tool", "go", "--", "version"}, &out, &errb))
	assert.Contains(t, errb.String(), "shim recursion: go resolved to the pdx shim again")
	assert.Empty(t, got.path)

	t.Setenv("PDX_GATE_TOOL_DEPTH", "go=2,pnpm=8")
	errb.Reset()
	var env []string
	old := gateExec
	t.Cleanup(func() { gateExec = old })
	gateExec = func(path string, argv []string, e []string) error { got.path, env = path, e; return nil }
	assert.Equal(t, 0, gateMain([]string{"--config", cfg, "--auto", "--tool", "go", "--", "version"}, &out, &errb), errb.String())
	assert.NotEmpty(t, got.path)
	assert.Contains(t, env, "PDX_GATE_TOOL_DEPTH=go=3,pnpm=8")
}
