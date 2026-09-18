package config

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

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
	// InboundTokenPrev is the outgoing inbound token during a rotation
	// (spec §6): non-empty means a rotation is pending and BOTH values
	// authenticate. Cleared by rotate/commit (drop the old) or
	// rotate/cancel (restore the old as the only one).
	InboundTokenPrev string `toml:"inbound_token_prev" json:"inbound_token_prev"`
	AllowBypass      bool   `toml:"allow_bypass"  json:"allow_bypass"`
}

type PeersConfig struct {
	Alias   string     `toml:"alias"   json:"alias"`
	Hosts   []PeerHost `toml:"hosts"   json:"hosts"`
	Deliver bool       `toml:"deliver" json:"deliver"` // default false; enables inbound /api/peers/deliver
}

// aliasPattern matches one URL path segment safe to use as a peer alias:
// starts with an alphanumeric, followed by up to 63 more alphanumerics,
// dots, underscores or hyphens.
var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ErrSelfAliasCollision is the sentinel ValidateSelfAlias's collision error
// matches under errors.Is (spec S-6, #1196): the settings handler maps it
// to 409 rather than the 400 a shape error gets. It is never returned
// directly — the returned error carries the alias-specific text and this
// sentinel only identifies its kind.
var ErrSelfAliasCollision = errors.New("self alias collides with a peer host alias")

// selfAliasCollisionError is ValidateSelfAlias's collision error: its text
// is exactly `alias %q is already used by a peer host` (the wording the
// entry side uses for the mirror case, spec S-6) and it answers errors.Is
// for ErrSelfAliasCollision without appending the sentinel's text.
type selfAliasCollisionError struct{ alias string }

func (e *selfAliasCollisionError) Error() string {
	return fmt.Sprintf("alias %q is already used by a peer host", e.alias)
}

func (e *selfAliasCollisionError) Is(target error) bool { return target == ErrSelfAliasCollision }

// validateAliasShape is the shape rule both kinds of alias share (spec
// S-1): not "." or "..", and one URL path segment per aliasPattern.
func validateAliasShape(alias string) error {
	if alias == "." || alias == ".." {
		return fmt.Errorf("alias %q is reserved", alias)
	}
	if !aliasPattern.MatchString(alias) {
		return fmt.Errorf("alias %q must match %s", alias, aliasPattern.String())
	}
	return nil
}

// ValidateAlias checks that alias is safe to use as a one-segment URL path
// component and distinct from localAlias (case-insensitive).
func ValidateAlias(alias, localAlias string) error {
	if err := validateAliasShape(alias); err != nil {
		return err
	}
	if strings.EqualFold(alias, localAlias) {
		return fmt.Errorf("alias %q collides with the local alias", alias)
	}
	return nil
}

// ValidateSelfAlias checks that alias is safe as this host's own alias
// (spec S-1, #1196): the same shape rule as ValidateAlias, and not
// (case-insensitively) the alias of any configured peer host — the local
// alias and the peer aliases share the <alias>/<name> address namespace on
// this host, so this is the mirror image of ValidateAlias's "not the local
// alias" clause. A collision matches ErrSelfAliasCollision under errors.Is.
// Empty is not valid input here; callers clear Peers.Alias without
// validating (S-2).
func ValidateSelfAlias(alias string, hosts []PeerHost) error {
	if err := validateAliasShape(alias); err != nil {
		return err
	}
	for _, h := range hosts {
		if strings.EqualFold(alias, h.Alias) {
			return &selfAliasCollisionError{alias: alias}
		}
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

// TokenFingerprint is a short, non-reversible identity for a bearer token:
// the first 16 hex characters of its SHA-256. It is not a secret and it is
// not the token — it lets a record say WHICH of an entry's tokens a peer
// presented (the peers module's rotation record, spec §6.2) without holding
// the value. "" fingerprints to "" so an unset token never matches anything.
func TokenFingerprint(tok string) string {
	if tok == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])[:16]
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

// MatchInboundToken returns the host whose InboundToken OR InboundTokenPrev
// equals bearer, and whether it was the prev one. Every non-empty field of
// every entry is compared with subtle.ConstantTimeCompare and there is no
// early exit; when more than one field matches, the LAST one wins (an
// operator who pasted one token into two entries gets a deterministic
// answer, not a random one). An empty bearer never matches.
func (p PeersConfig) MatchInboundToken(bearer string) (host PeerHost, usedPrev bool, ok bool) {
	if bearer == "" {
		return PeerHost{}, false, false
	}
	matchIdx := -1
	bearerBytes := []byte(bearer)
	for i, h := range p.Hosts {
		if h.InboundToken != "" && subtle.ConstantTimeCompare(bearerBytes, []byte(h.InboundToken)) == 1 {
			matchIdx, usedPrev = i, false
		}
		if h.InboundTokenPrev != "" && subtle.ConstantTimeCompare(bearerBytes, []byte(h.InboundTokenPrev)) == 1 {
			matchIdx, usedPrev = i, true
		}
	}
	if matchIdx == -1 {
		return PeerHost{}, false, false
	}
	return p.Hosts[matchIdx], usedPrev, true
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
//
// It is the JSON view of the config (GET/PUT /api/config), so nex lists that
// are nil — a TOML without the key, or a default — are emitted as [] rather
// than null. This is done here, not in Load, because the TOML encoder omits
// a nil slice but writes an empty one: normalising at Load would add
// `repo_roots = []` lines to every config.toml written back.
func (c Config) Redacted() Config {
	out := c.Clone()
	out.Nex.RepoRoots = nonNil(out.Nex.RepoRoots)
	out.Nex.ServiceRoots = nonNil(out.Nex.ServiceRoots)
	out.Nex.PathPrepend = nonNil(out.Nex.PathPrepend)
	out.Token = ""
	out.HostID = ""
	for i := range out.Peers.Hosts {
		out.Peers.Hosts[i].Token = ""
		out.Peers.Hosts[i].InboundToken = ""
		out.Peers.Hosts[i].InboundTokenPrev = ""
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

	out.Detect.CCCommands = slices.Clone(c.Detect.CCCommands)

	out.Peers.Hosts = slices.Clone(c.Peers.Hosts)

	out.Nex.RepoRoots = slices.Clone(c.Nex.RepoRoots)
	out.Nex.ServiceRoots = slices.Clone(c.Nex.ServiceRoots)
	out.Nex.PathPrepend = slices.Clone(c.Nex.PathPrepend)

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
	Detect       DetectConfig   `toml:"detect"         json:"detect"`
	Monitor      MonitorConfig  `toml:"monitor"        json:"monitor"`
	Features     FeaturesConfig `toml:"features"       json:"features"`
	Dev          DevConfig      `toml:"dev"            json:"dev"`
	Peers        PeersConfig    `toml:"peers"          json:"peers"`
	Nex          NexConfig      `toml:"nex"            json:"nex"`
}

func defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Bind:      "127.0.0.1",
		Port:      7860,
		DataDir:   filepath.Join(home, ".config", "pdx"),
		UploadDir: filepath.Join(home, "tmp", "purdex-upload"),
		Detect: DetectConfig{
			CCCommands:   []string{"claude"},
			PollInterval: 2,
		},
		Monitor: MonitorConfig{
			RefreshIntervalMS: 5000,
			TopProcessLimit:   10,
		},
		Nex: DefaultNexConfig(),
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

	// A failed UserHomeDir (no HOME in the environment) is deliberately
	// passed on as home == "": NexConfig.Validate then skips "~" entries
	// for a disabled section and reports "HOME is not set" for an enabled
	// one, so a host that never opted into nex still loads. The error is
	// returned unwrapped — it already names the key ("nex.<key>: …") and
	// callers add their own prefix (`config:` in main.go/daemon.go; the CLI
	// subcommands print `pdx <cmd>: …`).
	home, _ := os.UserHomeDir()
	if err := cfg.Nex.Validate(home); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// nonNil returns s, or an empty non-nil slice when s is nil.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
