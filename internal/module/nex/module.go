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
	"time"

	"lab.protype.tw/wake/nexen"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/probe"
	pdxconfig "github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// engine is the private seam over *nexen.System: what the module needs
// from an assembled Nexen — serve, drain, release, and (since P-C.3a) the
// embedded Service and Store the handoff endpoints call directly (spec
// §4.4). Tests in this package substitute a fake engine through
// assembleFn; production goes through realAssemble.
type engine struct {
	handler  http.Handler
	shutdown func(context.Context) error
	close    func() error
	service  nexService // nil on a fake engine that never delegates
	store    nexStore   // nil on a fake engine that never reads rows
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
		service:  sys.Service,
		store:    sys.Store,
	}, nil
}

// proberKey is where the agent module registers its *probe.Prober; the
// agent package exports no constant for it.
const proberKey = "agent.prober"

// livenessProber is the slice of *probe.Prober the handoff needs.
type livenessProber interface {
	IsAliveFor(agentType, target string) bool
	CheckReadiness(agentType, target string) (probe.ReadinessResult, bool)
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

	// Hand-to-nex / take-back (spec §4.4) orchestrate a tmux pane's Claude
	// Code around the embedded engine, so the module needs the session and
	// agent modules' services. Init resolves them from the registry.
	sessions session.SessionProvider
	owners   agent.OwnerResolver
	prober   livenessProber
	ccOps    agentcc.CCOperator
	tmux     tmux.Executor
	locks    *session.HandoffLocks // per-session-code; same type the stream relay uses

	// Handoff timing (handoff.go); zero values take the defaults in Init.
	handoffResolveTimeout   time.Duration // ResolveSessionOwner
	handoffInterruptTimeout time.Duration // CCOperator.Interrupt
	handoffExitTimeout      time.Duration // CCOperator.Exit
	rollbackWait            time.Duration // wait for CC after a rollback resume
	rollbackPoll            time.Duration // liveness poll interval during that wait

	// Engine call budgets (handoff.go, takeback.go): every Service/Store
	// call runs under a context detached from the request's (a client that
	// disconnects must not cancel it) but bounded by one of these (an
	// engine that never answers must not hold the per-session lock).
	delegateTimeout        time.Duration // Service.Delegate
	engineOpTimeout        time.Duration // Store.Get, AcquireLease, Archive
	engineInterruptTimeout time.Duration // Service.Interrupt; > Nexen's own interruptTimeout so its verdict wins
	leaseCleanupTimeout    time.Duration // ReleaseLease, under its own fresh context

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

// Dependencies orders nex after session and agent: Init reads their
// registry entries, and the core inits modules in dependency order.
func (m *Module) Dependencies() []string { return []string{"session", "agent"} }

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
// Every failure is wrapped with the "nex: init:" prefix. Only a missing
// session/agent service and the initial Validate error are fatal (module
// wiring and static config shape errors — config.Load should already have
// rejected the latter). Every later failure (buildOptions, creating
// data_dir, assembling the engine) is a soft-fail (spec §4.4.1, I8): Init
// still returns nil, m.initErr records why, and RegisterRoutes / Status
// surface it — a broken [nex] must not take the terminal daemon down.
func (m *Module) Init(c *core.Core) error {
	m.core = c

	// Providers first, before anything with a side effect (the PATH policy,
	// data_dir, the engine): a missing one is a hard error and must never
	// leave a half-assembled engine or a policed PATH behind.
	if err := m.resolveProviders(c); err != nil {
		return fmt.Errorf("nex: init: %w", err)
	}
	m.tmux = c.Tmux
	m.applyHandoffDefaults()

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

// resolveProviders looks up the session and agent services the handoff
// endpoints need. Hard, not soft: a broken [nex] soft-fails so the
// terminal daemon stays up, but an absent provider means the daemon's
// module wiring is wrong — the engine's own /api/nex would still serve
// while every handoff silently could not, so refuse to start instead.
func (m *Module) resolveProviders(c *core.Core) error {
	svc, ok := c.Registry.Get(session.RegistryKey)
	if !ok {
		return fmt.Errorf("service %q not registered", session.RegistryKey)
	}
	if m.sessions, ok = svc.(session.SessionProvider); !ok {
		return fmt.Errorf("service %q does not implement session.SessionProvider (%T)", session.RegistryKey, svc)
	}

	// The daemon's one handoff lock instance (session module's). It is
	// registry-owned rather than private so a second handoff path (there
	// was one until P-D.2) could never run on the same session at once.
	if svc, ok = c.Registry.Get(session.HandoffLocksKey); !ok {
		return fmt.Errorf("service %q not registered", session.HandoffLocksKey)
	}
	if m.locks, ok = svc.(*session.HandoffLocks); !ok {
		return fmt.Errorf("service %q is not a *session.HandoffLocks (%T)", session.HandoffLocksKey, svc)
	}

	if svc, ok = c.Registry.Get(agent.OwnerResolverKey); !ok {
		return fmt.Errorf("service %q not registered", agent.OwnerResolverKey)
	}
	if m.owners, ok = svc.(agent.OwnerResolver); !ok {
		return fmt.Errorf("service %q does not implement agent.OwnerResolver (%T)", agent.OwnerResolverKey, svc)
	}

	if svc, ok = c.Registry.Get(proberKey); !ok {
		return fmt.Errorf("service %q not registered", proberKey)
	}
	if m.prober, ok = svc.(livenessProber); !ok {
		return fmt.Errorf("service %q does not implement livenessProber (%T)", proberKey, svc)
	}

	if svc, ok = c.Registry.Get(agentcc.OperatorKey); !ok {
		return fmt.Errorf("service %q not registered", agentcc.OperatorKey)
	}
	if m.ccOps, ok = svc.(agentcc.CCOperator); !ok {
		return fmt.Errorf("service %q does not implement cc.CCOperator (%T)", agentcc.OperatorKey, svc)
	}
	return nil
}

// principal names the caller the way the engine's own API would
// (principalAuth in build_config.go): the handoff endpoints must act as
// the principal /api/nex would derive for the same request, or the lease
// they take is not the one the SPA later holds.
func (m *Module) principal(r *http.Request) (string, error) {
	return m.opts.Auth.Authenticate(r)
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
//
// The handoff and take-back endpoints live under /api/sessions, not
// RoutePrefix (they orchestrate a tmux pane, the engine is only one step),
// and are mounted whether or not the engine assembled: the handlers
// themselves answer 503 nex_unavailable, so a client sees the structured
// error rather than a 404 it would read as "old daemon".
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions/{code}/nex-handoff", m.handleNexHandoff)
	mux.HandleFunc("POST /api/sessions/{code}/nex-takeback", m.handleNexTakeback)
	// Under RoutePrefix but purdex orchestration, not an engine route: a
	// more specific pattern than RoutePrefix+"/", so it wins either way.
	mux.HandleFunc("POST "+RoutePrefix+"/executions/{id}/take-to-terminal", m.handleTakeToTerminal)
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
