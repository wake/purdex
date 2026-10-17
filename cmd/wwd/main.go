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
)

func main() {
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
	var srcOpt facts.SourceOptions
	if cfg.Owner.Enabled {
		srcOpt.Owner = &facts.OwnerConfig{IdleMinutes: cfg.Owner.IdleMinutes}
	}
	sampler := daemon.NewSampler(daemon.SamplerOptions{
		HostID: hostID, Alias: alias, OSVersion: facts.OSVersion(),
		Sources: facts.SystemSourcesWith(srcOpt), Slots: cfg.Worker.Slots(), WorkerEnabled: cfg.Worker.Enabled,
	})
	d := daemon.New(daemon.Options{
		HostID: hostID, Alias: alias, Version: buildinfo.Version,
		Token: tok, WorkerEnabled: cfg.Worker.Enabled, Sampler: sampler,
	})
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go sampler.Run(ctx)
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
