package gate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestLoad_DefaultsWhenThereIsNothing(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "")
	d := Defaults()
	assert.True(t, d.Enabled)
	assert.Equal(t, 2, d.HeavySlots)
	assert.Equal(t, 4, d.LightSlots)
	assert.Equal(t, 3, d.GoP)
	assert.Equal(t, 3, d.VitestWorkers)
	assert.Equal(t, 3.0, d.DiskMinGB)

	cfg, err := Load(filepath.Join(t.TempDir(), "missing.toml"))
	require.NoError(t, err)
	assert.Equal(t, d, cfg)
	cfg, err = Load("")
	require.NoError(t, err)
	assert.Equal(t, d, cfg)
	cfg, err = Load(writeConfig(t, "[other]\nx = 1\n"))
	require.NoError(t, err)
	assert.Equal(t, d, cfg, "a file without [gate]")
}

func TestLoad_TheGateSectionOverridesTheDefaults(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "")
	cfg, err := Load(writeConfig(t, `
token = "secret"
[gate]
enabled = false
heavy_slots = 3
light_slots = 6
go_p = 2
vitest_workers = 4
pnpm_concurrency = 1
max_page_rate = 250.5
heavy_background = false
light_background = true
disk_min_gb = 8
dir = "/tmp/pdx-gate-test"
`))
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)
	assert.Equal(t, 3, cfg.HeavySlots)
	assert.Equal(t, 6, cfg.LightSlots)
	assert.Equal(t, 2, cfg.GoP)
	assert.Equal(t, 4, cfg.VitestWorkers)
	assert.Equal(t, 1, cfg.PnpmConcurrency)
	assert.Equal(t, 250.5, cfg.MaxPageRate)
	assert.False(t, cfg.HeavyBackground)
	assert.True(t, cfg.LightBackground)
	assert.Equal(t, 8.0, cfg.DiskMinGB)
	assert.Equal(t, "/tmp/pdx-gate-test", cfg.Dir)
}

func TestLoad_ANumberOutOfRangeKeepsTheDefault(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "")
	cfg, err := Load(writeConfig(t, "[gate]\nheavy_slots = 0\nlight_slots = 1000\ngo_p = -1\nmax_page_rate = -5\ndisk_min_gb = -1\n"))
	require.NoError(t, err)
	d := Defaults()
	assert.Equal(t, d.HeavySlots, cfg.HeavySlots)
	assert.Equal(t, d.LightSlots, cfg.LightSlots)
	assert.Equal(t, d.GoP, cfg.GoP)
	assert.Equal(t, d.MaxPageRate, cfg.MaxPageRate)
	assert.Equal(t, d.DiskMinGB, cfg.DiskMinGB)
}

func TestLoad_ABrokenFileGivesTheDefaultsAndTheError(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "")
	cfg, err := Load(writeConfig(t, "[gate\nheavy_slots = "))
	assert.Error(t, err)
	assert.Equal(t, Defaults(), cfg, "the gate never refuses a command over its configuration")
}

func TestLoad_TheEnvironmentDirectoryWins(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "/tmp/from-env")
	cfg, err := Load(writeConfig(t, "[gate]\ndir = \"/tmp/from-file\"\n"))
	require.NoError(t, err)
	assert.Equal(t, "/tmp/from-env", cfg.Dir)
}
