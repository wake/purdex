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
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/wwd/clones"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/jobsapi"
	"github.com/wake/purdex/internal/wwd/localruns"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
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
	if !wire.ValidAlias(alias) { // the one rule the pool routes also apply
		return fmt.Errorf("this host's alias %q is empty, over %d bytes or holds a control character; set alias in the config", alias, wire.MaxAliasBytes)
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
	// One queue per process. Its job events go to the event stream and to the fast lane, which releases a finished run's keys.
	// The lock table is the one every claim on this host takes its keys from.
	var fastLane *localruns.Service
	q, err := queue.New(st.DB(), queue.Options{
		Origin: alias, LogDir: filepath.Join(cfg.DataDir, "joblogs"),
		Publish: func(ev wire.JobEvent) {
			broker.PublishJob(ev)
			fastLane.OnJobEvent(ev)
		},
	})
	if err != nil {
		return err
	}
	fastLane = localruns.New(locks.New(), localruns.FromQueue(q), alias)
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
	// the observation records (spec 8.4): the sampler hands each process table to the observer, the routes read it back
	observer := daemon.NewObserver(alias, facts.ObserveConfig{
		MinCPUSeconds: cfg.Observe.MinCPUSeconds, MinMemMB: cfg.Observe.MinMemMB, Interval: daemon.SampleInterval,
	}, st)
	sources := facts.SystemSourcesWith(srcOpt)
	sources.Observe = observer.Observe
	sampler := daemon.NewSampler(daemon.SamplerOptions{
		HostID: hostID, Alias: alias, OSVersion: facts.OSVersion(),
		Sources: sources, Slots: cfg.Worker.Slots(), WorkerEnabled: cfg.Worker.Enabled,
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
		State: state, Broker: broker, Queue: q, LocalRuns: fastLane, RepoSource: clones.NewSource(cfg.Repos).Names,
		// the worker routes on the socket: the host's own worker and the fast-lane runners claim, heartbeat and report with them, and
		// POST /v1/local-runs opens only together with them
		ClaimRoutes: func(mux *http.ServeMux) { jobsapi.New(q).WorkerRoutes(mux, false) },
		Observer:    observer,
	})
	sampler.SetOnFacts(d.OnFacts)
	go sampler.Run(ctx)
	go d.Pool().Run(ctx)
	go sweepQueue(ctx, q) // lapsed leases, queue timeouts, retention
	go fastLane.Run(ctx)  // rebuilds the locks of the runs that were going on, then compares with the queue every 30 s
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

// sweepEvery is how often the queue's housekeeping runs (queue.Sweep is built for a few seconds).
const sweepEvery = 5 * time.Second

// sweepQueue runs the queue's periodic housekeeping until ctx ends; without it a run whose caller died would stay running.
func sweepQueue(ctx context.Context, q *queue.Queue) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := q.Sweep(ctx); err != nil && ctx.Err() == nil {
				log.Printf("wwd: queue sweep: %v", err)
			}
		}
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
