// Package gate is the host load gate (host-load-control spec §5): a heavy command runs under a slot that is a local
// file lock, so the gate works with the daemon down, slow or restarting. The kernel drops a lock when its holder
// exits, so a slot is held exactly as long as the gate process lives and there is no expiry to leak.
//
// The gate is seven modules, each with its own switch and parameters in a table of `[gate]` in config.toml (intercept,
// slots, admission, parallelism, priority, lifetime, reap, cache: one file each, mod_*.go). The file is read again
// every time a command starts and every time a waiting gate looks at the queue, so a change takes effect with the next
// command and needs no restart. A parameter may be "auto": worked out from the host when the command starts.
package gate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the effective setting of the gate at one moment.
type Config struct {
	Enabled bool
	Profile string
	// Dir holds the lock files, tickets and holder records.
	Dir string

	Intercept   InterceptConfig
	Slots       SlotsConfig
	Admission   AdmissionConfig
	Parallelism ParallelismConfig
	Priority    PriorityConfig
	Lifetime    LifetimeConfig
	Reap        ReapConfig
	Cache       CacheConfig

	// Sources says, for every key, whether it is the default, set in the file, or (parallelism) worked out; keys are
	// "slots.heavy", "admission.max_page_rate" and so on. Warnings are the lines about values that were refused.
	Sources  map[string]Source
	Warnings []string
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

// FromTable builds the config from the decoded `[gate]` table (nil is "nothing set": the defaults).
func FromTable(gate map[string]any) Config {
	r := newRaw(gate)
	c := Config{
		Enabled:     r.Bool("enabled", true),
		Profile:     r.Name("profile", "default"),
		Dir:         DefaultDir(),
		Intercept:   parseIntercept(r.sub("intercept")),
		Slots:       parseSlots(r.sub("slots")),
		Admission:   parseAdmission(r.sub("admission")),
		Parallelism: parseParallelism(r.sub("parallelism")),
		Priority:    parsePriority(r.sub("priority")),
		Lifetime:    parseLifetime(r.sub("lifetime")),
		Reap:        parseReap(r.sub("reap")),
		Cache:       parseCache(r.sub("cache")),
	}
	if d, _ := gate["dir"].(string); d != "" && os.Getenv("PDX_GATE_DIR") == "" {
		c.Dir = d
	}
	c.Sources, c.Warnings = r.st.src, r.st.warn
	return c
}

// Defaults is the config with nothing set.
func Defaults() Config { return FromTable(nil) }

// Load reads `[gate]` from the config file. A missing file or section gives the defaults; a file that cannot be parsed
// gives the defaults and the error (the caller prints it once: the gate never refuses to run a command because of its
// settings). A value that is of the wrong kind or out of range keeps its default and is listed in Warnings.
func Load(path string) (Config, error) {
	if path == "" {
		return Defaults(), nil
	}
	var top map[string]any
	if _, err := toml.DecodeFile(path, &top); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Defaults(), nil
		}
		return Defaults(), fmt.Errorf("gate config %s: %w", path, err)
	}
	g, isTable := top["gate"].(map[string]any)
	if v, present := top["gate"]; present && !isTable {
		cfg := Defaults()
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("gate: want a [gate] table, got %v (defaults kept)", v))
		return cfg, nil
	}
	return FromTable(g), nil
}
