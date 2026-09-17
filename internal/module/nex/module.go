package nex

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"lab.protype.tw/wake/nexen"

	pdxconfig "github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// engine is the private seam over *nexen.System: the three things the
// module needs from an assembled Nexen (serve, drain, release). Tests in
// this package substitute a fake engine through assembleFn; production
// goes through realAssemble.
type engine struct {
	handler  http.Handler
	shutdown func(context.Context) error
	close    func() error
}

// assembleFn builds an engine from the Options buildOptions produced. The
// production value is realAssemble (a thin adapter over nexen.Assemble).
type assembleFn func(context.Context, nexen.Options) (engine, error)

// realAssemble adapts *nexen.System to engine. The System pointer is
// captured by the closures, never copied (System carries an atomic.Bool
// and sync.Once fields; see its doc comment on why a copy would break
// Shutdown).
func realAssemble(ctx context.Context, opts nexen.Options) (engine, error) {
	sys, err := nexen.Assemble(ctx, opts)
	if err != nil {
		return engine{}, err
	}
	return engine{
		handler:  sys.Handler,
		shutdown: sys.Shutdown,
		close:    sys.Close,
	}, nil
}

// Module embeds the Nexen execution engine as a pdx daemon module and
// mounts its HTTP API under RoutePrefix.
type Module struct {
	core       *core.Core
	sys        engine
	opts       nexen.Options
	expanded   pdxconfig.NexConfig // [nex] after "~" expansion; what Status reports as effective
	pathPrefix string              // applied path_prepend entries only, joined by the list separator; for the Start log line

	// initErr is why the engine is not running (spec §4.4.1 soft-fail). Set
	// by Init for every failure past static validation; nil when the engine
	// assembled. RegisterRoutes mounts the 503 fallback when it is set.
	initErr error

	// origPath is the process PATH before Init applied the path_prepend
	// policy, and pathChanged whether it did; softFail restores it so a
	// daemon whose engine never assembled keeps its original environment.
	origPath    string
	pathChanged bool

	assemble assembleFn        // default realAssemble; test seam
	isDir    func(string) bool // default statIsDir; test seam
	logf     func(string, ...any)
}

// New returns a Module wired with production defaults.
func New() *Module {
	return &Module{
		assemble: realAssemble,
		isDir:    statIsDir,
		logf:     log.Printf,
	}
}

// statIsDir reports whether p is an existing directory (symlinks followed).
func statIsDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func (m *Module) Name() string { return "nex" }

func (m *Module) Dependencies() []string { return nil }

// Init prepares and assembles the engine, in this order:
//  1. re-validate and expand the [nex] config against the user's home, so
//     `~/...` entries in path_prepend are real paths before they are
//     checked;
//  2. apply the PATH prepend policy to the process environment (the
//     engine's `claude -p` children inherit it; a later soft-fail restores
//     the original PATH);
//  3. map the config onto nexen.Options (buildOptions);
//  4. create <DataDir>/nex;
//  5. assemble the engine.
//
// The HOME rule mirrors config.Load's (spec §4.2): a missing $HOME
// (os.UserHomeDir fails — a launchd/Finder-started daemon) is not an error
// by itself. Validate(home) with home == "" rejects only a `~` entry that
// would need expanding, naming HOME and the key; an enabled host whose
// roots/claude_bin are absolute and whose path_prepend is []
// initialises without HOME. Validate is cheap and re-run here so Init is
// self-contained rather than trusting that the config went through Load.
//
// Every failure is wrapped with the "nex: init:" prefix. Only the initial
// Validate error is fatal (a static config shape error — config.Load
// should already have rejected it). Every later failure (buildOptions,
// creating data_dir, assembling the engine) is a soft-fail (spec §4.4.1,
// I8): Init still returns nil, m.initErr records why, and RegisterRoutes /
// Status surface it — a broken [nex] must not take the terminal daemon
// down.
func (m *Module) Init(c *core.Core) error {
	m.core = c

	home, _ := os.UserHomeDir() // "" when unset; Validate decides whether that matters
	if err := c.Cfg.Nex.Validate(home); err != nil {
		return fmt.Errorf("nex: init: %w", err)
	}
	n := c.Cfg.Nex.Expanded(home)
	m.expanded = n

	// Log the applied prefix and the original PATH's element count, not
	// the full PATH: on a developer machine that is kilobytes per line.
	m.origPath = os.Getenv("PATH")
	pathElements := len(filepath.SplitList(m.origPath))
	_, changed := applyPathPolicy(n.PathPrepend, m.isDir)
	m.pathChanged = changed
	m.pathPrefix = strings.Join(existingPrefix(n.PathPrepend, m.isDir), string(os.PathListSeparator))
	if changed {
		m.logf("nex: PATH policy applied (path_prepend=%q): prefix=%s path_elements=%d", n.PathPrepend, m.pathPrefix, pathElements)
	} else {
		m.logf("nex: PATH policy left PATH unchanged (path_prepend=%q): prefix=%s path_elements=%d", n.PathPrepend, m.pathPrefix, pathElements)
	}

	// buildOptions errors carry no "nex:" prefix of their own (a Nexen
	// Validate error starts with "config:"), so this wrap is the only one.
	opts, err := buildOptions(c.Cfg.HostID, c.Cfg.DataDir, n, core.ShutdownBudget)
	if err != nil {
		return m.softFail(fmt.Errorf("nex: init: %w", err))
	}
	m.opts = opts

	if err := os.MkdirAll(opts.Config.DataDir, 0o755); err != nil {
		return m.softFail(fmt.Errorf("nex: init: creating data_dir %s: %w", opts.Config.DataDir, err))
	}

	sys, err := m.assemble(context.Background(), opts)
	if err != nil {
		return m.softFail(fmt.Errorf("nex: init: assembling engine: %w", err))
	}
	m.sys = sys
	return nil
}

// softFail records why the engine is unavailable and reports success to the
// core: a broken [nex] must not take the terminal daemon down (spec §4.4.1,
// I8). The 503 fallback handler and Status() surface the error instead.
//
// The PATH policy is applied before assemble (the engine may resolve
// `claude` through PATH while assembling), so a failure past that point
// restores the original PATH here: the policy exists only for the engine's
// children, and with no engine it must not leak into the rest of the
// daemon.
func (m *Module) softFail(err error) error {
	m.initErr = err
	m.sys = engine{}
	if m.pathChanged {
		os.Setenv("PATH", m.origPath)
		m.pathChanged = false
	}
	m.logf("nex: init failed (engine unavailable, /api/nex answers 503): %v", err)
	return nil
}

// RegisterRoutes mounts the engine's handler under RoutePrefix, stripping
// the prefix (the engine is told about it through Options.PublicPrefix so
// the URLs it emits stay correct) and containing per-request panics. When
// Init soft-failed, every path under RoutePrefix instead answers 503
// nex_unavailable (spec §4.4.1, I8).
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	if m.initErr != nil {
		mux.Handle(RoutePrefix+"/", unavailableHandler(m.initErr))
		return
	}
	mux.Handle(RoutePrefix+"/", http.StripPrefix(RoutePrefix, recoverer(m.logf, m.sys.handler)))
}

// unavailableHandler answers every request under RoutePrefix with the same
// structured error shape Nexen uses, so one client-side parser covers both.
func unavailableHandler(initErr error) http.Handler {
	body, _ := json.Marshal(map[string]string{"error": initErr.Error(), "code": "nex_unavailable"})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(body)
	})
}

// Start logs what the module is serving. When Init soft-failed there is
// nothing to log or serve. The engine is already live after a successful
// Init; there is nothing further to start.
func (m *Module) Start(context.Context) error {
	if m.initErr != nil {
		return nil
	}
	cfg := m.opts.Config
	claudeBin := m.opts.ClaudeBin
	if claudeBin == "" {
		claudeBin = "claude (via PATH)"
	}
	m.logf("nex: serving %s (host_id=%s, data_dir=%s, claude_bin=%s, profiles=%s, path_prepend=%s)",
		RoutePrefix, cfg.HostID, cfg.DataDir, claudeBin, profilesText(cfg.Sandbox.MaxProfile, cfg.Sandbox.DefaultProfile), m.pathPrefix)
	return nil
}

// profilesText renders the sandbox policy as "max=<max>,default=<default>"
// with an empty name spelled out as what Nexen actually applies — its
// fail-closed default, "readonly" — so the log does not hide that an
// unset profile is the most restrictive one.
func profilesText(maxProfile, defaultProfile string) string {
	name := func(s string) string {
		if strings.TrimSpace(s) == "" {
			return "readonly (nexen fail-closed default)"
		}
		return s
	}
	return "max=" + name(maxProfile) + ",default=" + name(defaultProfile)
}

// Stop drains the engine within ctx's budget (core.ShutdownBudget, shared
// with the HTTP server's Shutdown).
//
// Stop and Close are no-ops when Init never assembled an engine (only a
// Validate error from Init is fatal to the daemon; an engine-assembly error
// soft-fails, spec §4.4.1, and the lifecycle still walks this Module).
func (m *Module) Stop(ctx context.Context) error {
	if m.sys.shutdown == nil {
		return nil
	}
	return m.sys.shutdown(ctx)
}

// Close releases the engine's store and credential scope. Called by
// core.CloseModules after the HTTP server has stopped.
func (m *Module) Close() error {
	if m.sys.close == nil {
		return nil
	}
	return m.sys.close()
}
