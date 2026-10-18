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
	"time"

	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/fault"
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
	// The one lock table of this host: the fast lane's runs and the worker's claims take their keys from it. The quarantine store holds
	// the keys of the jobs whose commands were not proven stopped; their holds go back before anything can take a lock, and a host that
	// cannot put them all back does not start (a key another owner took would let a second job run beside the first one's leftovers).
	tbl := locks.New()
	sb := sandboxFor(cfg.Worker)
	qstore, workRoot, err := openQuarantine(cfg, tbl, sb)
	if err != nil {
		return err
	}
	if err := qstore.Restore(); err != nil {
		return fmt.Errorf("worker: the quarantined jobs' holds could not all be put back, wwd does not start: %w", err)
	}
	// One queue per process. Its job events go to the event stream and to the fast lane, which releases a finished run's keys.
	// The lock table is the one every claim on this host takes its keys from.
	var fastLane *localruns.Service
	// what each job that ran here used, from the samples and the job's final event (spec 8.5); recording only
	usage := daemon.NewUsageRecorder(alias, facts.NewJobUsageTracker(time.Now(), daemon.SampleInterval), st)
	q, err := queue.New(st.DB(), queueOptions(alias, cfg.DataDir, func(ev wire.JobEvent) {
		broker.PublishJob(ev)
		fastLane.OnJobEvent(ev)
		usage.OnJobEvent(ev)
	}))
	if err != nil {
		return err
	}
	fastLane = localruns.New(tbl, localruns.FromQueue(q), alias)
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
	sources.Observe = func(now time.Time, snap facts.ProcSnapshot, c facts.ProcContext) {
		observer.Observe(now, snap, c)
		usage.Observe(now, snap, c)
	}
	hooks := fault.New(cfg.Fault, log.Printf) // nil unless [fault] enabled = true names a hook
	sampler := daemon.NewSampler(daemon.SamplerOptions{
		HostID: hostID, Alias: alias, OSVersion: facts.OSVersion(),
		Fault: hooks, Sources: sources, Slots: cfg.Worker.Slots(), WorkerEnabled: cfg.Worker.Enabled,
		OwnerRule: ownerRule, State: state, Minutes: st,
	})
	peers := make([]daemon.PeerAddr, len(cfg.Peers))
	for i, p := range cfg.Peers {
		if p.Alias == alias {
			return fmt.Errorf("config: peer %q has this host's own alias", p.Alias)
		}
		peers[i] = daemon.PeerAddr{Alias: p.Alias, HostPort: p.HostPort()}
	}
	d := daemon.New(daemonOptions(daemonParts{
		HostID: hostID, Alias: alias, Token: tok, Cfg: cfg, Hooks: hooks, Sampler: sampler, Peers: peers, State: state,
		Broker: broker, Queue: q, FastLane: fastLane, Observer: observer, Store: st,
	}))
	sampler.SetOnFacts(func(f wire.Facts) {
		d.OnFacts(f)
		usage.OnFacts(f)
	})
	go sampler.Run(ctx)
	go d.Pool().Run(ctx)
	// The usage recorder and the worker (takes jobs from this host's queue and the peers', runs them, reports). At the end the worker is
	// stopped first, then the recorder writes what is left (it records the usage of the jobs the worker hands back), then st.Close (this
	// defer runs before it, which was deferred earlier), also when run() failed.
	svc := startServices(ctx, usage.Run, func(ctx context.Context) error {
		return runWorker(ctx, workerInputs{
			Cfg: cfg, Alias: alias, Token: tok, Queue: q, Locks: tbl, Store: qstore, WorkRoot: workRoot, Sandbox: sb,
			Settings: state.Worker, Latest: sampler.Latest, Labels: d.Labels, Peers: d.Pool().Peers, FastLaneReady: fastLane.Ready,
			Usage: usage.ForWorker(), DB: newPerJobDB(cfg.JobDB), Hooks: hooks,
		})
	})
	defer svc.Stop(stop)
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
	case err := <-svc.Err():
		return err // the worker could not start: wwd does not run half-way
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
