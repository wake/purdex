package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"
)

// gateEnv points the gate at a temp directory with a small config, and keeps the host's claim out.
// noAdmission is the [gate.admission] of the tests that hold or queue heavy commands and are not of admission. With admission on the
// gate holds even a first heavy command while the host is under memory pressure or paging, so such a test fails on a busy host
// (#3241); the gate package tests admission with a pressure reader of its own. (A bare [admission] after "[gate]" is a table of its
// own, not gate.admission: it sets nothing.)
const noAdmission = "[gate.admission]\nenabled = false\ndisk_min_gb = 0\n"

func gateEnv(t *testing.T, gateSection string) (cfgPath, dir string) {
	t.Helper()
	dir = t.TempDir()
	t.Setenv("PDX_GATE_DIR", filepath.Join(dir, "gate"))
	t.Setenv("PDX_GATE_HELD", "")
	cfgPath = filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("[gate]\n"+gateSection), 0o600))
	return cfgPath, dir
}

// A disabled gate lets everything through, so a class at 0 is not shown as paused (R2: status and Run would otherwise disagree).
// Mutation gate: the Enabled check dropped → paused is shown (red).
func TestGate_StatusDoesNotShowPausedWhenTheGateIsDisabled(t *testing.T) {
	cfg, _ := gateEnv(t, "enabled = false\n[gate.slots]\nheavy = 0\n[gate.admission]\ndisk_min_gb = 0\n")
	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"status", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), "停用")
	assert.NotContains(t, out.String(), "已暫停")
	out.Reset()
	require.Equal(t, 0, gateMain([]string{"status", "--json", "--config", cfg}, &out, &errb))
	var st map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &st))
	assert.Equal(t, false, st["PausedHeavy"])
}

// A class at slots=0 is shown as paused in the text and in the JSON (W3c); slots.<class> keeps its 0 in the settings (the fleet
// reads that one).
func TestGate_StatusShowsAPausedClass(t *testing.T) {
	cfg, _ := gateEnv(t, "[gate.slots]\nheavy = 0\n[gate.admission]\ndisk_min_gb = 0\n")
	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"status", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), "重 0/0 使用中")
	assert.Contains(t, out.String(), "已暫停（名額 0）")
	assert.Equal(t, 1, strings.Count(out.String(), "已暫停"), "only the heavy class")
	assert.Regexp(t, `slots\.heavy\s+0\s+config`, out.String())
	out.Reset()
	require.Equal(t, 0, gateMain([]string{"status", "--json", "--config", cfg}, &out, &errb))
	var st map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &st))
	assert.Equal(t, true, st["PausedHeavy"])
	assert.Equal(t, false, st["PausedLight"])

	cfg, _ = gateEnv(t, "[gate.slots]\nlight = 0\n[gate.admission]\ndisk_min_gb = 0\n")
	out.Reset()
	require.Equal(t, 0, gateMain([]string{"status", "--json", "--config", cfg}, &out, &errb))
	st = nil
	require.NoError(t, json.Unmarshal(out.Bytes(), &st))
	assert.Equal(t, false, st["PausedHeavy"])
	assert.Equal(t, true, st["PausedLight"])
}

// A slot that is locked while its record is gone (a child that inherited the lock outlived the gate) is listed and says so, not as a
// holder with no pid (#3049). The JSON carries the flag.
func TestGate_StatusShowsASlotWithNoRecord(t *testing.T) {
	cfg, dir := gateEnv(t, "[gate.admission]\ndisk_min_gb = 0\n")
	pool := &gate.Pool{Dir: filepath.Join(dir, "gate"), Class: gate.ClassLight, Size: 3, Now: time.Now}
	tk, err := pool.Enqueue()
	require.NoError(t, err)
	s, _, err := pool.TryTake(tk)
	require.NoError(t, err)
	require.NotNil(t, s)
	tk.Drop()
	defer s.Release(nil)
	require.NoError(t, os.Remove(filepath.Join(dir, "gate", "light-1.json")))

	var out, errb bytes.Buffer
	require.Equal(t, 0, gateMain([]string{"status", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), "輕 1/3 使用中")
	assert.Contains(t, out.String(), "沒有紀錄")
	out.Reset()
	require.Equal(t, 0, gateMain([]string{"status", "--json", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), `"no_record": true`)
}

// (The tests that ran a command under a slot are gate_direct_test.go now: S0-a.)

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
	cfg, _ := gateEnv(t, "[admission]\ndisk_min_gb = 0\n")
	var out, errb bytes.Buffer
	assert.Equal(t, 0, gateMain([]string{"status", "--config", cfg}, &out, &errb))
	assert.Contains(t, out.String(), "重 0/1 使用中，0 個在排隊")
	assert.Contains(t, out.String(), "輕 0/3 使用中，0 個在排隊")
	out.Reset()
	assert.Equal(t, 0, gateMain([]string{"status", "--json", "--config", cfg}, &out, &errb))
	assert.True(t, strings.HasPrefix(strings.TrimSpace(out.String()), "{"))
	out.Reset()
	assert.Equal(t, 0, gateMain([]string{"status", "--json", "--holders", "--config", cfg}, &out, &errb), "--holders is accepted with --json")
	assert.Equal(t, 0, gateMain([]string{"status", "--holders", "--config", cfg}, &out, &errb), "and harmless without it")
}

// A settings file that does not read is reported and the defaults answer; would-wait is the one that still reads it (a run does not).
func TestGate_ABrokenConfigIsReportedAndTheDefaultsAnswer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PDX_GATE_DIR", filepath.Join(dir, "gate"))
	t.Setenv("PDX_GATE_HELD", "")
	bad := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(bad, []byte("[gate\n"), 0o600))
	var out, errb bytes.Buffer
	code := gateMain([]string{"would-wait", "--config", bad, "--auto", "--", "go", "test"}, &out, &errb)
	assert.Equal(t, 0, code)
	assert.Equal(t, "no\n", out.String())
	assert.Contains(t, errb.String(), "using the defaults")
}
