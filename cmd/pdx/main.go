// cmd/pdx/main.go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/wake/purdex/cmd/pdx/plugin"
	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/codexbroker"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/locale"
	"github.com/wake/purdex/internal/module/agent"
	backupmod "github.com/wake/purdex/internal/module/backup"
	"github.com/wake/purdex/internal/module/dev"
	fsmod "github.com/wake/purdex/internal/module/fs"
	hostconfigmod "github.com/wake/purdex/internal/module/hostconfig"
	hosttransfermod "github.com/wake/purdex/internal/module/hosttransfer"
	"github.com/wake/purdex/internal/module/logs"
	"github.com/wake/purdex/internal/module/monitor"
	"github.com/wake/purdex/internal/module/nex"
	peersmod "github.com/wake/purdex/internal/module/peers"
	profilesmod "github.com/wake/purdex/internal/module/profiles"
	"github.com/wake/purdex/internal/module/session"
	teammod "github.com/wake/purdex/internal/module/team"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
	"github.com/wake/purdex/internal/tmuxenv"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: pdx <command> [flags]\n")
		fmt.Fprintf(os.Stderr, "Commands: serve, start, stop, status, statusline-proxy, hook, setup, token, peers, msg, lead, relay, nex, path, version\n")
		os.Exit(1)
	}

	// The embedded Claude Code plugin reaches the CC hook installer here, so
	// internal/ never imports cmd/ (the installer only sees an fs.FS).
	agentcc.PluginSource = plugin.Files()

	switch os.Args[1] {
	case "serve":
		if plan := runServe(os.Args[2:]); plan != nil {
			reexec(plan, syscall.Exec, log.Printf, os.Exit)
		}
	case "hook":
		runHook(os.Args[2:])
	case "setup":
		runSetup(os.Args[2:])
	case "token":
		runToken(os.Args[2:])
	case "start":
		runStart(os.Args[2:])
	case "stop":
		runStop(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "statusline-proxy":
		runStatuslineProxy(os.Args[2:])
	case "peers":
		runPeers(os.Args[2:])
	case "msg":
		runMsg(os.Args[2:])
	case "lead":
		runLead(os.Args[2:])
	case "relay":
		runRelay(os.Args[2:])
	case "nex":
		runNexMain(os.Args[2:])
	case "path":
		runPath(os.Args[2:])
	case "peer-proxy":
		os.Exit(runPeerProxy())
	case "version":
		runVersion(os.Args[2:], os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runServe(args []string) *reexecPlan {
	bootStart := time.Now() // monotonic; feeds the "startup: ready in" line
	defer func() {
		if r := recover(); r != nil {
			home, _ := os.UserHomeDir()
			logsDir := filepath.Join(home, ".config", "pdx", "logs")
			writeCrashLog(logsDir, r, debug.Stack())
			panic(r)
		}
	}()

	// Before locale.EnsureUTF8 / tmuxenv.Prepare / nex PATH policy mutate the
	// process env: a restart must re-exec the boot command, not the mutated one.
	boot, bootErr := captureReexecPlan(os.Executable, os.Args, os.Environ())

	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml (default: ~/.config/pdx/config.toml)")
	bindOverride := fs.String("bind", "", "override bind address")
	portOverride := fs.Int("port", 0, "override port")
	quick := fs.Bool("quick", false, "quick setup mode with pairing code")
	fs.Parse(args)

	// 0. Locale — tmux sanitises TAB out of -F output under a non-UTF-8
	// client locale, which breaks every tab-separated parser below, and
	// the tmux server we may spawn inherits this env. Must precede any
	// tmux exec (GetTmuxInstance, hook install).
	switch r := locale.EnsureUTF8(); r.Action {
	case locale.Set:
		log.Printf("locale: no UTF-8 locale in environment, exported LANG=%s", r.Value)
	case locale.Warned:
		log.Printf("locale: WARNING %s=%q is not UTF-8; tmux output parsing will break", r.Source, r.Value)
	}

	// 0b. tmux environment — the daemon execs `tmux` by bare name in ~35
	// places and must not inherit the socket of whatever pane it was started
	// from. Same constraint as the locale step: it must precede every tmux
	// exec, the earliest of which is reached through module init and the
	// first /api/info (config.GetTmuxInstance), not through config.Load.
	//
	// It also has to run before nex's PATH policy, which snapshots PATH at
	// Init and restores that snapshot on soft-fail: taking this slot means
	// the snapshot already contains our repair, so a nex failure cannot
	// undo it.
	tenv := tmuxenv.Prepare()
	switch tenv.Action {
	case tmuxenv.Appended:
		log.Printf("tmux: not on PATH, appended %s (using %s)", tenv.AddedDir, tenv.Resolved)
	case tmuxenv.NotFound:
		log.Printf("tmux: ERROR not found on PATH nor in %s — sessions, terminals and monitoring will fail until tmux is installed or PATH is fixed (see `pdx path`)", tenv.ProbedList())
	}
	if tenv.DroppedTMUX {
		log.Printf("tmux: started from inside a tmux pane; dropped inherited $TMUX so this daemon addresses the default socket, not that pane's server")
	}

	// 1. Load config
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *bindOverride != "" {
		cfg.Bind = *bindOverride
	}
	if *portOverride != 0 {
		cfg.Port = *portOverride
	}

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("data dir: %v", err)
	}

	// 1b. Ensure stable host ID
	resolvedCfgPath := *cfgPath
	if resolvedCfgPath == "" {
		resolvedCfgPath = filepath.Join(cfg.DataDir, "config.toml")
	}
	hostID, err := config.EnsureHostID(&cfg, resolvedCfgPath)
	if err != nil {
		log.Printf("host_id: %v (running without stable host ID)", err)
	} else {
		log.Printf("host_id: %s", hostID)
	}

	// Acquire PID file lock (for pdx start/stop/status). A held lock means
	// another daemon already owns this data_dir, so refuse to start rather
	// than run two daemons against the same SQLite files.
	pidPath := filepath.Join(cfg.DataDir, "pdx.pid")
	var pidFile *os.File
	if v, ok := os.LookupEnv(pidLockFDEnv); ok {
		// Unset either way: children (tmux, nex turns) must not see a stale fd number.
		os.Unsetenv(pidLockFDEnv)
		if f, err := adoptPidLock(v, pidPath, os.Getpid()); err != nil {
			log.Printf("pid lock: inherited fd not adopted (%v); acquiring", err)
		} else {
			log.Printf("pid lock: adopted from the previous image")
			pidFile = f
		}
	}
	if pidFile == nil {
		pidFile = mustAcquirePidLock(pidPath, os.Getpid(), log.Fatalf)
	}
	handingOff := false
	defer func() {
		if !handingOff {
			releasePidLock(pidFile, pidPath)
		}
	}()

	// Files of modules that no longer exist (sync.db, device_state.db + -wal/-shm; #1303).
	// After the PID lock, so only the daemon that owns this data_dir touches it.
	removeLegacyDataFiles(cfg.DataDir, log.Printf)

	// What the previous image's restart shutdown could not clean up (spec
	// D13); consumed here so /api/info reports it once, for this boot.
	lastShutdown, lsErr := takeLastShutdown(cfg.DataDir)
	if lsErr != nil {
		log.Printf("last shutdown: record problem (%v)", lsErr)
	}
	if lastShutdown != nil {
		log.Printf("last shutdown: %d error(s) recorded by the previous image", len(lastShutdown.Errors))
	}

	// Register token for crash log redaction
	if cfg.Token != "" {
		setRedactTokens([]string{cfg.Token})
	}

	// 2. Open MetaStore
	meta, err := store.OpenMeta(filepath.Join(cfg.DataDir, "meta.db"))
	if err != nil {
		log.Fatalf("meta store: %v", err)
	}
	defer meta.Close()

	// 2b. Open AgentEventStore
	agentEvents, err := store.OpenAgentEvent(filepath.Join(cfg.DataDir, "agent_events.db"))
	if err != nil {
		log.Fatalf("agent event store: %v", err)
	}
	defer agentEvents.Close()

	// 3. Create tmux executor
	tx := tmux.NewRealExecutor()

	// 4. Create Core with config + tmux
	c := core.New(core.CoreDeps{
		Config: &cfg,
		Tmux:   tx,
	})
	c.LastShutdown = lastShutdown

	// POST /api/daemon/restart → restartCh → serveAndWait runs the normal
	// shutdown; the re-exec happens in main once this function's defers
	// (stores, pid lock) have run. No boot plan → no hook → the endpoint
	// answers 503 rather than accepting a restart nothing can perform.
	restartCh := make(chan struct{}, 1)
	if bootErr != nil {
		log.Printf("restart: unavailable (%v)", bootErr)
	} else {
		c.SetRestartHook(func() {
			select {
			case restartCh <- struct{}{}:
			default:
			}
		})
	}

	// Set config path for persistence via PUT /api/config
	c.CfgPath = resolvedCfgPath

	// Phase 5a: Pairing initialization
	initPairing(c, &cfg, resolvedCfgPath, *quick)

	// 5. Add modules (order doesn't matter — topoSort handles dependencies)
	if err := registerServeModules(c, meta, agentEvents); err != nil {
		log.Fatalf("serve modules: %v", err)
	}
	if c.Cfg.Dev.Update {
		repoRoot := c.Cfg.Dev.RepoRoot
		if repoRoot == "" {
			wd, err := os.Getwd()
			if err != nil {
				log.Fatalf("dev module: cannot determine repo root: %v", err)
			}
			repoRoot = wd
		}
		c.AddModule(dev.New(repoRoot))
	}

	// 6. Init all modules
	initStart := time.Now()
	if err := c.InitModules(); err != nil {
		log.Fatalf("core init: %v", err)
	}
	initDur := time.Since(initStart)

	// 7. Create shared http.ServeMux and register routes
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	c.RegisterRoutes(mux)

	// Context for background goroutines (modules).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 8. Start modules (session resets stale modes, cc starts poller, agent registers snapshot)
	startStart := time.Now()
	if err := c.StartModules(ctx); err != nil {
		log.Fatalf("core start: %v", err)
	}
	startDur := time.Since(startStart)

	// 9. Apply middleware chain and start HTTP server
	// Health endpoint bypasses auth (used for connection testing).
	// It still needs CORS so cross-origin SPA requests succeed.
	// /api/peers gets its own chain (PeerAuth, no TokenAuth) so a
	// configured peer host's inbound token can authenticate.
	outerMux := newOuterHandler(c, mux, cfg.Allow)

	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: outerMux,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Print(startupReadyLine(time.Since(bootStart), initDur, startDur))
	log.Printf("pdx daemon listening on %s", addr)
	listener, err := listenWithReuseAddr(addr)
	if err != nil {
		log.Fatalf("bind %s: %v", addr, err)
	}
	// 10. Serve until a signal or a Serve failure, then run the full
	// shutdown sequence (cancel → StopModules → Shutdown/Close →
	// CloseModules) and only return once it has finished, so the deferred
	// store close and PID-lock release registered above run against closed
	// modules.
	err = serveAndWait(srv, listener, sigCh, restartCh, cancel, c, core.ShutdownBudget, log.Printf, os.Exit, withInflight(processInflight))
	if errors.Is(err, errRestart) {
		if restartStillWanted(sigCh, func() { signal.Stop(sigCh) }, log.Printf) {
			// The restart goes ahead despite cleanup errors; leave a record
			// so the new image can report them (spec D13).
			var rr *restartRequested
			if errors.As(err, &rr) {
				log.Print(shutdownDoneLine(rr.elapsed))
			}
			if rr != nil && len(rr.warnings) > 0 {
				if werr := writeLastShutdown(cfg.DataDir, rr.warnings, time.Now()); werr != nil {
					log.Printf("last shutdown: not recorded (%v)", werr)
				}
			}
			// Keep the pid lock held through the exec: the new image adopts it.
			// reexec clears close-on-exec right before the exec.
			handingOff = true
			boot.env = append(boot.env, pidLockEnvEntry(pidFile))
			boot.lock = pidFile // keeps the *os.File (and its fd) alive until exec
			return boot
		}
		return nil
	}
	if err != nil {
		log.Printf("server error: %v", err)
	}
	return nil
}

func registerServeModules(c *core.Core, meta *store.MetaStore, agentEvents *store.AgentEventStore) error {
	c.AddModule(session.NewSessionModule(meta))
	agentMod, err := agent.New(agentEvents)
	if err != nil {
		return err
	}
	c.AddModule(agentMod)
	// A nil meta store (tests) must stay a nil AuditStore, not a typed-nil
	// *PeerMessageStore inside the interface: peers treats nil as
	// "audit unavailable" and refuses every delivery.
	var audit peersmod.AuditStore
	var titles peersmod.TitleStore
	var titleMover teammod.TitleMover
	if meta != nil {
		audit = meta.PeerMessages()
		titles = meta.PeerLabels()
		titleMover = meta.PeerLabels()
	}
	peersMod := peersmod.New(audit, titles)
	if meta != nil {
		// Registry names of live sessions, kept for the conversation list's
		// title fallback after the session ends.
		peersMod.WithNameSink(meta.ConversationNames())
	}
	c.AddModule(peersMod)
	c.AddModule(fsmod.New())
	c.AddModule(logs.New())
	c.AddModule(profilesmod.New())
	c.AddModule(hostconfigmod.New())
	c.AddModule(hosttransfermod.New())
	c.AddModule(backupmod.New())
	c.AddModule(monitor.New())
	c.AddModule(codexbroker.New())
	// team depends on peers (the origin resolver) and hostconfig (the relay
	// switches); InitModules topo-sorts. The title mover is meta.db's
	// peer_labels (nil in tests: titles then stay on the old session id).
	c.AddModule(teammod.New().WithTitles(titleMover))

	c.CfgMu.RLock()
	nexEnabled := c.Cfg.Nex.Enabled
	c.CfgMu.RUnlock()
	if nexEnabled {
		nexMod := nex.New()
		// A nil meta store (tests) leaves the conversation listing off: its
		// Conversations() would dereference it.
		if meta != nil {
			nexMod.WithConversationIndex(meta.Conversations()).
				WithConversationNames(meta.ConversationNames())
		}
		c.AddModule(nexMod)
	} else {
		log.Printf("nex: disabled")
	}

	return nil
}
