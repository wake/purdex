// Package gate is the host load gate (host-load-control spec §5): a heavy command runs under a slot that is a local
// file lock, so the gate works with the daemon down, slow or restarting. The kernel drops a lock when its holder
// exits, so a slot is held exactly as long as the gate process lives and there is no expiry to leak.
package gate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the `[gate]` section of config.toml, read by the gate itself (the daemon is not asked).
type Config struct {
	Enabled bool
	// HeavySlots and LightSlots are the sizes of the two pools (spec §3).
	HeavySlots, LightSlots int
	// GoP is the `-p` put into GOFLAGS of a gated command that has none; VitestWorkers the `--maxWorkers` of a vitest
	// run without a limit; PnpmConcurrency the `--workspace-concurrency` of a recursive pnpm run without one.
	GoP, VitestWorkers, PnpmConcurrency int
	// MaxPageRate is the pageouts + swapouts per second above which nothing new is admitted.
	MaxPageRate float64
	// HeavyBackground and LightBackground run that class with background QoS (`taskpolicy -b`, E cores only).
	HeavyBackground, LightBackground bool
	// DiskMinGB is the free space a heavy command needs on the disk of its directory and of $TMPDIR; below it the
	// gate does not run it (0 turns the check off).
	DiskMinGB float64
	// Dir holds the lock files, tickets and holder records.
	Dir string
}

// Defaults suit a 10-core host (spec §5); the measured ones (QoS, page rate) are the G0 numbers.
func Defaults() Config {
	return Config{
		Enabled: true, HeavySlots: 2, LightSlots: 4, GoP: 3, VitestWorkers: 3, PnpmConcurrency: 2,
		MaxPageRate: 100, HeavyBackground: true, LightBackground: false, DiskMinGB: 3, Dir: DefaultDir(),
	}
}

// DefaultDir is ~/.config/pdx/gate (PDX_GATE_DIR overrides it: tests and a host that keeps its state elsewhere).
func DefaultDir() string {
	if d := os.Getenv("PDX_GATE_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "pdx-gate")
	}
	return filepath.Join(home, ".config", "pdx", "gate")
}

// DefaultConfigPath is ~/.config/pdx/config.toml.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "pdx", "config.toml")
}

type fileGate struct {
	Enabled         *bool    `toml:"enabled"`
	HeavySlots      *int     `toml:"heavy_slots"`
	LightSlots      *int     `toml:"light_slots"`
	GoP             *int     `toml:"go_p"`
	VitestWorkers   *int     `toml:"vitest_workers"`
	PnpmConcurrency *int     `toml:"pnpm_concurrency"`
	MaxPageRate     *float64 `toml:"max_page_rate"`
	HeavyBackground *bool    `toml:"heavy_background"`
	LightBackground *bool    `toml:"light_background"`
	DiskMinGB       *float64 `toml:"disk_min_gb"`
	Dir             *string  `toml:"dir"`
}

// Load reads `[gate]` from the config file over the defaults. A missing file or section gives the defaults; a file
// that cannot be parsed gives the defaults and the error (the caller prints it once: the gate never refuses to run
// a command because of its configuration). A number out of range is replaced by the default.
func Load(path string) (Config, error) {
	cfg := Defaults()
	if path == "" {
		return cfg, nil
	}
	var f struct {
		Gate fileGate `toml:"gate"`
	}
	if _, err := toml.DecodeFile(path, &f); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("gate config %s: %w", path, err)
	}
	g := f.Gate
	setBool(&cfg.Enabled, g.Enabled)
	setInt(&cfg.HeavySlots, g.HeavySlots, 1, 64)
	setInt(&cfg.LightSlots, g.LightSlots, 1, 64)
	setInt(&cfg.GoP, g.GoP, 1, 64)
	setInt(&cfg.VitestWorkers, g.VitestWorkers, 1, 64)
	setInt(&cfg.PnpmConcurrency, g.PnpmConcurrency, 1, 64)
	if g.MaxPageRate != nil && *g.MaxPageRate > 0 {
		cfg.MaxPageRate = *g.MaxPageRate
	}
	if g.DiskMinGB != nil && *g.DiskMinGB >= 0 {
		cfg.DiskMinGB = *g.DiskMinGB
	}
	setBool(&cfg.HeavyBackground, g.HeavyBackground)
	setBool(&cfg.LightBackground, g.LightBackground)
	if g.Dir != nil && *g.Dir != "" && os.Getenv("PDX_GATE_DIR") == "" {
		cfg.Dir = *g.Dir
	}
	return cfg, nil
}

func setBool(dst *bool, v *bool) {
	if v != nil {
		*dst = *v
	}
}

func setInt(dst *int, v *int, lo, hi int) {
	if v != nil && *v >= lo && *v <= hi {
		*dst = *v
	}
}
