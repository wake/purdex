// Command wwd is the per-host watcher/worker daemon (spec docs/specs/2026-10-18-wwd-spec.md).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/wwd/clones"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/fault"
	"github.com/wake/purdex/internal/wwd/gateheld"
	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/localruns"
	"github.com/wake/purdex/internal/wwd/manifestref"
	"github.com/wake/purdex/internal/wwd/orphans"
	"github.com/wake/purdex/internal/wwd/qualify"
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
	if handled, err := runAdvisorCmd(os.Args[1:]); handled {
		if err != nil {
			log.Fatalf("wwd: %v", err)
		}
		return
	}
	cfgPath, action, code := parseCLI(os.Args[1:], os.Stdout, os.Stderr)
	switch action {
	case cliExit:
		os.Exit(code)
	case cliVersion:
		fmt.Printf("wwd %s (%s)\n", buildinfo.Version, buildinfo.Hash)
		return
	}
	if err := run(cfgPath); err != nil {
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
		return fmt.Errorf("this host's alias %q is empty, a dot name (. or ..), over %d bytes or holds a control character; set alias in the config", alias, wire.MaxAliasBytes)
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
	// one stamp for every try of the start record, and for the clean-exit mark of this run
	startedAt := time.Now()
	// A stop on request (SIGTERM, SIGINT) leaves the mark that tells the next start this run did not crash (#3843); an error return does not.
	// The data directory is made first: the mark may be needed before wwd.db is open.
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)
	ctx, stop := watchStop(sigs, cfg.DataDir, startedAt, log.Printf)
	defer stop()
	// wwd.db: the host state here, the queue's tables beside it (queue.Migrate is
	// idempotent and runs on every start)
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
	var d *daemon.Daemon // the sampler's gate reads its pool, and a result is filed by it: it exists once the daemon does, before any job can end
	qopts := queueOptions(alias, cfg.DataDir, func(ev wire.JobEvent) {
		broker.PublishJob(ev)
		fastLane.OnJobEvent(ev)
		usage.OnJobEvent(ev)
	})
	qopts.OnSucceeded = func(ctx context.Context, j wire.Job, p queue.Produced) { // the result of a job a worker ended is filed for reuse (spec §4.5)
		if d != nil {
			d.FileResult(ctx, j, p)
		}
	}
	// how a job came out is recorded in the transaction that ends it, judged by the reference host of that moment (spec §7)
	qopts.ReferenceHost = func(repo, task string) string {
		if d == nil {
			return ""
		}
		return d.ReferenceHost(repo, task)
	}
	q, err := queue.New(st.DB(), qopts)
	if err != nil {
		return err
	}
	fastLane = localruns.New(tbl, localruns.FromQueue(q), alias)
	state, err := daemon.NewHostState(daemon.HostStateOptions{
		Store: st, Host: alias, Events: broker, OwnerRuleSupported: facts.OwnerReadable(),
		Initial: wire.WorkerSettings{Enabled: cfg.Worker.Enabled, Slots: cfg.Worker.Slots(), OwnerRule: ownerRule, DiskMinGB: cfg.Worker.DiskMinGB},
	})
	if err != nil {
		return err
	}
	// the owner is read whether or not the rule is enabled ([owner] enabled only sets the rule in force, above): the facts report the
	// reading and the rule decides whether it counts (spec §3.2, #3682). The idle limit follows the worker settings: PUT /v1/worker
	// changes it at run time.
	// the tool directories of [tools] extra_path: one resolved list for the facts' tool probe and for the jobs' PATH
	toolDirs := facts.ResolveExtraDirs(home, cfg.Tools.ExtraPath, log.Printf)
	srcOpt := sourceOptions(toolDirs, func() int {
		m, _ := daemon.ParseOwnerRule(state.Worker().OwnerRule)
		return m
	})
	// the observation records (spec 8.4): the sampler hands each process table to the observer, the routes read it back
	observer := daemon.NewObserver(alias, facts.ObserveConfig{
		MinCPUSeconds: cfg.Observe.MinCPUSeconds, MinMemMB: cfg.Observe.MinMemMB, Interval: daemon.SampleInterval,
	}, st)
	sources := facts.SystemSourcesWith(srcOpt)
	// the leaked dev processes and fork loops (spec §13.3 orphans): looked at from the same snapshot, listed only, never signalled
	orphanScan := orphans.New(orphans.Config{UID: os.Getuid()})
	sources.Observe = func(now time.Time, snap facts.ProcSnapshot, c facts.ProcContext) {
		observer.Observe(now, snap, c)
		usage.Observe(now, snap, c)
		orphanScan.Observe(now, snap, c)
	}
	hooks := fault.New(cfg.Fault, log.Printf) // nil unless [fault] enabled = true names a hook
	// H2 stands in for the keyboard: with owner_active_file named the real HID is not read (#3561)
	sources.Owner = facts.OverrideOwner(sources.Owner, hooks.OwnerBlock)
	// until WWD-6 a queue job is outside the gate's slots: the host's own gate is counted before a job is claimed (#3447) and a claimed job
	// holds one of its heavy slots (#3502); both go with WWD-6 / WWD-9
	hostGate := gateheld.New(gate.DefaultConfigPath(), log.Printf).WithWorkDir(workRoot) // the jobs' disk is the work root's
	// the runs on this host by slot: the worker's queue jobs (heavy) and the fast lane's runs (spec §5.2); the sampler shows them in
	// facts.worker.running and the fast lane's admission counts them
	queueRun := &queueRunning{}
	hostRunning := func() wire.Slots {
		l, o := fastLane.Running(), queueRun.Slots()
		return wire.Slots{Heavy: l.Heavy + o.Heavy, Light: l.Light + o.Light}
	}
	sampler := daemon.NewSampler(daemon.SamplerOptions{
		Running: hostRunning,
		HostID:  hostID, Alias: alias, OSVersion: facts.OSVersion(),
		Fault: hooks, Sources: sources, Slots: cfg.Worker.Slots(), WorkerEnabled: cfg.Worker.Enabled,
		OwnerRule: ownerRule, State: state, Minutes: st, WorkRoot: workRoot, // the disk floor is measured on the work root's volume
		// the proto 2 fields (decompress_rate, queue) are written only while every peer reports proto >= 2, an unreachable one by its last known info (spec §13.1)
		SignalsOpen:   func() bool { return d != nil && wire.PoolAtLeast(d.Pool().Peers(), 2) },
		Queued:        func() (int, error) { return q.CountQueued(ctx) },
		GateHeavyFull: hostGate.HeavyFull,
	})
	// a fast-lane run is admitted by the latest record, the live counts and the gate's last reading (all memory: the fast lane asks with its lock held)
	fastLane.Others = queueRun.Slots
	fastLane.Capacity = func() wire.Slots { return state.Worker().Slots }
	fastLane.Room = func(heavy bool, running wire.Slots) []string {
		f, ok := sampler.Latest()
		if !ok {
			return []string{"no_facts_yet"}
		}
		return derived.AdmitLocal(f, sampler.GateFull(), heavy, running)
	}
	peers := make([]daemon.PeerAddr, len(cfg.Peers))
	for i, p := range cfg.Peers {
		if p.Alias == alias {
			return fmt.Errorf("config: peer %q has this host's own alias", p.Alias)
		}
		peers[i] = daemon.PeerAddr{Alias: p.Alias, HostPort: p.HostPort()}
	}
	repoSource := clones.NewSource(cfg.Repos)
	// a clone with a manifest_ref is read at that ref, not in its working tree (#3658): fetched in the background, one commit for the
	// reference labels, the qualify task list and the pair's snapshot alike
	var mrefs *manifestref.Source
	if len(cfg.ManifestRefs) > 0 {
		mrefs = manifestref.New(repoSource, cfg.ManifestRefs, "")
		go mrefs.Run(ctx, manifestRefInterval)
	}
	refs := newRefTaskSource(repoSource)
	refs.mrefs = mrefs
	refs.ttl = 2 * time.Second // the daemon's labels and the drift watcher ask once per facts record each: one read of the manifests serves both
	// the qualification coordinator (spec §7): a person starts a pair with pdx wwd qualify (POST /v1/qualify), and every facts record asks for a
	// tick that starts the pairs the rules want (qualifyTrigger, on a goroutine of its own). It reads the host through d, which exists before any
	// request can arrive.
	qualifier := qualify.New(qualify.Config{
		Queue: q, Store: st,
		Source: &qualifySource{repos: repoSource, mrefs: mrefs, host: func() qualify.Host {
			l, fp := d.Labels()
			return qualify.Host{Alias: alias, WorkerOn: state.Worker().Enabled, Labels: l, Fingerprint: fp}
		}},
		Granted: func() { d.RefreshLabels() },
	})
	d = daemon.New(daemonOptions(daemonParts{
		HostID: hostID, Alias: alias, Token: tok, Cfg: cfg, Hooks: hooks, Sampler: sampler, Peers: peers, State: state,
		Broker: broker, Queue: q, FastLane: fastLane, Observer: observer, Store: st, Refs: refs, Qualifier: qualifier,
	}))
	qualifyTick := newQualifyTrigger()
	watcher := newDriftWatcher(alias, st, refs, d.RefreshLabels) // an earned label whose OS version or tool moved is taken away (spec §7)
	sampleSignal := make(chan struct{}, 1)                       // pressure danger is judged at every sample (S3-2): a mark only, the judging runs in the advisor's goroutine
	sampler.SetOnFacts(func(f wire.Facts) {
		select {
		case sampleSignal <- struct{}{}:
		default:
		}
		d.OnFacts(f)
		usage.OnFacts(f)
		_, fp := labels.FromFacts(f)
		watcher.OnFacts(fp)
		qualifyTick.Notify() // a mark only: the tick itself runs on its own goroutine, not under the locks this callback runs in
	})
	go sampler.Run(ctx)
	go d.Pool().Run(ctx)
	adv := advisorInputs{Alias: alias, WorkRoot: workRoot, Store: st, Broker: broker, Sampler: sampler, Known: cfg.Advisor.KnownResidents, Orphans: orphanScan.Report,
		Peers: cfg.Peers, Downs: d.Pool().Downs, Watch: cfg.Advisor, SampleSignal: sampleSignal}
	if cfg.Advisor.PartnerCheck {
		adv.PartnerSeen = d.PartnerSeen
	}
	go runAdvisor(ctx, adv)
	// The usage recorder and the worker (takes jobs from this host's queue and the peers', runs them, reports). At the end the worker is
	// stopped first, then the recorder writes what is left (it records the usage of the jobs the worker hands back), then st.Close (this
	// defer runs before it, which was deferred earlier), also when run() failed.
	svc := startServices(ctx, usage.Run, func(ctx context.Context) error {
		return runWorker(ctx, workerInputs{
			Cfg: cfg, Alias: alias, Token: tok, Queue: q, Locks: tbl, Store: qstore, WorkRoot: workRoot, Sandbox: sb, Home: home, ToolDirs: toolDirs,
			Settings: state.Worker, Latest: sampler.Latest, Labels: d.Labels, Peers: d.Pool().Peers, FastLaneReady: fastLane.Ready, Running: queueRun, ReserveHeavy: fastLane.ReserveHeavy,
			Usage: usage.ForWorker(), DB: newPerJobDB(cfg.JobDB), Hooks: hooks, Gate: gateTaker{hostGate},
		})
	})
	// What else makes a job's final event besides the worker: the HTTP servers (a peer's or the caller's result, release, cancel), the queue's
	// sweeper (a lapsed lease) and the fast lane. At the end they are quiet before the recorder is stopped (Stop's quiesce): the servers closed
	// with their requests answered (Running.Close waits for them, up to its own limit), the two goroutines returned.
	var producers sync.WaitGroup
	var httpRun *daemon.Running
	defer svc.Stop(stop, func() {
		if httpRun != nil {
			_ = httpRun.Close()
		}
		producers.Wait()
	})
	producers.Add(5)
	go func() { defer producers.Done(); sweepQueue(ctx, q) }()                            // lapsed leases, queue timeouts, retention
	go func() { defer producers.Done(); fastLane.Run(ctx) }()                             // rebuilds the locks of the runs that were going on, then compares with the queue every 30 s
	go func() { defer producers.Done(); d.RunQuarantine(ctx, daemon.QuarantinePeriod) }() // reads the disagreements that concern this host and quarantines its labels (spec §7)
	go func() { defer producers.Done(); qualifier.RunSettle(ctx, qualifySettlePeriod) }() // a pair ends here even when no facts record comes to tick
	go func() { defer producers.Done(); qualifyTick.Run(ctx, qualifier.Tick) }()          // the automatic trigger: settles, then starts the pairs the rules ask for
	run, err := d.Listen(ctx, daemon.ListenSpec{Addrs: addrs, Port: cfg.Port, SocketPath: cfg.SocketPath})
	if err != nil {
		return err
	}
	httpRun = run
	log.Printf("wwd %s listening on %v and %s", buildinfo.Version, run.TCPAddrs(), cfg.SocketPath)
	// One record per run, for the crash-loop rule (spec §13.1), written once the ports are bound: a start that could not bind (a second wwd,
	// `wwd` run by mistake) is not a start of the service (#3843). A failure to write it must not stop the host.
	recordThisStart(ctx, st, cfg.DataDir, startedAt, buildinfo.Version, startRetryDelays, log.Printf)
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

// manifestRefInterval is how often a clone with a manifest_ref is fetched and its manifest read again.
const manifestRefInterval = 10 * time.Minute

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
