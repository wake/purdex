// cmd/tbox/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/middleware"
	"github.com/wake/tmux-box/internal/module/agent"
	"github.com/wake/tmux-box/internal/module/cc"
	"github.com/wake/tmux-box/internal/module/dev"
	"github.com/wake/tmux-box/internal/module/session"
	"github.com/wake/tmux-box/internal/module/stream"
	"github.com/wake/tmux-box/internal/relay"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: tbox <command> [flags]\n")
		fmt.Fprintf(os.Stderr, "Commands: serve, relay, hook, setup, token\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		runServe(os.Args[2:])
	case "relay":
		runRelay(os.Args[2:])
	case "hook":
		runHook(os.Args[2:])
	case "setup":
		runSetup(os.Args[2:])
	case "token":
		runToken(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml (default: ~/.config/tbox/config.toml)")
	bindOverride := fs.String("bind", "", "override bind address")
	portOverride := fs.Int("port", 0, "override port")
	fs.Parse(args)

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

	// Set config path for persistence via PUT /api/config
	resolvedCfgPath := *cfgPath
	if resolvedCfgPath == "" {
		resolvedCfgPath = filepath.Join(cfg.DataDir, "config.toml")
	}
	c.CfgPath = resolvedCfgPath

	// 5. Add modules (order doesn't matter — topoSort handles dependencies)
	c.AddModule(session.NewSessionModule(meta))
	c.AddModule(cc.New())
	c.AddModule(stream.New())
	c.AddModule(agent.New(agentEvents))
	if c.Cfg.Dev.Update {
		wd, _ := os.Getwd()
		c.AddModule(dev.New(wd))
	}

	// 6. Init all modules
	if err := c.InitModules(); err != nil {
		log.Fatalf("core init: %v", err)
	}

	// 7. Create shared http.ServeMux and register routes
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	c.RegisterRoutes(mux)

	// Context for background goroutines (modules).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 8. Start modules (session resets stale modes, cc starts poller, agent registers snapshot)
	if err := c.StartModules(ctx); err != nil {
		log.Fatalf("core start: %v", err)
	}

	// 9. Apply middleware chain and start HTTP server
	// Health endpoint bypasses auth (used for connection testing).
	// It still needs CORS so cross-origin SPA requests succeed.
	outerMux := http.NewServeMux()
	outerMux.Handle("GET /api/health", middleware.CORS(
		http.HandlerFunc(c.HandleHealth)))
	outerMux.Handle("/", middleware.CORS(
		middleware.IPWhitelist(cfg.Allow)(
			middleware.TokenAuth(cfg.Token, c.Tickets)(mux))))

	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: outerMux,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nshutting down...")
		cancel() // stop status poller + modules
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := c.StopModules(shutdownCtx); err != nil {
			log.Printf("stop modules: %v", err)
		}
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("tbox daemon listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("server error: %v", err)
	}
}

func runRelay(args []string) {
	fs := flag.NewFlagSet("relay", flag.ExitOnError)
	session := fs.String("session", "", "session code (required)")
	daemon := fs.String("daemon", "ws://127.0.0.1:7860", "daemon WebSocket address")
	tokenFile := fs.String("token-file", "", "path to file containing auth token (read and deleted)")
	fs.Parse(args)

	if *session == "" {
		fmt.Fprintln(os.Stderr, "relay: --session is required")
		os.Exit(1)
	}

	cmdArgs := fs.Args()
	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr, "relay: no command specified after flags")
		os.Exit(1)
	}

	token := os.Getenv("TBOX_TOKEN")
	wsURL := fmt.Sprintf("%s/ws/cli-bridge/%s", *daemon, *session)

	r := &relay.Relay{
		SessionCode: *session,
		DaemonURL:   wsURL,
		Token:       token,
		TokenFile:   *tokenFile,
		Command:     cmdArgs,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := r.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "relay: %v\n", err)
		os.Exit(1)
	}
}
