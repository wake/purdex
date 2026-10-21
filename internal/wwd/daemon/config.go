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
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/wake/purdex/internal/wwd/fault"
	"github.com/wake/purdex/internal/wwd/jobdb"
	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/slicecfg"
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
	Advisor     AdvisorRule  `toml:"advisor"`
	JobDB       JobDBConfig  `toml:"jobdb"`
	Tools       ToolsConfig  `toml:"tools"`
	Jobs        JobsConfig   `toml:"jobs"`
	Fault       fault.Config `toml:"fault"` // test hooks for the C2 matrix; all off unless enabled here
	// Slice is the limits `wwd install` writes for wwd.slice on a Linux host (#3549). The daemon does not use it; it is a known key so
	// that an operator's override does not stop wwd from starting.
	Slice slicecfg.Limits `toml:"slice"`
	// Repos lists the prepared clones of this host: repo name = clone path
	// (absolute, or ~/ for home). A path counts only once internal/wwd/clones has
	// verified it; the verified repos become repo:<name> labels.
	Repos map[string]string `toml:"repos"`
	// ManifestRefs is repo name = git ref (for example origin/main) for the prepared clones whose manifest is read at that ref, and whose
	// qualification snapshot is taken at the commit it names, instead of the working tree (#3658). Such a clone is fetched in the
	// background. A repo not listed keeps reading its working tree.
	ManifestRefs map[string]string `toml:"manifest_refs"`
	// DepsRoot holds the dependency cache and the package managers' download caches (default <data_dir>/deps; keep it on the volume of work_root
	// so that installed dependencies are cloned, not copied).
	DepsRoot string `toml:"deps_root"`
}

// WorkerConfig is the worker's starting settings. Heavy and Light are slots; an
// unset value is the default (heavy 1, light 2), an explicit 0 is kept (mserv).
type WorkerConfig struct {
	Enabled bool `toml:"enabled"`
	Heavy   *int `toml:"heavy"`
	Light   *int `toml:"light"`
	// BackgroundDev runs dev jobs under macOS background QoS (mlab; spec §5 Platform limits).
	BackgroundDev bool `toml:"background_dev"`
	// SpeedFactor is this host's speed against the reference host for size_penalty (spec §4.6); 0 means none.
	SpeedFactor float64 `toml:"speed_factor"`
	// DeferPatienceSeconds is how long a queued job is left to a faster host of the pool that could take it right now, before this one takes it
	// (#3904). Not set: 120. 0: never leave a job (the switch off). It only matters on a host whose speed_factor is above that of a peer.
	DeferPatienceSeconds *int `toml:"defer_patience_seconds"`
	// ExplicitLabelsOnly: the worker takes only jobs whose labels name this host's os: label, a task: label it earned (#3441; mserv) or a host: label (#3873).
	ExplicitLabelsOnly bool `toml:"explicit_labels_only"`
	// SandboxUser is the account job commands run as, in a transient systemd unit (Linux hosts such as mserv, spec §5). Empty: no
	// sandbox, the command runs as a process group of the daemon's own account.
	SandboxUser string `toml:"sandbox_user"`
	// DepsKeep is how many installed dependency sets the cache keeps (default 2; a set is a node_modules or vendor tree, hundreds of MB for a
	// large project). 1 on a host short of disk.
	DepsKeep int `toml:"deps_keep"`
	// DepsStoreMaxMB bounds each package manager's download cache under deps_root (default 4096): one over it is emptied before the next install.
	DepsStoreMaxMB int `toml:"deps_store_max_mb"`
	// GoCacheMaxMB bounds the shared Go build cache of the go tasks (one per Go version, under deps_root/gocache; default 10240): over it, and
	// with no job using it, it is emptied before the next job.
	GoCacheMaxMB int `toml:"gocache_max_mb"`
	// GoCachePruneHours: an entry of that cache not used for this many hours is removed (default 24); over the bound the oldest entries go
	// first, and only a cache that cannot be trimmed is emptied.
	GoCachePruneHours int `toml:"gocache_prune_hours"`
	// DiskMinGB is the free space the work volume must have for a heavy run to start (a queue job or an adhoc command): 0 is no floor,
	// unset is 10 (the pdx gate's disk_min_gb default). It is where the setting starts; a PUT /v1/worker is kept in wwd.db and wins.
	DiskMinGB *float64 `toml:"disk_min_gb"`
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

// ToolsConfig is where this host keeps tools that are not on the default search path. ExtraPath holds absolute or ~/ directories; they
// go in front of the default tool directories, for the facts' tool probe and for a job's PATH alike. A directory that does not exist
// is skipped and logged when wwd starts (facts.ResolveExtraDirs), so one config can serve hosts that differ.
type ToolsConfig struct {
	ExtraPath []string `toml:"extra_path"`
}

func (c *Config) validateTools() error {
	for i, p := range c.Tools.ExtraPath {
		if p == "" || strings.ContainsRune(p, os.PathListSeparator) {
			return fmt.Errorf("tools.extra_path: %q is not a single directory", p)
		}
		if p != "~" && !strings.HasPrefix(p, "~/") {
			if !filepath.IsAbs(p) {
				return fmt.Errorf("tools.extra_path: %q is not absolute (or ~/...)", p)
			}
			c.Tools.ExtraPath[i] = filepath.Clean(p)
		}
	}
	return nil
}

// JobsConfig is what the host gives to its jobs (#3933).
type JobsConfig struct {
	// Env is variables of the host's own for the jobs of its worker: where something is on this host (a browser cache, a toolchain
	// directory). A leading ~ or ~/ in a value is the home of the account wwd runs as, not the job's workspace home. A job still sees only the
	// names its task lists in `env`; a name the runner owns (HOME, TMPDIR, XDG_*, USER, LOGNAME, WWD_*, PDX_*), PATH ([tools] extra_path) and
	// TERM cannot be set here, and wwd does not start with one. Values are not part of a result's reuse key or of the dependency key: a value
	// that changes what the job computes belongs in the manifest (`NAME=value`).
	Env map[string]string `toml:"env"`
}

func (c *Config) validateJobs(home string) error {
	for name, v := range c.Jobs.Env {
		if !run.ValidEnvName(name) {
			return fmt.Errorf("jobs.env: %q is not an environment variable name", name)
		}
		if run.ReservedEnvName(name) {
			return fmt.Errorf("jobs.env: %s is set by the runner for every job and cannot be given by the host", name)
		}
		switch name {
		case "PATH":
			return fmt.Errorf("jobs.env: PATH is the tool list; use [tools] extra_path")
		case "TERM":
			return fmt.Errorf("jobs.env: TERM is set by the runner")
		}
		if strings.ContainsRune(v, 0) {
			return fmt.Errorf("jobs.env: the value of %s has a NUL in it", name)
		}
		if v == "~" {
			c.Jobs.Env[name] = home
		} else if rest, ok := strings.CutPrefix(v, "~/"); ok {
			c.Jobs.Env[name] = filepath.Join(home, rest)
		}
	}
	return nil
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

// AdvisorRule is the alert rules' own settings (spec §13.3). KnownResidents names the system or resident processes whose large footprint
// is expected on this host: they are not reported by "resident growth" while the host has ≥ 50 % memory available and the swap did not
// grow. Empty (the default): none is known, every large resident is reported.
type AdvisorRule struct {
	KnownResidents []string `toml:"known_residents"`
	// PartnerCheck turns on the partner check of the silence rules (spec §13.3): this wwd tells when the Purdex daemon of its own host has
	// not read its alerts for 3 minutes. Only a host that runs a Purdex daemon (mlab, air26) sets it; the default is off, since a host
	// without one (mserv, air19) would otherwise be told it has lost a partner it never had.
	PartnerCheck bool `toml:"partner_check"`
	// WatchRules turns on the watcher's three pressure rules (spec §13.3: pressure_watch, pressure_danger, swap_growth). Off by default: they
	// write a level and kinds an older reader of the alert list refuses whole, so they are switched on host by host once every reader has the
	// build that knows them (docs/wwd-deploy.md).
	WatchRules bool `toml:"watch_rules"`
	// QueueRules turns on the four rules of the queue (WWD-9 S3-3b: queue_stalled, job_requeued, queue_backlog, queue_unclaimable). Off by default,
	// for the same reason as WatchRules: a reader from before S3-3a refuses the whole list that holds one of the kinds. Only the host that the
	// jobs are queued on has a queue to judge; the others have an empty one and the rules hold nowhere (docs/wwd-deploy.md).
	QueueRules bool `toml:"queue_rules"`
	// Off names the watcher rules this host does not judge (only the three kinds above); air19 lists the two that mean nothing to a laptop
	// whose Windows VM pages all day. It takes a rule out, and never switches one on: WatchRules decides that.
	Off []string `toml:"off"`
	// SwapGrowthMB is the growth of the swap in use over 30 minutes that raises swap_growth; 0 takes the default by RAM (500 MB up to 16 GB,
	// more in proportion above it).
	SwapGrowthMB int `toml:"swap_growth_mb"`
}

// maxSwapGrowthMB bounds swap_growth_mb: a growth of 1 TB over half an hour is a typo, not a setting.
const maxSwapGrowthMB = 1 << 20

// watchKinds are the alert kinds [advisor] off may name: the watcher rules added by S3-2.
var watchKinds = []wire.AlertKind{wire.AlertPressureWatch, wire.AlertPressureDanger, wire.AlertSwapGrowth}

// WatchRuleOn says whether this host judges the watcher rule of kind k: watch_rules is on and k is not in off. A kind that is not a
// watcher rule is never on here (the other rules do not depend on watch_rules).
func (a AdvisorRule) WatchRuleOn(k wire.AlertKind) bool {
	if !a.WatchRules || !slices.Contains(watchKinds, k) {
		return false
	}
	return !slices.Contains(a.Off, string(k))
}

// PeerConfig is another wwd of the pool: its alias and tailnet address
// (host or host:port; the port defaults to 7871). AlertUnreachable (spec §13.3 silence) says whether this host raises an alert when the
// peer has not answered for 3 minutes; the default is on, and air19 (a laptop that sleeps) sets it to false.
type PeerConfig struct {
	Alias            string `toml:"alias"`
	Addr             string `toml:"addr"`
	AlertUnreachable *bool  `toml:"alert_unreachable"`
}

// AlertsUnreachable is AlertUnreachable with its default (on).
func (p PeerConfig) AlertsUnreachable() bool { return p.AlertUnreachable == nil || *p.AlertUnreachable }

// HostPort is the address with the default port filled in.
func (p PeerConfig) HostPort() string {
	if _, _, err := net.SplitHostPort(p.Addr); err == nil {
		return p.Addr
	}
	return net.JoinHostPort(p.Addr, strconv.Itoa(wire.Port))
}

var reSandboxUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (c Config) validateWorker() error {
	if c.Worker.Heavy != nil && *c.Worker.Heavy < 0 {
		return fmt.Errorf("worker.heavy is %d, slots cannot be negative", *c.Worker.Heavy)
	}
	if c.Worker.Light != nil && *c.Worker.Light < 0 {
		return fmt.Errorf("worker.light is %d, slots cannot be negative", *c.Worker.Light)
	}
	if v := c.Worker.DiskMinGB; v != nil && (math.IsNaN(*v) || *v < 0 || *v > maxDiskMinGB) {
		return fmt.Errorf("worker.disk_min_gb is %g, it must be 0 (no floor) or up to %g", *v, float64(maxDiskMinGB))
	}
	if c.Worker.DepsKeep < 0 || c.Worker.DepsKeep > 50 {
		return fmt.Errorf("worker.deps_keep is %d, it must be 0 (the default, 2) or 1-50", c.Worker.DepsKeep)
	}
	if c.Worker.DepsStoreMaxMB < 0 || c.Worker.DepsStoreMaxMB > 1_000_000 {
		return fmt.Errorf("worker.deps_store_max_mb is %d, it must be 0 (the default, 4096) or 1-1000000", c.Worker.DepsStoreMaxMB)
	}
	if c.Worker.GoCacheMaxMB < 0 || c.Worker.GoCacheMaxMB > 1_000_000 {
		return fmt.Errorf("worker.gocache_max_mb is %d, it must be 0 (the default, 10240) or 1-1000000", c.Worker.GoCacheMaxMB)
	}
	if c.Worker.GoCachePruneHours < 0 || c.Worker.GoCachePruneHours > 8760 {
		return fmt.Errorf("worker.gocache_prune_hours is %d, it must be 0 (the default, 24) or 1-8760", c.Worker.GoCachePruneHours)
	}
	if v := c.Worker.SpeedFactor; v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("worker.speed_factor is %g, it must be 0 (none) or a finite number above it", v)
	}
	if v := c.Worker.DeferPatienceSeconds; v != nil && (*v < 0 || *v > 86400) {
		return fmt.Errorf("worker.defer_patience_seconds is %d, it must be 0 (off) or 1-86400 (not set: 120)", *v)
	}
	if u := c.Worker.SandboxUser; u != "" {
		if runtime.GOOS != "linux" {
			return fmt.Errorf("worker.sandbox_user is for Linux hosts (systemd units); this host is %s", runtime.GOOS)
		}
		if !reSandboxUser.MatchString(u) || u == "root" {
			return fmt.Errorf("worker.sandbox_user %q is not a plain non-root account name", u)
		}
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
	for _, n := range c.Advisor.KnownResidents {
		if n == "" || len(n) > 128 || strings.ContainsFunc(n, unicode.IsControl) {
			return fmt.Errorf("advisor.known_residents has %q: a process name is 1 to 128 characters without control characters", n)
		}
	}
	for _, n := range c.Advisor.Off {
		if !slices.Contains(watchKinds, wire.AlertKind(n)) {
			return fmt.Errorf("advisor.off has %q: it may name only pressure_watch, pressure_danger or swap_growth", n)
		}
	}
	if m := c.Advisor.SwapGrowthMB; m < 0 || m > maxSwapGrowthMB {
		return fmt.Errorf("advisor.swap_growth_mb is %d, it must be 0 (the default by RAM) or 1-%d", m, maxSwapGrowthMB)
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
			return fmt.Errorf("peers: alias %q is empty, a dot name (. or ..), repeated, over %d bytes or holds a control character", p.Alias, wire.MaxAliasBytes)
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

// validateManifestRefs: each entry names a repo of [repos], and a ref is one word that cannot be taken for a git option.
func (c *Config) validateManifestRefs() error {
	for name, ref := range c.ManifestRefs {
		if _, ok := c.Repos[name]; !ok {
			return fmt.Errorf("manifest_refs: %q is not in [repos]", name)
		}
		if ref == "" || strings.HasPrefix(ref, "-") || strings.IndexFunc(ref, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return fmt.Errorf("manifest_refs: %s: %q is not a git ref", name, ref)
		}
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
	if err := c.validateManifestRefs(); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	if err := c.validateJobDB(home); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	if err := c.validateTools(); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	if err := c.validateJobs(home); err != nil {
		return c, fmt.Errorf("config %s: %w", path, err)
	}
	if err := c.Slice.Validate(); err != nil {
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
	if c.DepsRoot == "" {
		c.DepsRoot = filepath.Join(c.DataDir, "deps")
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

// DeferPatience is how long a job is left to a faster host before this one takes it: 2 minutes unless set, 0 for never.
func (w WorkerConfig) DeferPatience() time.Duration {
	if w.DeferPatienceSeconds == nil {
		return 2 * time.Minute
	}
	return time.Duration(*w.DeferPatienceSeconds) * time.Second
}
