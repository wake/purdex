package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/wwd/clones"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/fault"
	"github.com/wake/purdex/internal/wwd/queue"
	wrun "github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/jobexec"
	"github.com/wake/purdex/internal/wwd/worker/locks"
	"github.com/wake/purdex/internal/wwd/worker/loop"
	"github.com/wake/purdex/internal/wwd/worker/origin"
	"github.com/wake/purdex/internal/wwd/worker/pick"
	"github.com/wake/purdex/internal/wwd/worker/quarantine"
	"github.com/wake/purdex/internal/wwd/worker/report"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// errSandboxWithoutGuard: a host that runs commands as systemd units must have the runner's guard (the marker written before the command
// starts); without it a daemon that dies while a unit runs leaves nothing to keep the job's keys and workspace, and the next daemon
// would hand them out again. The worker does not start.
var errSandboxWithoutGuard = errors.New("worker: this host runs commands as sandbox units but the runner has no guard (quarantine store), the worker does not start")

// noUnits is the unit checker of a host with no sandbox: an entry there has no unit (Sweep never asks), and a human clears it.
type noUnits struct{}

func (noUnits) Gone(context.Context, string) (bool, error) {
	return false, errors.New("this host has no sandbox units")
}

// sandboxFor is the sandbox of this host's config, or nil (commands run as a process group of the daemon's own account).
func sandboxFor(c daemon.WorkerConfig) *wrun.Sandbox {
	if c.SandboxUser == "" {
		return nil
	}
	return &wrun.Sandbox{User: c.SandboxUser}
}

// openQuarantine makes the work root, resolves it to the real path the store and the job executor both need (a job directory is a
// direct child of it), and opens the quarantine store over the one lock table. Restore has not been called: the caller does that before
// anything else takes a lock.
func openQuarantine(cfg daemon.Config, tbl *locks.Table, sb *wrun.Sandbox) (store *quarantine.Store, workRoot string, err error) {
	if err := os.MkdirAll(cfg.WorkRoot, 0o700); err != nil {
		return nil, "", fmt.Errorf("worker: work root: %w", err)
	}
	workRoot, err = filepath.EvalSymlinks(cfg.WorkRoot)
	if err != nil {
		return nil, "", fmt.Errorf("worker: work root: %w", err)
	}
	var checker quarantine.UnitChecker = noUnits{}
	if sb != nil {
		checker = sb
	}
	store, err = quarantine.Open(filepath.Join(cfg.DataDir, "quarantine"), tbl, checker, time.Now, quarantine.WithWorkRoot(workRoot))
	if err != nil {
		return nil, "", err
	}
	return store, workRoot, nil
}

// workerInputs is what the worker takes from the rest of the daemon.
type workerInputs struct {
	Cfg      daemon.Config
	Alias    string
	Token    string
	Queue    *queue.Queue
	Locks    *locks.Table // the one table: the fast lane's runs and the worker's claims take their keys from it
	Store    *quarantine.Store
	WorkRoot string // the real path openQuarantine returned
	// Home and ToolDirs build the jobs' PATH (jobParentEnv); ToolDirs is the resolved [tools] extra_path, the same list the facts use.
	Home     string
	ToolDirs []string
	Sandbox  *wrun.Sandbox
	Settings func() wire.WorkerSettings
	Latest   func() (wire.Facts, bool)
	Labels   func() ([]string, map[string]string)
	Peers    func() []wire.Peer // the pool poller's view, for each origin's zone
	Usage    loop.UsageRecorder // nil: nothing is recorded
	DB       perJobDB           // nil: this host has no per-job database
	Hooks    *fault.Hooks
	Logf     func(string, ...any)
	// FastLaneReady says the fast lane has rebuilt the keys of its runs that were going on (localruns.Service.Ready). The worker takes
	// no job before that: it could otherwise be given a key a fast-lane run still holds. nil: no fast lane to wait for.
	FastLaneReady func() bool
	// ReserveHeavy books a heavy slot in the fast lane's ledger before a claim (localruns.Service.ReserveHeavy); nil: none.
	ReserveHeavy func() (func(), bool)
	// Running is told the worker's loop once it is built (nil: nothing reads it).
	Running *queueRunning
}

// jobParentEnv is the environment the jobs' allowed names are read from: the daemon's own, with PATH replaced by the tool list the
// facts' tool probe searches (facts.ToolPath, the configured extra directories in front). Built at run time, so a change of
// [tools] extra_path takes effect when wwd restarts, without touching the systemd unit or the LaunchAgent. It is the worker's jobs
// only: a fast-lane run keeps its caller's environment.
func jobParentEnv(environ []string, home string, extra []string) []string {
	path := ""
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PATH="+facts.ToolPath(home, path, extra))
}

// hostGuard is the runner's guard, or an error when this host needs one and cannot have it.
func hostGuard(store *quarantine.Store, sandboxed bool, logf func(string, ...any)) (runner.Guard, error) {
	var g runner.Guard
	if store != nil { // a nil *Store must not become a non-nil Marker
		g = loop.NewGuard(store, sandboxed, time.Now, logf)
	}
	if sandboxed && g == nil {
		return nil, errSandboxWithoutGuard
	}
	return g, nil
}

// localFetcher gets a job's snapshot from this host's own queue: the live claim names the repository and the commit.
type localFetcher struct {
	q     *queue.Queue
	hooks *fault.Hooks
}

func (f localFetcher) Fetch(ctx context.Context, job wire.Job, claimID, _ string, dir string) (snapshot.Workspace, error) {
	if err := f.hooks.SnapshotDelay(ctx); err != nil {
		return snapshot.Workspace{}, err
	}
	lc, err := f.q.LiveClaim(ctx, job.ID, claimID)
	if err != nil {
		return snapshot.Workspace{}, err
	}
	return snapshot.CheckoutLocal(ctx, lc.GitDir, lc.Commit, dir)
}

// peerFetcher gets a job's snapshot from a peer's wwd (the bundle or the tar of the claim).
type peerFetcher struct{ c *snapshot.Client }

func (f peerFetcher) Fetch(ctx context.Context, job wire.Job, claimID, clone, dir string) (snapshot.Workspace, error) {
	return snapshot.Materialize(ctx, snapshot.MaterializeIn{
		Client: f.c, JobID: job.ID, ClaimID: claimID, Commit: job.Snapshot.Commit, Base: job.Snapshot.Base, Clone: clone, Dir: dir,
	})
}

// peerLinks is everything the worker holds of one peer.
type peerLinks struct {
	alias  string
	origin loop.Origin
	fetch  jobexec.Fetcher
}

func peerBase(p daemon.PeerConfig) string { return "http://" + p.HostPort() }

// loopConfig builds the worker loop's configuration. It fails when the host cannot run the worker safely (errSandboxWithoutGuard).
func loopConfig(in workerInputs) (loop.Config, error) {
	logf := in.Logf
	if logf == nil {
		logf = log.Printf
	}
	sandboxed := in.Sandbox != nil
	guard, err := hostGuard(in.Store, sandboxed, logf)
	if err != nil {
		return loop.Config{}, err
	}
	hc := &http.Client{}
	local := origin.Local{Q: in.Queue}
	origins := []loop.Origin{{Host: in.Alias, Reader: pick.NewLocalSource(in.Alias, local), Conn: local}}
	fetchers := map[string]jobexec.Fetcher{in.Alias: localFetcher{q: in.Queue, hooks: in.Hooks}}
	for _, p := range in.Cfg.Peers {
		base := peerBase(p)
		sc := snapshot.NewClient(base, in.Token)
		sc.Delay = in.Hooks.SnapshotDelay // the C2 matrix' H4: a slow snapshot fetch
		alias := p.Alias
		origins = append(origins, loop.Origin{
			Host: alias, Reader: pick.NewPeerSource(alias, base, in.Token, hc), Conn: origin.NewPeer(base, in.Token, hc),
			Zone: func() wire.Zone { return peerZone(in.Peers, alias) },
		})
		fetchers[alias] = peerFetcher{c: sc}
	}
	var db jobexec.PerJobDB
	var sweepers []loop.Sweeper
	if in.DB != nil {
		db = in.DB
		sweepers = []loop.Sweeper{in.DB}
	}
	repos := clones.NewSource(in.Cfg.Repos)
	exec := jobexec.New(jobexec.Config{
		Host:     in.Alias,
		WorkRoot: in.WorkRoot,
		Fetchers: func(source string) jobexec.Fetcher {
			if f, ok := fetchers[source]; ok {
				return f
			}
			return nil
		},
		ClonePath:         repos.Path,
		BackgroundDev:     in.Cfg.Worker.BackgroundDev,
		Sandbox:           in.Sandbox,
		DB:                db,
		ParentEnv:         jobParentEnv(os.Environ(), in.Home, in.ToolDirs),
		HostEnv:           in.Cfg.Jobs.Env, // [jobs.env]: laid over only where a command of a job is run (#3933)
		DepsDir:           in.Cfg.DepsRoot,
		DepsKeep:          in.Cfg.Worker.DepsKeep,
		DepsStoreMaxMB:    in.Cfg.Worker.DepsStoreMaxMB,
		GoCacheMaxMB:      in.Cfg.Worker.GoCacheMaxMB,
		GoCachePruneHours: in.Cfg.Worker.GoCachePruneHours,
	})
	// the build caches are trimmed on the beat of the loop's sweepers (once in a quarter of an hour, in the background), whether or not the
	// worker takes jobs: they are on the disk either way (#3833)
	sweepers = append(sweepers, exec.GoCacheTrimmer())
	// the claim says what the host is at the time of the claim (in.Labels), not what it was when the worker started: that is before the first
	// facts record, with no tool labels and a fingerprint of the OS version only, and no result made under it could be filed for reuse
	ctl := runner.New(runner.Config{
		Worker: in.Alias, Facts: in.Labels, Locks: in.Locks, Exec: exec, Guard: guard,
	})
	return loop.Config{
		Reserve:  in.ReserveHeavy,
		Settings: in.Settings,
		Facts: func() *wire.Facts {
			f, ok := in.Latest()
			if !ok {
				return nil
			}
			return &f
		},
		Origins: func() []loop.Origin { return origins },
		Labels:  func() []string { l, _ := in.Labels(); return l },
		Locks:   in.Locks,
		Ctl:     ctl,
		Report: report.New(report.Config{Logf: logf, Digest: func() bool { // a summary carries the passed-test digest only while no host of the pool would reject it
			return in.Peers != nil && wire.PoolAtLeast(in.Peers(), wire.PassedDigestProto)
		}}),
		Warm:               func(j wire.Job) bool { _, ok := repos.Path(j.Repo.Name); return ok },
		SpeedFactor:        in.Cfg.Worker.SpeedFactor,
		Peers:              in.Peers,
		DeferPatience:      in.Cfg.Worker.DeferPatience(),
		ExplicitLabelsOnly: in.Cfg.Worker.ExplicitLabelsOnly,
		Usage:              in.Usage,
		Sweepers:           sweepers,
		Quarantine:         in.Store,
		Sandboxed:          sandboxed,
		Guarded:            guard != nil,
		Logf:               logf,
	}, nil
}

// peerZone is the zone of a peer's last facts, for origin_bonus; unknown when the pool has not reached it.
func peerZone(peers func() []wire.Peer, alias string) wire.Zone {
	if peers == nil {
		return ""
	}
	for _, p := range peers() {
		if p.Info.Alias == alias && p.Facts != nil {
			return p.Facts.Derived.PickZone()
		}
	}
	return ""
}

// waitUntil waits for ready to be true (looking every 200 ms) or for ctx to end; false when ctx ended first.
func waitUntil(ctx context.Context, ready func() bool) bool {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		if ready() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
}

// startable says the worker may be built now: the sampler has its first facts record (the runner claims with the host's labels and
// toolchain fingerprint, which come from the facts) and the fast lane has its keys back.
func startable(in workerInputs) bool {
	if _, ok := in.Latest(); !ok {
		return false
	}
	return in.FastLaneReady == nil || in.FastLaneReady()
}

// runWorker builds the worker once the host has facts and the fast lane has its keys, and runs it until ctx ends. It returns when the loop has stopped what it ran (the
// caller closes the queue and the database after that), and returns the error that kept the worker from starting.
func runWorker(ctx context.Context, in workerInputs) error {
	if !waitUntil(ctx, func() bool { return startable(in) }) {
		return nil
	}
	cfg, err := loopConfig(in)
	if err != nil {
		return err
	}
	l := loop.New(cfg)
	if in.Running != nil {
		in.Running.l.Store(l)
	}
	return l.Run(ctx)
}

// queueRunning is the queue jobs this host's worker is running, for facts.worker.running and the fast lane's admission: the sampler and
// the fast lane are built before the worker is, so they hold this and the worker is put in when it exists. A queue job takes a heavy
// slot (the loop claims only while its running count is below the heavy slots).
type queueRunning struct{ l atomic.Pointer[loop.Loop] }

// Waiting is what the worker last saw queued for it (loop.Waiting), nil before the worker is made: facts.worker.waiting.
func (r *queueRunning) Waiting() *wire.Waiting {
	if l := r.l.Load(); l != nil {
		return l.Waiting()
	}
	return nil
}

func (r *queueRunning) Slots() wire.Slots {
	if l := r.l.Load(); l != nil {
		return wire.Slots{Heavy: l.Running()}
	}
	return wire.Slots{}
}
