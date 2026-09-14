package nex

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"lab.protype.tw/wake/nexen"

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
	pathPrefix string // applied path_prepend entries only, joined by the list separator; for the Start log line

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
//  1. expand the [nex] config against the user's home, so `~/...` entries
//     in path_prepend are real paths before they are checked;
//  2. apply the PATH prepend policy to the process environment (the
//     engine's `claude -p` children inherit it);
//  3. map the config onto nexen.Options (buildOptions);
//  4. create <DataDir>/nex;
//  5. assemble the engine.
//
// Every failure is wrapped with the "nex: init:" prefix.
func (m *Module) Init(c *core.Core) error {
	m.core = c

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("nex: init: resolving home directory: %w", err)
	}
	n := c.Cfg.Nex.Expanded(home)

	// Log the applied prefix and the original PATH's element count, not
	// the full PATH: on a developer machine that is kilobytes per line.
	pathElements := len(filepath.SplitList(os.Getenv("PATH")))
	_, changed := applyPathPolicy(n.PathPrepend, m.isDir)
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
		return fmt.Errorf("nex: init: %w", err)
	}
	m.opts = opts

	if err := os.MkdirAll(opts.Config.DataDir, 0o755); err != nil {
		return fmt.Errorf("nex: init: creating data_dir %s: %w", opts.Config.DataDir, err)
	}

	sys, err := m.assemble(context.Background(), opts)
	if err != nil {
		return fmt.Errorf("nex: init: assembling engine: %w", err)
	}
	m.sys = sys
	return nil
}

// RegisterRoutes mounts the engine's handler under RoutePrefix, stripping
// the prefix (the engine is told about it through Options.PublicPrefix so
// the URLs it emits stay correct) and containing per-request panics.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.Handle(RoutePrefix+"/", http.StripPrefix(RoutePrefix, recoverer(m.logf, m.sys.handler)))
}

// Start logs what the module is serving. The engine is already live after
// Init; there is nothing further to start.
func (m *Module) Start(context.Context) error {
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
// Stop and Close are no-ops when Init never assembled an engine (the
// daemon log.Fatals on an Init error, but a partially built Module must
// not panic if a caller still walks the lifecycle).
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
