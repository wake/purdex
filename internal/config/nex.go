package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lab.protype.tw/wake/nexen/sandbox"
)

// NexConfig holds the [nex] section: whether the embedded Nexen module is
// enabled, and the pdx-side settings it needs (repo/service containment
// roots, binaries, PATH additions, sandbox profile ceiling/default, and
// timeouts). See spec §4.2 layer 1 for the validation this feeds.
type NexConfig struct {
	Enabled      bool              `toml:"enabled"       json:"enabled"`
	RepoRoots    []string          `toml:"repo_roots"    json:"repo_roots"`
	ServiceRoots []string          `toml:"service_roots" json:"service_roots"`
	ClaudeBin    string            `toml:"claude_bin"    json:"claude_bin"`
	CswapBin     string            `toml:"cswap_bin"     json:"cswap_bin"`
	PathPrepend  []string          `toml:"path_prepend"  json:"path_prepend"`
	Sandbox      NexSandboxConfig  `toml:"sandbox"       json:"sandbox"`
	Timeouts     NexTimeoutsConfig `toml:"timeouts"      json:"timeouts"`
}

// NexSandboxConfig names the sandbox profile ceiling (MaxProfile) and the
// profile used when a caller does not request one (DefaultProfile). Both
// are names from nexen's built-in profile table (see sandbox.ValidName);
// empty means "no preference" and is left to Nexen's own fail-closed
// default rather than validated here.
type NexSandboxConfig struct {
	MaxProfile     string `toml:"max_profile"     json:"max_profile"`
	DefaultProfile string `toml:"default_profile" json:"default_profile"`
}

// NexTimeoutsConfig holds Nexen turn/lease timeouts as duration strings
// (e.g. "5m"). Empty means "use Nexen's own default"; a non-empty value
// must parse with time.ParseDuration.
type NexTimeoutsConfig struct {
	LeaseTTL  string `toml:"lease_ttl" json:"lease_ttl"`
	Interrupt string `toml:"interrupt" json:"interrupt"`
	Turn      string `toml:"turn"      json:"turn"`
}

// DefaultNexConfig returns the zero-config-friendly nex defaults: disabled,
// a conservative PATH prepend list, and the "trusted" sandbox profile for
// both the ceiling and the default. Timeouts are left empty (Nexen's own
// defaults apply).
func DefaultNexConfig() NexConfig {
	return NexConfig{
		Enabled:     false,
		PathPrepend: []string{"~/.local/bin", "/opt/homebrew/bin", "/usr/local/bin"},
		Sandbox: NexSandboxConfig{
			MaxProfile:     "trusted",
			DefaultProfile: "trusted",
		},
	}
}

// Validate checks the pdx-side layer of nex config (spec §4.2 layer 1).
// Every error names the offending key. It does not touch the filesystem —
// paths are checked for shape (absolute after "~" expansion), not
// existence.
//
// home == "" means the caller could not resolve the user's home directory
// (os.UserHomeDir failed — a launchd/Finder-started process without HOME).
// A "~" entry then cannot be expanded: when the section is not Enabled the
// shape check for that entry is skipped (the default path_prepend contains
// "~/.local/bin", and a host that never opted into nex must not fail every
// pdx subcommand over it); when Enabled it is an error naming HOME rather
// than a misleading "must be an absolute path". Entries that are relative
// without a leading "~" are rejected either way.
func (n *NexConfig) Validate(home string) error {
	if n.Enabled && len(n.RepoRoots)+len(n.ServiceRoots) == 0 {
		return fmt.Errorf("nex.repo_roots / nex.service_roots: at least one root required when nex.enabled")
	}

	if n.Sandbox.MaxProfile != "" && !sandbox.ValidName(n.Sandbox.MaxProfile) {
		return fmt.Errorf("nex.sandbox.max_profile: unknown profile %q", n.Sandbox.MaxProfile)
	}
	if n.Sandbox.DefaultProfile != "" && !sandbox.ValidName(n.Sandbox.DefaultProfile) {
		return fmt.Errorf("nex.sandbox.default_profile: unknown profile %q", n.Sandbox.DefaultProfile)
	}

	if n.ClaudeBin != "" {
		if err := n.checkAbs("nex.claude_bin", n.ClaudeBin, home); err != nil {
			return err
		}
	}
	if n.CswapBin != "" {
		if err := n.checkAbs("nex.cswap_bin", n.CswapBin, home); err != nil {
			return err
		}
	}
	for i, p := range n.PathPrepend {
		if err := n.checkAbs(fmt.Sprintf("nex.path_prepend[%d]", i), p, home); err != nil {
			return err
		}
	}
	for i, r := range n.RepoRoots {
		if err := n.checkAbs(fmt.Sprintf("nex.repo_roots[%d]", i), r, home); err != nil {
			return err
		}
	}
	for i, r := range n.ServiceRoots {
		if err := n.checkAbs(fmt.Sprintf("nex.service_roots[%d]", i), r, home); err != nil {
			return err
		}
	}

	if n.Timeouts.LeaseTTL != "" {
		if _, err := time.ParseDuration(n.Timeouts.LeaseTTL); err != nil {
			return fmt.Errorf("nex.timeouts.lease_ttl: %w", err)
		}
	}
	if n.Timeouts.Interrupt != "" {
		if _, err := time.ParseDuration(n.Timeouts.Interrupt); err != nil {
			return fmt.Errorf("nex.timeouts.interrupt: %w", err)
		}
	}
	if n.Timeouts.Turn != "" {
		if _, err := time.ParseDuration(n.Timeouts.Turn); err != nil {
			return fmt.Errorf("nex.timeouts.turn: %w", err)
		}
	}

	return nil
}

// checkAbs is Validate's per-entry shape check: p must be absolute after
// "~" expansion against home. See Validate for the home == "" rule.
func (n *NexConfig) checkAbs(key, p, home string) error {
	if home == "" && hasTilde(p) {
		if !n.Enabled {
			return nil
		}
		return fmt.Errorf("%s: HOME is not set, cannot expand %q", key, p)
	}
	if !filepath.IsAbs(expandTildeHome(p, home)) {
		return fmt.Errorf("%s: must be an absolute path (got %q)", key, p)
	}
	return nil
}

// hasTilde reports whether p is "~" or begins with "~/" — the two shapes
// expandTildeHome expands.
func hasTilde(p string) bool {
	return p == "~" || strings.HasPrefix(p, "~/")
}

// Expanded returns a copy of n with "~" expanded to home and every path
// (RepoRoots, ServiceRoots, PathPrepend, ClaudeBin, CswapBin) cleaned via
// filepath.Clean. The receiver's slices are never mutated — every slice
// field is copied into a fresh backing array.
func (n NexConfig) Expanded(home string) NexConfig {
	out := n
	out.RepoRoots = expandTildeAll(n.RepoRoots, home)
	out.ServiceRoots = expandTildeAll(n.ServiceRoots, home)
	out.PathPrepend = expandTildeAll(n.PathPrepend, home)
	out.ClaudeBin = expandTildeClean(n.ClaudeBin, home)
	out.CswapBin = expandTildeClean(n.CswapBin, home)
	return out
}

// expandTildeHome expands a leading "~" or "~/..." in p to home, without
// cleaning the result. It is the shape check Validate uses (existence is
// never checked).
func expandTildeHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// expandTildeClean expands "~" via expandTildeHome and cleans the result.
// An empty string is returned unchanged.
func expandTildeClean(p, home string) string {
	if p == "" {
		return p
	}
	return filepath.Clean(expandTildeHome(p, home))
}

// expandTildeAll applies expandTildeClean to every element of list into a
// freshly allocated slice, preserving nil for a nil input.
func expandTildeAll(list []string, home string) []string {
	if list == nil {
		return nil
	}
	out := make([]string, len(list))
	for i, p := range list {
		out[i] = expandTildeClean(p, home)
	}
	return out
}
