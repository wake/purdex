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

// gateEnv points the gate at a temp directory with a small config, and keeps the host's claim out.
func gateEnv(t *testing.T, gateSection string) (cfgPath, dir string) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("PDX_GATE_DIR", filepath.Join(dir, "gate"))
	t.Setenv("PDX_GATE_HELD", "")
	cfgPath = filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("[gate]\n"+gateSection), 0o600))
	return cfgPath, dir
}

func TestGate_RunsTheCommandAndReturnsItsExitCode(t *testing.T) {
	cfg, dir := gateEnv(t, "disk_min_gb = 0\nheavy_background = false\n")
	var out, errb bytes.Buffer
	code := gateMain([]string{"--config", cfg, "--", "sh", "-c", "touch " + dir + "/ran; exit 5"}, &out, &errb)
	assert.Equal(t, 5, code)
	_, err := os.Stat(dir + "/ran")
	assert.NoError(t, err)
}

func TestGate_AutoPassesWhatIsNotHeavy(t *testing.T) {
	cfg, dir := gateEnv(t, "disk_min_gb = 0\n")
	var out, errb bytes.Buffer
	code := gateMain([]string{"--config", cfg, "--auto", "--", "sh", "-c", "touch " + dir + "/ran"}, &out, &errb)
	assert.Equal(t, 0, code)
	_, err := os.Stat(dir + "/ran")
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "gate"))
	assert.True(t, os.IsNotExist(err), "a command that is not heavy never touches the gate directory")
}

func TestGate_RealRunsInPlaceOfTheFirstWord(t *testing.T) {
	cfg, _ := gateEnv(t, "disk_min_gb = 0\nheavy_background = false\n")
	var out, errb bytes.Buffer
	code := gateMain([]string{"--config", cfg, "--real", "/bin/echo", "--", "not-a-binary", "from-real"}, &out, &errb)
	assert.Equal(t, 0, code)
	assert.Equal(t, "from-real\n", out.String()+"")
}

func TestGate_Usage(t *testing.T) {
	var out, errb bytes.Buffer
	assert.Equal(t, 2, gateMain(nil, &out, &errb))
	assert.Contains(t, errb.String(), "usage: pdx gate")
	errb.Reset()
	assert.Equal(t, 2, gateMain([]string{"--class", "huge", "--", "true"}, &out, &errb))
	assert.Contains(t, errb.String(), "--class must be heavy or light")
	errb.Reset()
	assert.Equal(t, 2, gateMain([]string{"--bogus", "--", "true"}, &out, &errb))
}

func TestGate_StatusReadsTheDirectoryWithoutTheDaemon(t *testing.T) {
	cfg, _ := gateEnv(t, "disk_min_gb = 0\n")
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"status", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), "重 0/2 使用中，0 個在排隊")
	assert.Contains(t, out.String(), "輕 0/4 使用中，0 個在排隊")
	out.Reset()
	assert.Equal(t, 0, gateMain([]string{"status", "--json", "--config", cfg}, &out, &errb))
	assert.True(t, strings.HasPrefix(strings.TrimSpace(out.String()), "{"))
}

func TestGate_ABrokenConfigIsReportedAndTheDefaultsRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PDX_GATE_DIR", filepath.Join(dir, "gate"))
	t.Setenv("PDX_GATE_HELD", "")
	bad := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(bad, []byte("[gate\n"), 0o600))
	var out, errb bytes.Buffer
	code := gateMain([]string{"--config", bad, "--auto", "--", "true"}, &out, &errb)
	assert.Equal(t, 0, code)
	assert.Contains(t, errb.String(), "using the defaults")
}
