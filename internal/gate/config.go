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
	"sort"

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
	// Values is the effective value of every key as text ("auto" for a number left to the gate); Profiles names the
	// profiles the file defines.
	Values   map[string]string
	Profiles []string
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
	// The active profile's table is laid over the settings before they are read; its keys are told apart afterwards.
	profile := newRaw(gate).Name("profile", "default")
	profKeys := map[string]bool{}
	profiles, _ := gate["profiles"].(map[string]any)
	if pt, ok := profiles[profile].(map[string]any); ok {
		gate = overlay(gate, pt, "", profKeys)
	}
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
	if v, present := gate["profiles"]; present && profiles == nil {
		r.warn("profiles", "want a table of profiles, got %v", v)
	}
	for k := range profKeys {
		if r.st.src[k] == SrcConfig {
			r.st.src[k] = SrcProfile
		}
	}
	for name := range profiles {
		c.Profiles = append(c.Profiles, name)
	}
	sort.Strings(c.Profiles)
	c.Sources, c.Warnings, c.Values = r.st.src, r.st.warn, r.st.val
	return c
}

// overlay is base with the keys of over laid on it (tables merge, anything else is replaced); keys collects the dotted
// path of every key that came from over. A profile cannot change which profile is active, nor the directory.
func overlay(base, over map[string]any, prefix string, keys map[string]bool) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if prefix == "" && (k == "profile" || k == "profiles" || k == "dir") {
			continue
		}
		ov, isTable := v.(map[string]any)
		bv, baseIsTable := out[k].(map[string]any)
		if isTable && (baseIsTable || out[k] == nil) {
			out[k] = overlay(bv, ov, prefix+k+".", keys)
			continue
		}
		out[k] = v
		keys[prefix+k] = true
	}
	return out
}

// Setting is one effective value with where it came from.
type Setting struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source Source `json:"source"`
}

// Effective lists every setting that was read, sorted by key.
func (c Config) Effective() []Setting {
	out := make([]Setting, 0, len(c.Values))
	for k, v := range c.Values {
		out = append(out, Setting{Key: k, Value: v, Source: c.Sources[k]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
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
