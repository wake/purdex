package config

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

type Preset struct {
	Name    string `toml:"name"    json:"name"`
	Command string `toml:"command" json:"command"`
}

type StreamConfig struct {
	Presets []Preset `toml:"presets" json:"presets"`
}

type DetectConfig struct {
	CCCommands   []string `toml:"cc_commands"   json:"cc_commands"`
	PollInterval int      `toml:"poll_interval" json:"poll_interval"`
}

type FeaturesConfig struct {
	FS  bool `toml:"fs"`
	Git bool `toml:"git"`
}

type DevConfig struct {
	Update   bool   `toml:"update"    json:"update"`
	RepoRoot string `toml:"repo_root" json:"repo_root"`
}

type TerminalConfig struct {
	SizingMode string `toml:"sizing_mode" json:"sizing_mode"`
}

type MonitorConfig struct {
	RefreshIntervalMS int `toml:"refresh_interval_ms" json:"refresh_interval_ms"`
	TopProcessLimit   int `toml:"top_process_limit"   json:"top_process_limit"`
}

// PeerHost is one entry in the peer host list: a remote Purdex daemon this
// host can bridge to (outbound) and/or accept bridged requests from
// (inbound).
type PeerHost struct {
	Alias        string `toml:"alias"         json:"alias"`
	URL          string `toml:"url"           json:"url"`
	HostID       string `toml:"host_id"       json:"host_id"`       // "" until verified
	Token        string `toml:"token"         json:"token"`         // outbound: what we present to that host
	InboundToken string `toml:"inbound_token" json:"inbound_token"` // what that host must present to us
	AllowBypass  bool   `toml:"allow_bypass"  json:"allow_bypass"`
}

type PeersConfig struct {
	Alias string     `toml:"alias" json:"alias"`
	Hosts []PeerHost `toml:"hosts" json:"hosts"`
}

// aliasPattern matches one URL path segment safe to use as a peer alias:
// starts with an alphanumeric, followed by up to 63 more alphanumerics,
// dots, underscores or hyphens.
var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidateAlias checks that alias is safe to use as a one-segment URL path
// component and distinct from localAlias (case-insensitive).
func ValidateAlias(alias, localAlias string) error {
	if alias == "." || alias == ".." {
		return fmt.Errorf("alias %q is reserved", alias)
	}
	if !aliasPattern.MatchString(alias) {
		return fmt.Errorf("alias %q must match %s", alias, aliasPattern.String())
	}
	if strings.EqualFold(alias, localAlias) {
		return fmt.Errorf("alias %q collides with the local alias", alias)
	}
	return nil
}

// NewPeerToken returns a fresh bearer token: "pdxp_" followed by 32 lowercase
// hex characters derived from 16 bytes of crypto/rand.
func NewPeerToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate peer token: %w", err)
	}
	return "pdxp_" + hex.EncodeToString(buf), nil
}

// FindPeerHostByAlias returns the index of the host whose Alias
// case-insensitively matches alias, or -1 if none match.
func (p PeersConfig) FindPeerHostByAlias(alias string) int {
	for i, h := range p.Hosts {
		if strings.EqualFold(h.Alias, alias) {
			return i
		}
	}
	return -1
}

// MatchInboundToken compares bearer against every host's non-empty
// InboundToken in constant time, returning a copy of the matching host and
// true. An empty bearer never matches.
func (p PeersConfig) MatchInboundToken(bearer string) (PeerHost, bool) {
	if bearer == "" {
		return PeerHost{}, false
	}
	matchIdx := -1
	bearerBytes := []byte(bearer)
	for i, h := range p.Hosts {
		if h.InboundToken == "" {
			continue
		}
		if subtle.ConstantTimeCompare(bearerBytes, []byte(h.InboundToken)) == 1 {
			matchIdx = i
		}
	}
	if matchIdx == -1 {
		return PeerHost{}, false
	}
	return p.Hosts[matchIdx], true
}

// DispatchConfig holds the Ploom-dispatch (M0) daemon-side settings.
//
// SandboxHostPolicy is the daemon's authoritative sandbox policy: every
// dispatch's requested profile is clamped down to it (spec §8.1). Empty →
// least-privilege default (ask).
//
// AllowedRepoRoots is the repo containment boundary (spec §7.1): a dispatch's
// repo_location.local_dir must resolve to a path inside one of these roots. Both
// the roots and the requested path are symlink-resolved before comparison, and
// the list is FAIL CLOSED — unset/empty admits no dispatch at all, since the path
// is supplied by Ploom and the sandbox clamp bounds what an execution may do, not
// which repos it may touch.
type DispatchConfig struct {
	SandboxHostPolicy string   `toml:"sandbox_host_policy" json:"sandbox_host_policy"`
	AllowedRepoRoots  []string `toml:"allowed_repo_roots"  json:"allowed_repo_roots"`
}

// GetSizingMode returns the sizing mode, defaulting to "auto".
func (tc TerminalConfig) GetSizingMode() string {
	if tc.SizingMode == "" {
		return "auto"
	}
	return tc.SizingMode
}

// PeerAlias returns Peers.Alias, or HostID up to the first ':' when unset.
func (c Config) PeerAlias() string {
	if c.Peers.Alias != "" {
		return c.Peers.Alias
	}
	// Fall back to HostID up to the first ':'
	if i := strings.IndexByte(c.HostID, ':'); i > 0 {
		return c.HostID[:i]
	}
	return c.HostID
}

// Redacted returns a deep copy of c with Token, HostID, and every
// Peers.Hosts[i].Token / .InboundToken blanked. c itself is never mutated.
func (c Config) Redacted() Config {
	out := c.Clone()
	out.Token = ""
	out.HostID = ""
	for i := range out.Peers.Hosts {
		out.Peers.Hosts[i].Token = ""
		out.Peers.Hosts[i].InboundToken = ""
	}
	return out
}

// Clone returns a deep copy of c: every slice field is copied into a fresh
// backing array so mutating the copy never touches c's. slices.Clone
// preserves nil vs non-nil-empty (unlike append(nil, src...), which always
// collapses a non-nil empty slice to nil) — a config that explicitly has an
// empty list (e.g. detect.cc_commands = []) must stay that way across a
// Clone, or it would silently re-encode without the key and re-apply
// defaults on the next Load.
func (c Config) Clone() Config {
	out := c

	out.Allow = slices.Clone(c.Allow)
	out.AllowedPaths = slices.Clone(c.AllowedPaths)

	out.Stream.Presets = slices.Clone(c.Stream.Presets)

	out.Detect.CCCommands = slices.Clone(c.Detect.CCCommands)

	out.Dispatch.AllowedRepoRoots = slices.Clone(c.Dispatch.AllowedRepoRoots)

	out.Peers.Hosts = slices.Clone(c.Peers.Hosts)

	return out
}

type Config struct {
	HostID       string         `toml:"host_id"        json:"host_id"`
	Bind         string         `toml:"bind"           json:"bind"`
	Port         int            `toml:"port"           json:"port"`
	Token        string         `toml:"token"          json:"token"`
	Allow        []string       `toml:"allow"          json:"allow"`
	DataDir      string         `toml:"data_dir"       json:"data_dir"`
	AllowedPaths []string       `toml:"allowed_paths"  json:"allowed_paths"`
	UploadDir    string         `toml:"upload_dir"     json:"upload_dir"`
	Terminal     TerminalConfig `toml:"terminal"       json:"terminal"`
	Stream       StreamConfig   `toml:"stream"         json:"stream"`
	Detect       DetectConfig   `toml:"detect"         json:"detect"`
	Monitor      MonitorConfig  `toml:"monitor"        json:"monitor"`
	Features     FeaturesConfig `toml:"features"       json:"features"`
	Dev          DevConfig      `toml:"dev"            json:"dev"`
	Dispatch     DispatchConfig `toml:"dispatch"       json:"dispatch"`
	Peers        PeersConfig    `toml:"peers"          json:"peers"`
}

func defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Bind:      "127.0.0.1",
		Port:      7860,
		DataDir:   filepath.Join(home, ".config", "pdx"),
		UploadDir: filepath.Join(home, "tmp", "purdex-upload"),
		Stream: StreamConfig{
			Presets: []Preset{{
				Name:    "cc",
				Command: "claude -p --verbose --input-format stream-json --output-format stream-json",
			}},
		},
		Detect: DetectConfig{
			CCCommands:   []string{"claude"},
			PollInterval: 2,
		},
		Monitor: MonitorConfig{
			RefreshIntervalMS: 5000,
			TopProcessLimit:   10,
		},
	}
}

// Load reads config from path. Empty path → tries ~/.config/pdx/config.toml.
// Missing file → returns defaults (no error). Invalid TOML → returns error.
func Load(path string) (Config, error) {
	cfg := defaults()

	if path == "" {
		path = filepath.Join(cfg.DataDir, "config.toml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config: %w", err)
	}

	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}
