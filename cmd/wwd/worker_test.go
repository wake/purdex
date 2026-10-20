package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/localruns"
	"github.com/wake/purdex/internal/wwd/queue"
	wrun "github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/jobexec"
	"github.com/wake/purdex/internal/wwd/worker/locks"
	"github.com/wake/purdex/internal/wwd/worker/quarantine"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

type fakeDB struct{}

func (fakeDB) Create(context.Context, string) (map[string]string, error) { return nil, nil }
func (fakeDB) Drop(context.Context, string) error                        { return nil }
func (fakeDB) Sweep(context.Context, []string) error                     { return nil }

type fakeUsage struct{}

func (fakeUsage) RecordRun(runner.Ended) {}

func testCfg(t *testing.T) daemon.Config {
	t.Helper()
	d := t.TempDir()
	return daemon.Config{
		DataDir: filepath.Join(d, "data"), WorkRoot: filepath.Join(d, "work"),
		Peers: []daemon.PeerConfig{{Alias: "air26", Addr: "127.0.0.1:7871"}, {Alias: "mserv", Addr: "127.0.0.1:7872"}},
	}
}

func inputs(t *testing.T, sb *wrun.Sandbox, withStore bool) (workerInputs, *locks.Table) {
	t.Helper()
	cfg := testCfg(t)
	tbl := locks.New()
	in := workerInputs{
		Cfg: cfg, Alias: "mlab", Token: "tok", Locks: tbl, Sandbox: sb,
		Settings: func() wire.WorkerSettings { return wire.WorkerSettings{} },
		Latest:   func() (wire.Facts, bool) { return wire.Facts{}, false },
		Labels:   func() ([]string, map[string]string) { return []string{"arch:arm64"}, map[string]string{"go": "1"} },
		Logf:     func(string, ...any) {},
	}
	if withStore {
		store, root, err := openQuarantine(cfg, tbl, sb)
		if err != nil {
			t.Fatal(err)
		}
		in.Store, in.WorkRoot = store, root
	} else {
		in.WorkRoot = cfg.WorkRoot
	}
	return in, tbl
}

// The condition for the sandbox hosts: no worker without the guard. A host that runs commands as units and has no quarantine store (so no
// guard) must not start the worker, and it says why.
func TestASandboxedHostWithoutAGuardDoesNotStartTheWorker(t *testing.T) {
	in, _ := inputs(t, &wrun.Sandbox{User: "wwd-job"}, false)
	if _, err := loopConfig(in); !errors.Is(err, errSandboxWithoutGuard) {
		t.Fatalf("err = %v, want errSandboxWithoutGuard", err)
	}
	in.Latest = func() (wire.Facts, bool) { return wire.Facts{}, true }
	if err := runWorker(context.Background(), in); !errors.Is(err, errSandboxWithoutGuard) {
		t.Fatalf("runWorker err = %v: the worker must not run without the guard", err)
	}
}

func TestASandboxedHostWithTheStoreRunsGuardedAndSandboxed(t *testing.T) {
	in, _ := inputs(t, &wrun.Sandbox{User: "wwd-job"}, true)
	cfg, err := loopConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Guarded || !cfg.Sandboxed {
		t.Fatalf("Guarded %v Sandboxed %v: the loop must know the runner has the guard", cfg.Guarded, cfg.Sandboxed)
	}
}

// With no sandbox there is no guard (nothing could clear a marker), and the loop writes the marker after the run itself.
func TestAHostWithoutASandboxHasNoGuardAndTheLoopWritesTheMarker(t *testing.T) {
	in, _ := inputs(t, nil, true)
	cfg, err := loopConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Guarded || cfg.Sandboxed {
		t.Fatalf("Guarded %v Sandboxed %v", cfg.Guarded, cfg.Sandboxed)
	}
	if cfg.Quarantine == nil {
		t.Fatal("the quarantine store is still the loop's: Restore and the marker after a run need it")
	}
}

// A nil *quarantine.Store must not turn into a non-nil Marker (a guard over nothing).
func TestANilStoreMakesNoGuard(t *testing.T) {
	g, err := hostGuard((*quarantine.Store)(nil), false, nil)
	if err != nil || g != nil {
		t.Fatalf("guard %v err %v", g, err)
	}
}

func TestTheWorkerAndTheFastLaneShareOneLockTable(t *testing.T) {
	in, tbl := inputs(t, nil, true)
	cfg, err := loopConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Locks != tbl {
		t.Fatal("the loop's lock table is not the one the fast lane and the quarantine store use")
	}
	// the store puts its holds into that table
	if _, err := tbl.Acquire("mlab/j1", []string{"k"}, nil); err != nil {
		t.Fatal(err)
	}
	e := quarantine.Entry{Owner: "mlab/j1", JobID: "j1", Dir: filepath.Join(in.WorkRoot, "h-j1"), Keys: []string{"k"}}
	if err := in.Store.Add(e); err != nil {
		t.Fatal(err)
	}
	tbl2 := locks.New()
	store2, _, err := openQuarantine(in.Cfg, tbl2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store2.Restore(); err != nil {
		t.Fatal(err)
	}
	if owner, ok := tbl2.Holder("k"); !ok || owner != "mlab/j1" {
		t.Fatalf("a new process did not get the hold back: %q %v (the markers live in <data_dir>/quarantine)", owner, ok)
	}
}

func TestTheOriginsAreTheHostFirstAndThenThePeersInOrder(t *testing.T) {
	in, _ := inputs(t, nil, true)
	in.Peers = func() []wire.Peer {
		return []wire.Peer{{Info: wire.Info{Alias: "air26"}, Facts: &wire.Facts{Derived: wire.Derived{Zone: wire.ZoneDanger}}}}
	}
	cfg, err := loopConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	os := cfg.Origins()
	if len(os) != 3 || os[0].Host != "mlab" || os[1].Host != "air26" || os[2].Host != "mserv" {
		t.Fatalf("origins %+v", os)
	}
	if os[0].Zone != nil {
		t.Fatal("this host's own queue has no zone")
	}
	if z := os[1].Zone(); z != wire.ZoneDanger {
		t.Fatalf("air26 zone %q: it comes from the pool's last facts", z)
	}
	if z := os[2].Zone(); z != "" {
		t.Fatalf("mserv zone %q: never reached is unknown", z)
	}
}

func TestThePerJobDatabaseIsSweptOnlyWhenThereIsOne(t *testing.T) {
	in, _ := inputs(t, nil, true)
	cfg, err := loopConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	// the build caches' timed trim is always one of the sweepers (#3833); the per-job database is the other, when there is one
	if len(cfg.Sweepers) != 1 {
		t.Fatalf("sweepers %v on a host with no per-job database: only the build cache's trim", cfg.Sweepers)
	}
	if _, ok := cfg.Sweepers[0].(*jobexec.GoCacheTrimmer); !ok {
		t.Fatalf("sweeper %T: the build cache's trim", cfg.Sweepers[0])
	}
	in.DB = fakeDB{}
	cfg, err = loopConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sweepers) != 2 {
		t.Fatalf("sweepers %v: the per-job database is a sweeper of the loop, besides the build cache's trim", cfg.Sweepers)
	}
}

func TestTheUsageRecorderAndTheHostSettingsReachTheLoop(t *testing.T) {
	in, _ := inputs(t, nil, true)
	in.Usage = fakeUsage{}
	in.Cfg.Worker.SpeedFactor = 1.25
	cfg, err := loopConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Usage == nil || cfg.SpeedFactor != 1.25 {
		t.Fatalf("Usage %v SpeedFactor %v", cfg.Usage, cfg.SpeedFactor)
	}
	if cfg.Facts() != nil {
		t.Fatal("no facts yet: the loop takes nothing")
	}
	in.Latest = func() (wire.Facts, bool) { return wire.Facts{HostID: "h"}, true }
	cfg, _ = loopConfig(in)
	if f := cfg.Facts(); f == nil || f.HostID != "h" {
		t.Fatalf("facts %v", f)
	}
}

func TestSandboxForIsNilWithoutAUser(t *testing.T) {
	if sandboxFor(daemon.WorkerConfig{}) != nil {
		t.Fatal("no sandbox_user, no sandbox")
	}
	if sb := sandboxFor(daemon.WorkerConfig{SandboxUser: "wwd-job"}); sb == nil || sb.User != "wwd-job" {
		t.Fatalf("sandbox %+v", sb)
	}
}

func TestWaitUntilReturnsWhenReadyOrTheContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitUntil(ctx, func() bool { return false }) {
		t.Fatal("a context that has ended")
	}
	n := 0
	ok := waitUntil(context.Background(), func() bool { n++; return n > 2 })
	if !ok || n != 3 {
		t.Fatalf("ok %v after %d looks", ok, n)
	}
}

// The worker takes no job before the fast lane has its keys back, and none before the first facts.
func TestTheWorkerWaitsForTheFirstFactsAndTheFastLane(t *testing.T) {
	in, _ := inputs(t, nil, true)
	if startable(in) {
		t.Fatal("no facts yet")
	}
	in.Latest = func() (wire.Facts, bool) { return wire.Facts{}, true }
	if !startable(in) {
		t.Fatal("facts, and no fast lane to wait for")
	}
	ready := false
	in.FastLaneReady = func() bool { return ready }
	if startable(in) {
		t.Fatal("the fast lane has not rebuilt its keys: the worker could be given one a running fast-lane job holds")
	}
	ready = true
	if !startable(in) {
		t.Fatal("facts and the fast lane is ready")
	}
}

// LocalRuns and ClaimRoutes are set together: the claim routes on the socket are what the host's own worker claims with, and the fast
// lane's POST /v1/local-runs opens only together with them.
func TestTheDaemonOptionsSetLocalRunsAndTheClaimRoutesTogether(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := queue.Migrate(context.Background(), st.DB()); err != nil {
		t.Fatal(err)
	}
	q, err := queue.New(st.DB(), queue.Options{Origin: "mlab", LogDir: t.TempDir(), Publish: func(wire.JobEvent) {}})
	if err != nil {
		t.Fatal(err)
	}
	fl := localruns.New(locks.New(), localruns.FromQueue(q), "mlab")
	o := daemonOptions(daemonParts{Alias: "mlab", Queue: q, FastLane: fl, Store: st})
	if o.LocalRuns == nil || o.ClaimRoutes == nil || o.Queue == nil {
		t.Fatalf("LocalRuns %v ClaimRoutes %v Queue %v", o.LocalRuns, o.ClaimRoutes != nil, o.Queue)
	}
	mux := http.NewServeMux()
	o.ClaimRoutes(mux)
	for _, route := range []struct{ method, path string }{
		{"POST", "/v1/jobs/j1/claim"}, {"POST", "/v1/jobs/j1/start"}, {"POST", "/v1/jobs/j1/heartbeat"},
		{"POST", "/v1/jobs/j1/release"}, {"POST", "/v1/jobs/j1/result"}, {"PUT", "/v1/jobs/j1/log"},
	} {
		if _, pattern := mux.Handler(httptest.NewRequest(route.method, route.path, nil)); pattern == "" {
			t.Errorf("%s %s is not served on the socket", route.method, route.path)
		}
	}
}

func TestPeerZoneIsUnknownForAnUnreachedPeer(t *testing.T) {
	if z := peerZone(nil, "air26"); z != "" {
		t.Fatalf("zone %q", z)
	}
	peers := func() []wire.Peer { return []wire.Peer{{Info: wire.Info{Alias: "air26"}}} }
	if z := peerZone(peers, "air26"); z != "" {
		t.Fatalf("zone %q for a peer with no facts", z)
	}
}

// The markers are in <data_dir>/quarantine (not among the job directories, where the work root's own cleanups would find them), and the
// store knows the work root: a human's Clear removes the job directory of an entry.
func TestTheQuarantineStoreKeepsItsMarkersInTheDataDirAndKnowsTheWorkRoot(t *testing.T) {
	in, tbl := inputs(t, nil, true)
	dir := filepath.Join(in.WorkRoot, "h-j1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := tbl.Acquire("mlab/j1", []string{"k"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := in.Store.Add(quarantine.Entry{Owner: "mlab/j1", JobID: "j1", Dir: dir, Keys: []string{"k"}}); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(filepath.Join(in.Cfg.DataDir, "quarantine"))
	if err != nil || len(ents) != 1 {
		t.Fatalf("markers in <data_dir>/quarantine: %v %v", ents, err)
	}
	if err := in.Store.Clear("j1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the job directory stays after Clear (%v): the store was not given the work root", err)
	}
}
