// Command wwd is the per-host watcher/worker daemon (spec docs/specs/2026-10-18-wwd-spec.md).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

func main() {
	if handled, err := runInstallCmd(os.Args[1:]); handled {
		if err != nil {
			log.Fatalf("wwd: %v", err)
		}
		return
	}
	cfgPath := flag.String("config", "", "config file (default ~/.config/wwd/config.toml)")
	version := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *version {
		fmt.Printf("wwd %s (%s)\n", buildinfo.Version, buildinfo.Hash)
		return
	}
	if err := run(*cfgPath); err != nil {
		log.Fatalf("wwd: %v", err)
	}
}

func run(cfgPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if cfgPath == "" {
		cfgPath = daemon.DefaultConfigPath(home)
	}
	cfg, err := daemon.LoadConfig(cfgPath, home)
	if err != nil {
		return err
	}
	tok, err := daemon.EnsureToken(cfg.TokenPath)
	if err != nil {
		return err
	}
	hostID, err := ensureHostID(cfg.DataDir)
	if err != nil {
		return err
	}
	alias := cfg.Alias
	if alias == "" {
		h, _ := os.Hostname()
		alias, _, _ = strings.Cut(h, ".")
	}
	ifaceAddrs, _ := net.InterfaceAddrs()
	addrs, err := daemon.PickBindAddrs(cfg.TailnetAddr, ifaceAddrs)
	if err != nil {
		return err
	}
	ownerRule := ""
	if cfg.Owner.Enabled {
		mins := cfg.Owner.IdleMinutes
		if mins <= 0 {
			mins = 5 // the rule's default (facts.OwnerConfig)
		}
		ownerRule = fmt.Sprintf("hid<%dm", mins)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// wwd.db: the host state here, the queue's tables beside it (queue.Migrate is
	// idempotent and runs on every start)
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "wwd.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	if err := queue.Migrate(ctx, st.DB()); err != nil {
		return err
	}
	broker := daemon.NewBroker()
	state, err := daemon.NewHostState(daemon.HostStateOptions{
		Store: st, Host: alias, Events: broker, OwnerRuleSupported: cfg.Owner.Enabled,
		Initial: wire.WorkerSettings{Enabled: cfg.Worker.Enabled, Slots: cfg.Worker.Slots(), OwnerRule: ownerRule},
	})
	if err != nil {
		return err
	}
	// the owner rule follows the worker settings: PUT /v1/worker changes the idle limit at run time
	var srcOpt facts.SourceOptions
	if cfg.Owner.Enabled {
		srcOpt.Owner = &facts.OwnerConfig{}
		srcOpt.OwnerMinutes = func() int {
			m, _ := daemon.ParseOwnerRule(state.Worker().OwnerRule)
			return m
		}
	}
	sampler := daemon.NewSampler(daemon.SamplerOptions{
		HostID: hostID, Alias: alias, OSVersion: facts.OSVersion(),
		Sources: facts.SystemSourcesWith(srcOpt), Slots: cfg.Worker.Slots(), WorkerEnabled: cfg.Worker.Enabled,
		OwnerRule: ownerRule, State: state, Minutes: st,
	})
	peers := make([]daemon.PeerAddr, len(cfg.Peers))
	for i, p := range cfg.Peers {
		if p.Alias == alias {
			return fmt.Errorf("config: peer %q has this host's own alias", p.Alias)
		}
		peers[i] = daemon.PeerAddr{Alias: p.Alias, HostPort: p.HostPort()}
	}
	d := daemon.New(daemon.Options{
		HostID: hostID, Alias: alias, Version: buildinfo.Version,
		Token: tok, WorkerEnabled: cfg.Worker.Enabled, Sampler: sampler, Peers: peers,
		State: state, Broker: broker,
	})
	sampler.SetOnFacts(d.Events().PublishFacts)
	go sampler.Run(ctx)
	go d.Pool().Run(ctx)
	run, err := d.Listen(ctx, daemon.ListenSpec{Addrs: addrs, Port: cfg.Port, SocketPath: cfg.SocketPath})
	if err != nil {
		return err
	}
	log.Printf("wwd %s listening on %v and %s", buildinfo.Version, run.TCPAddrs(), cfg.SocketPath)
	select {
	case <-ctx.Done():
		return nil
	case err := <-run.Err():
		return err
	}
}

// ensureHostID returns a stable random id persisted under the data dir.
func ensureHostID(dataDir string) (string, error) {
	p := filepath.Join(dataDir, "host_id")
	if b, err := os.ReadFile(p); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id, nil
		}
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	return id, os.WriteFile(p, []byte(id+"\n"), 0o600)
}
