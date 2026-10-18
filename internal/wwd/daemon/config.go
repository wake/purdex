// Package daemon is the wwd process: configuration, pool token, the two HTTP
// surfaces (token-protected TCP and the trusted local socket).
package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"

	"github.com/wake/purdex/internal/wwd/fault"
	"github.com/wake/purdex/internal/wwd/jobdb"
	"github.com/wake/purdex/internal/wwd/wire"
)

// Config is ~/.config/wwd/config.toml (spec §2). Every field is optional.
type Config struct {
	Alias       string       `toml:"alias"`
	Port        int          `toml:"port"`
	TailnetAddr string       `toml:"tailnet_addr"`
	DataDir     string       `toml:"data_dir"`
	SocketPath  string       `toml:"socket"`
	TokenPath   string       `toml:"token_file"`
	WorkRoot    string       `toml:"work_root"`
	Worker      WorkerConfig `toml:"worker"`
	Owner       OwnerRule    `toml:"owner"`
	Peers       []PeerConfig `toml:"peers"`
	Observe     ObserveRule  `toml:"observe"`
	JobDB       JobDBConfig  `toml:"jobdb"`
	Fault       fault.Config `toml:"fault"` // test hooks for the C2 matrix; all off unless enabled here
	// Repos lists the prepared clones of this host: repo name = clone path
	// (absolute, or ~/ for home). A path counts only once internal/wwd/clones has
	// verified it; the verified repos become repo:<name> labels.
	Repos map[string]string `toml:"repos"`
}

// WorkerConfig is the worker's starting settings. Heavy and Light are slots; an
// unset value is the default (heavy 1, light 2), an explicit 0 is kept (mserv).
type WorkerConfig struct {
	Enabled bool `toml:"enabled"`
	Heavy   *int `toml:"heavy"`
	Light   *int `toml:"light"`
}

// Slots is the configured slot pair with the defaults filled in.
func (w WorkerConfig) Slots() wire.Slots {
	s := wire.Slots{Heavy: 1, Light: 2}
	if w.Heavy != nil {
		s.Heavy = max(*w.Heavy, 0)
	}
	if w.Light != nil {
		s.Light = max(*w.Light, 0)
	}
	return s
}

// JobDBConfig is the per-job MySQL database (internal/wwd/jobdb). Off unless Enabled; a host without it rejects a task that asks
// for db "per-job". DefaultsFile is the administrator's mysql [client] file (the wwd_admin account, 600).
type JobDBConfig struct {
	Enabled      bool     `toml:"enabled"`
	Host         string   `toml:"host"`
	Port         int      `toml:"port"`
	UserHosts    []string `toml:"user_hosts"`
	DefaultsFile string   `toml:"defaults_file"`
}

// OwnerRule turns the "owner is active" rule on (air26 only).
type OwnerRule struct {
	Enabled     bool `toml:"enabled"`
	IdleMinutes int  `toml:"idle_minutes"`
}

// ObserveRule is when a process tree that belongs to no job is recorded (spec §8.4): CPU seconds of the whole tree, or memory at
// its peak. 0 (unset) takes the default (60 s, 1024 MB).
type ObserveRule struct {
	MinCPUSeconds float64 `toml:"min_cpu_seconds"`
	MinMemMB      int     `toml:"min_mem_mb"`
}

// PeerConfig is another wwd of the pool: its alias and tailnet address
// (host or host:port; the port defaults to 7871).
type PeerConfig struct {
	Alias string `toml:"alias"`
	Addr  string `toml:"addr"`
}

// HostPort is the address with the default port filled in.
func (p PeerConfig) HostPort() string {
	if _, _, err := net.SplitHostPort(p.Addr); err == nil {
		return p.Addr
	}
	return net.JoinHostPort(p.Addr, strconv.Itoa(wire.Port))
}

func (c Config) validateWorker() error {
	if c.Worker.Heavy != nil && *c.Worker.Heavy < 0 {
		return fmt.Errorf("worker.heavy is %d, slots cannot be negative", *c.Worker.Heavy)
	}
	if c.Worker.Light != nil && *c.Worker.Light < 0 {
		return fmt.Errorf("worker.light is %d, slots cannot be negative", *c.Worker.Light)
	}
	if m := c.Owner.IdleMinutes; m < 0 || m > 9999 {
		return fmt.Errorf("owner.idle_minutes is %d, it must be 0 (the default) or 1-9999", m)
	}
	if v := c.Observe.MinCPUSeconds; v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("observe.min_cpu_seconds is %g, it must be 0 (the default) or a finite number above it", v)
	}
	if err := c.Fault.Validate(); err != nil {
		return err
	}
	if c.Observe.MinMemMB < 0 {
		return fmt.Errorf("observe.min_mem_mb is %d, it must be 0 (the default) or more", c.Observe.MinMemMB)
	}
	return nil
}

// validateJobDB checks the per-job database settings when they are on, and expands ~/ in the defaults file. The administrator's
// credentials are never a default: without a defaults file the mysql client would take whatever account it finds.
func (c *Config) validateJobDB(home string) error {
	j := &c.JobDB
	if !j.Enabled {
		return nil
	}
	if j.Port < 0 || j.Port > 65535 {
		return fmt.Errorf("jobdb.port is %d", j.Port)
	}
	if j.DefaultsFile == "" {
		return fmt.Errorf("jobdb.defaults_file is empty; name the wwd_admin client file")
	}
	if j.DefaultsFile == "~" || strings.HasPrefix(j.DefaultsFile, "~/") {
		j.DefaultsFile = filepath.Join(home, strings.TrimPrefix(j.DefaultsFile, "~"))
	}
	if !filepath.IsAbs(j.DefaultsFile) {
		return fmt.Errorf("jobdb.defaults_file %q is not an absolute path", j.DefaultsFile)
	}
	// it holds the administrator's password: it must be there, be a plain file and be closed to group and others
	fi, err := os.Stat(j.DefaultsFile)
	if err != nil {
		return fmt.Errorf("jobdb.defaults_file: %w", err)
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("jobdb.defaults_file %s must be a regular file with mode 600 (it is %v)", j.DefaultsFile, fi.Mode())
	}
	// the same rule jobdb.New applies, so a bad host stops the start instead of the first job
	for _, h := range j.UserHosts {
		if !jobdb.ValidUserHost(h) {
			return fmt.Errorf("jobdb.user_hosts: %q is not a plain host name or address", h)
		}
	}
	return nil
}

func (c Config) validatePeers() error {
	seen := map[string]bool{}
	for _, p := range c.Peers {
		if !wire.ValidAlias(p.Alias) || seen[p.Alias] {
			return fmt.Errorf("peers: alias %q is empty, repeated, over %d bytes or holds a control character", p.Alias, wire.MaxAliasBytes)
		}
		seen[p.Alias] = true
		host, port, err := net.SplitHostPort(p.HostPort())
		if err != nil {
			return fmt.Errorf("peers: %s: %w", p.Alias, err)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("peers: %s: invalid port %q", p.Alias, port)
		}
		// the pool token is sent to this address: only loopback and tailnet IPs
		if err := ValidateBindAddr(host); err != nil {
			return fmt.Errorf("peers: %s: %w", p.Alias, err)
		}
	}
	return nil
}

var reRepoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// validateRepos checks the names (they become label text) and expands ~/ in the
// paths, which must then be absolute.
func (c *Config) validateRepos(home string) error {
	for name, p := range c.Repos {
		if !reRepoName.MatchString(name) {
			return fmt.Errorf("repos: name %q is not a plain repo name (letters, digits, . _ -)", name)
		}
		if p == "~" || strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
		if !filepath.IsAbs(p) {
			return fmt.Errorf("repos: %s: path %q is not absolute", name, p)
		}
		c.Repos[name] = filepath.Clean(p)
	}
	return nil
}

// DefaultConfigPath is ~/.config/wwd/config.toml.
func DefaultConfigPath(home string) string {
	return filepath.Join(home, ".config", "wwd", "config.toml")
}

// LoadConfig reads path (a missing file means all defaults) and fills defaults
// relative to home. Unknown keys are an error so typos cannot pass silently.
func LoadConfig(path, home string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		md, err := toml.Decode(string(b), &c)
		if err != nil {
			return c, fmt.Errorf("config %s: %w", path, err)
		}
		if u := md.Undecoded(); len(u) > 0 {
			return c, fmt.Errorf("config %s: unknown key %s", path, u[0])
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return c, err
	}
	if err := c.validateWorker(); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	if err := c.validatePeers(); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	if err := c.validateRepos(home); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	if err := c.validateJobDB(home); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	if c.Port == 0 {
		c.Port = wire.Port
	}
	if c.DataDir == "" {
		c.DataDir = filepath.Join(home, ".local", "share", "wwd")
	}
	if c.SocketPath == "" {
		c.SocketPath = filepath.Join(c.DataDir, "wwd.sock")
	}
	if c.TokenPath == "" {
		c.TokenPath = filepath.Join(home, ".config", "wwd", "pool.token")
	}
	if c.WorkRoot == "" {
		c.WorkRoot = filepath.Join(c.DataDir, "work")
	}
	return c, nil
}

// EnsureToken returns the pool token, creating a random one (0600) when the
// file does not exist. A token file readable by others, or empty, is refused.
func EnsureToken(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		tok := hex.EncodeToString(raw)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return "", err
		}
		defer f.Close()
		if _, err := f.WriteString(tok + "\n"); err != nil {
			return "", err
		}
		return tok, nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	// One descriptor for the checks and the read: no second pathname lookup.
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("token file %s is not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file %s: mode %v is readable by others; chmod 600", path, st.Mode().Perm())
	}
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	return tok, nil
}
