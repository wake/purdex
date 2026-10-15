package gate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// The defaults of every module (user decision D7, 2026-10-15; the table of docs/specs/…-host-load-control-spec.md §5).
func TestDefaults_EveryModule(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "")
	d := Defaults()
	assert.True(t, d.Enabled)
	assert.Equal(t, "default", d.Profile)

	assert.Equal(t, []string{"go", "pnpm", "npx", "vitest", "tsc"}, d.Intercept.Tools)
	assert.True(t, d.Intercept.ModRewrite)
	assert.Empty(t, d.Intercept.Rules)

	assert.Equal(t, SlotsConfig{Heavy: 1, Light: 3, Order: "fifo"}, d.Slots)

	assert.True(t, d.Admission.Enabled)
	assert.Equal(t, 1, d.Admission.MaxPressureLevel)
	assert.True(t, d.Admission.PageRateAuto)
	assert.Equal(t, 50.0, d.Admission.PageRateLimit(), "auto: 50 pages/s over 10 s (G0)")
	assert.Equal(t, 10.0, d.Admission.DiskMinGB)
	assert.Equal(t, "level-only", d.Admission.OnUnreadable)

	assert.Equal(t, "auto", d.Parallelism.Mode)
	assert.Equal(t, 2, d.Parallelism.ReserveCores)
	assert.Equal(t, 400, d.Parallelism.MemPerWorkerMB)
	assert.Equal(t, 150, d.Parallelism.GoMemPerWorkerMB)
	assert.Equal(t, 700, d.Parallelism.VitestMemPerWorkerMB)
	assert.Equal(t, 3, d.Parallelism.VitestMaxWorkers)
	assert.True(t, d.Parallelism.GoP.Auto)
	assert.True(t, d.Parallelism.VitestWorkers.Auto)
	assert.True(t, d.Parallelism.PnpmWorkspace.Auto)

	assert.Equal(t, PriorityConfig{Mode: "nice", Nice: 10}, d.Priority)
	assert.Equal(t, LifetimeConfig{Hold: "tree", AlertAfter: 60 * time.Minute}, d.Lifetime)
	assert.Equal(t, ReapConfig{Enabled: true, Grace: 5 * time.Second}, d.Reap)
	assert.Equal(t, CacheConfig{Count1: "warn"}, d.Cache)
	assert.Empty(t, d.Warnings)
	assert.Equal(t, SrcDefault, d.Sources["slots.heavy"])
}

func TestLoad_NothingThereGivesTheDefaults(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "")
	d := Defaults()
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

func TestLoad_EachModuleReadsItsOwnTableAndSaysWhereValuesCameFrom(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "")
	cfg, err := Load(writeConfig(t, `
token = "secret"
[gate]
enabled = true
profile = "night"
dir = "/tmp/pdx-gate-test"
[gate.intercept]
tools = ["go", "make"]
mod_rewrite = false
[[gate.intercept.rule]]
match = "pnpm run e2e"
class = "heavy"
[gate.slots]
heavy = 2
light = 5
[gate.admission]
max_pressure_level = 2
max_page_rate = 250.5
disk_min_gb = 4
on_unreadable = "admit"
[gate.parallelism]
mode = "fixed"
reserve_cores = 4
mem_per_worker_mb = 800
go_p = 6
vitest_workers = "auto"
[gate.parallelism.light]
go_p = 2
[gate.priority]
mode = "background"
nice = 5
[gate.lifetime]
alert_after_min = 0
[gate.reap]
enabled = false
grace_s = 9
[gate.cache]
count1 = "strip"
`))
	require.NoError(t, err)
	assert.Empty(t, cfg.Warnings)
	assert.Equal(t, "night", cfg.Profile)
	assert.Equal(t, "/tmp/pdx-gate-test", cfg.Dir)
	assert.Equal(t, []string{"go", "make"}, cfg.Intercept.Tools)
	assert.False(t, cfg.Intercept.ModRewrite)
	assert.Equal(t, []Rule{{Match: "pnpm run e2e", Class: ClassHeavy}}, cfg.Intercept.Rules)
	assert.Equal(t, SlotsConfig{Heavy: 2, Light: 5, Order: "fifo"}, cfg.Slots)
	assert.Equal(t, 2, cfg.Admission.MaxPressureLevel)
	assert.False(t, cfg.Admission.PageRateAuto)
	assert.Equal(t, 250.5, cfg.Admission.PageRateLimit())
	assert.Equal(t, 4.0, cfg.Admission.DiskMinGB)
	assert.Equal(t, "admit", cfg.Admission.OnUnreadable)
	assert.Equal(t, "fixed", cfg.Parallelism.Mode)
	assert.Equal(t, 4, cfg.Parallelism.ReserveCores)
	assert.Equal(t, 800, cfg.Parallelism.MemPerWorkerMB)
	assert.Equal(t, Param{N: 6}, cfg.Parallelism.GoP)
	assert.True(t, cfg.Parallelism.VitestWorkers.Auto)
	require.NotNil(t, cfg.Parallelism.ByClass[ClassLight].GoP)
	assert.Equal(t, 2, cfg.Parallelism.ByClass[ClassLight].GoP.N)
	assert.Equal(t, PriorityConfig{Mode: "background", Nice: 5}, cfg.Priority)
	assert.Equal(t, time.Duration(0), cfg.Lifetime.AlertAfter)
	assert.Equal(t, ReapConfig{Enabled: false, Grace: 9 * time.Second}, cfg.Reap)
	assert.Equal(t, "strip", cfg.Cache.Count1)

	assert.Equal(t, SrcConfig, cfg.Sources["slots.heavy"])
	assert.Equal(t, SrcConfig, cfg.Sources["parallelism.go_p"])
	assert.Equal(t, SrcDefault, cfg.Sources["parallelism.pnpm_concurrency"], "left out: the default")
	assert.Equal(t, SrcConfig, cfg.Sources["admission.max_page_rate"])
}

// A value of the wrong kind, out of range or not allowed keeps the default and is listed; the gate never refuses a
// command because of its settings.
func TestLoad_ABadValueKeepsTheDefaultAndIsListed(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "")
	cfg, err := Load(writeConfig(t, `
[gate]
enabled = "yes"
[gate.slots]
heavy = 0
light = 1000
order = "lifo"
[gate.admission]
max_pressure_level = 9
max_page_rate = -5
disk_min_gb = "lots"
on_unreadable = "shrug"
[gate.parallelism]
mode = "wild"
go_p = "many"
[gate.priority]
mode = "nice"
nice = 99
[gate.cache]
count1 = "maybe"
[[gate.intercept.rule]]
match = "x"
class = "enormous"
`))
	require.NoError(t, err)
	d := Defaults()
	assert.Equal(t, d.Enabled, cfg.Enabled)
	assert.Equal(t, d.Slots, cfg.Slots)
	assert.Equal(t, d.Admission, cfg.Admission)
	assert.Equal(t, d.Parallelism.Mode, cfg.Parallelism.Mode)
	assert.Equal(t, d.Parallelism.GoP, cfg.Parallelism.GoP)
	assert.Equal(t, d.Priority, cfg.Priority)
	assert.Equal(t, d.Cache, cfg.Cache)
	assert.Empty(t, cfg.Intercept.Rules)
	assert.GreaterOrEqual(t, len(cfg.Warnings), 12, "each refused value is named")
	assert.Contains(t, cfg.Warnings[0]+cfg.Warnings[1]+cfg.Warnings[2]+cfg.Warnings[3], "slots.")
}

func TestLoad_ABrokenFileGivesTheDefaultsAndTheError(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "")
	cfg, err := Load(writeConfig(t, "[gate\nslots = "))
	assert.Error(t, err)
	assert.Equal(t, Defaults(), cfg, "the gate never refuses a command over its configuration")
}

func TestLoad_TheEnvironmentDirectoryWins(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "/tmp/from-env")
	cfg, err := Load(writeConfig(t, "[gate]\ndir = \"/tmp/from-file\"\n"))
	require.NoError(t, err)
	assert.Equal(t, "/tmp/from-env", cfg.Dir)
}
