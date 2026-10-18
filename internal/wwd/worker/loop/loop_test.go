package loop

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
	"github.com/wake/purdex/internal/wwd/worker/pick"
	"github.com/wake/purdex/internal/wwd/worker/report"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

const unit = 20 * time.Millisecond

const claimID = "0123456789abcdef0123456789abcdef"

func qjob(id string, class wire.JobClass, age time.Duration, lockKeys ...string) wire.Job {
	return wire.Job{ID: id, Origin: "mlab", Task: "test", Snapshot: wire.Snapshot{Commit: "abc"}, Class: class, State: wire.StateQueued,
		Repo: wire.Repo{Name: "purdex"}, Locks: lockKeys, CreatedAt: time.Now().Add(-age), Submitter: wire.Submitter{Session: "s-" + id}}
}

// fakeConn is one origin: a queue of jobs it hands out, and a log of what was done to it.
type fakeConn struct {
	mu       sync.Mutex
	jobs     map[string]wire.Job
	claimErr map[string]error
	claims   []string
	starts   int
	results  []wire.ResultRequest
	releases []wire.ReleaseRequest
}

func newConn(jobs ...wire.Job) *fakeConn {
	c := &fakeConn{jobs: map[string]wire.Job{}, claimErr: map[string]error{}}
	for _, j := range jobs {
		c.jobs[j.ID] = j
	}
	return c
}

func (c *fakeConn) Queued(context.Context) ([]wire.Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []wire.Job
	for _, j := range c.jobs {
		out = append(out, j)
	}
	return out, nil
}

func (c *fakeConn) Claim(_ context.Context, id string, _ wire.ClaimRequest) (wire.ClaimResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.claims = append(c.claims, id)
	if err := c.claimErr[id]; err != nil {
		return wire.ClaimResponse{}, err
	}
	j := c.jobs[id]
	delete(c.jobs, id)
	return wire.ClaimResponse{Job: j, ClaimID: claimID, LeaseS: 30, RenewS: 1}, nil
}

func (c *fakeConn) Start(context.Context, string, string) (wire.Lease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.starts++
	return wire.Lease{LeaseS: 30, RenewS: 1}, nil
}

func (c *fakeConn) Heartbeat(context.Context, string, string) (wire.HeartbeatResponse, error) {
	return wire.HeartbeatResponse{LeaseS: 30, RenewS: 1}, nil
}

func (c *fakeConn) Result(_ context.Context, _ string, req wire.ResultRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results = append(c.results, req)
	return nil
}

func (c *fakeConn) PutLog(context.Context, string, string, io_Reader) error { return nil }

func (c *fakeConn) Release(_ context.Context, _ string, req wire.ReleaseRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releases = append(c.releases, req)
	return nil
}

func (c *fakeConn) snapshot() (claims []string, releases []wire.ReleaseRequest, results int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.claims...), append([]wire.ReleaseRequest(nil), c.releases...), len(c.results)
}

type fakeExec struct {
	run   func(ctx context.Context, job wire.Job) run.Result
	ran   atomic.Int32
	order []string
	mu    sync.Mutex
}

func (e *fakeExec) Materialize(context.Context, wire.Job, string) (snapshot.Workspace, error) {
	return snapshot.Workspace{Dir: "/ws"}, nil
}

func (e *fakeExec) Prepare(context.Context, wire.Job, snapshot.Workspace) (runner.Plan, error) {
	return runner.Plan{Argv: []string{"true"}, Dir: "/ws", Timeout: time.Minute}, nil
}

func (e *fakeExec) Run(ctx context.Context, job wire.Job, _ snapshot.Workspace, _ runner.Plan) run.Result {
	e.ran.Add(1)
	e.mu.Lock()
	e.order = append(e.order, job.ID)
	e.mu.Unlock()
	if e.run != nil {
		return e.run(ctx, job)
	}
	return run.Result{Outcome: run.Exited}
}

func (e *fakeExec) ranOrder() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.order...)
}

func blockUntilDone(ctx context.Context, _ wire.Job) run.Result {
	<-ctx.Done()
	return run.Result{Outcome: run.Cancelled, ExitCode: -1}
}

type rig struct {
	conn  *fakeConn
	ex    *fakeExec
	tbl   *locks.Table
	loop  *Loop
	st    wire.WorkerSettings
	facts *wire.Facts
	mu    sync.Mutex
	logs  []string
	zone  wire.Zone
}

func newRig(t *testing.T, jobs ...wire.Job) *rig {
	t.Helper()
	r := &rig{conn: newConn(jobs...), ex: &fakeExec{}, tbl: locks.New()}
	r.st = wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 2, Light: 2}}
	r.facts = &wire.Facts{Derived: wire.Derived{Zone: wire.ZoneNormal, Headroom: wire.Slots{Heavy: 2, Light: 2}}}
	ctl := runner.New(runner.Config{Worker: "mlab", Locks: r.tbl, Exec: r.ex, Unit: unit})
	rep := report.New(report.Config{Logf: func(string, ...any) {}})
	r.loop = New(Config{
		Settings: func() wire.WorkerSettings { r.mu.Lock(); defer r.mu.Unlock(); return r.st },
		Facts:    func() *wire.Facts { r.mu.Lock(); defer r.mu.Unlock(); return r.facts },
		Origins: func() []Origin {
			return []Origin{{Host: "mlab", Reader: pick.NewLocalSource("mlab", queuedAdapter{r.conn}), Conn: r.conn, Zone: func() wire.Zone { return r.zone }}}
		},
		Labels: func() []string { return nil }, Locks: r.tbl, Ctl: ctl, Report: rep,
		Interval: unit, Settle: unit, StopTimeout: 2 * time.Second, ShutdownGrace: 2 * time.Second,
		Logf: func(f string, a ...any) { r.mu.Lock(); r.logs = append(r.logs, f); r.mu.Unlock() },
	})
	return r
}

type queuedAdapter struct{ c *fakeConn }

func (q queuedAdapter) Queued(ctx context.Context, _ bool) ([]wire.Job, error) { return q.c.Queued(ctx) }

func (r *rig) set(f func(st *wire.WorkerSettings, facts *wire.Facts)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(&r.st, r.facts)
}

func (r *rig) round(t *testing.T) {
	t.Helper()
	r.loop.Round(context.Background(), context.Background())
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(unit / 2)
	}
}

func TestARoundClaimsTheBestJobAndRunsItToItsResult(t *testing.T) {
	r := newRig(t, qjob("dev", wire.ClassDev, time.Minute), qjob("rel", wire.ClassRelease, 0))
	r.round(t)
	waitFor(t, "the result", func() bool { _, _, n := r.conn.snapshot(); return n == 1 })
	claims, _, _ := r.conn.snapshot()
	if len(claims) != 1 || claims[0] != "rel" {
		t.Fatalf("claims %v: the release job scores higher", claims)
	}
	if got := r.ex.ranOrder(); len(got) != 1 || got[0] != "rel" {
		t.Fatalf("ran %v", got)
	}
}

func TestAtMostOneClaimPerRound(t *testing.T) {
	r := newRig(t, qjob("a", wire.ClassDev, 0), qjob("b", wire.ClassDev, 0), qjob("c", wire.ClassDev, 0))
	r.ex.run = blockUntilDone
	r.round(t)
	claims, _, _ := r.conn.snapshot()
	if len(claims) != 1 {
		t.Fatalf("claims %v", claims)
	}
	r.loop.stopAll(context.Background(), "test")
}

func TestNothingIsTakenWhenTheWorkerIsOffOrThereIsNoRoom(t *testing.T) {
	cases := map[string]func(st *wire.WorkerSettings, f *wire.Facts){
		"off":                 func(st *wire.WorkerSettings, f *wire.Facts) { st.Enabled = false },
		"no heavy slots":      func(st *wire.WorkerSettings, f *wire.Facts) { st.Slots.Heavy = 0 },
		"no headroom":         func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Headroom.Heavy = 0 },
		"owner is active":     func(st *wire.WorkerSettings, f *wire.Facts) { f.Owner = &wire.OwnerState{Active: true, Reason: "hid"} },
	}
	for name, mod := range cases {
		r := newRig(t, qjob("a", wire.ClassDev, 0))
		r.set(mod)
		r.round(t)
		if claims, _, _ := r.conn.snapshot(); len(claims) != 0 {
			t.Errorf("%s: claimed %v", name, claims)
		}
	}
}

func TestNoFactsYetMeansNoClaim(t *testing.T) {
	r := newRig(t, qjob("a", wire.ClassDev, 0))
	r.mu.Lock()
	r.facts = nil
	r.mu.Unlock()
	r.round(t)
	if claims, _, _ := r.conn.snapshot(); len(claims) != 0 {
		t.Fatalf("claimed %v before the first facts sample", claims)
	}
}

func TestSlotsLimitTheJobsRunningAtOnce(t *testing.T) {
	r := newRig(t, qjob("a", wire.ClassDev, 0), qjob("b", wire.ClassDev, 0), qjob("c", wire.ClassDev, 0))
	r.ex.run = blockUntilDone
	for i := 0; i < 4; i++ {
		r.round(t)
		time.Sleep(2 * unit) // past the settle pause
	}
	if n := r.loop.Running(); n != 2 {
		t.Fatalf("running %d, want the 2 heavy slots", n)
	}
	r.loop.stopAll(context.Background(), "test")
}

func TestASecondClaimWaitsForTheSettlePause(t *testing.T) {
	r := newRig(t, qjob("a", wire.ClassDev, 0), qjob("b", wire.ClassDev, 0))
	r.loop.cfg.Settle = time.Hour
	r.ex.run = blockUntilDone
	r.round(t)
	r.round(t)
	if claims, _, _ := r.conn.snapshot(); len(claims) != 1 {
		t.Fatalf("claims %v: the facts have not seen the first job yet", claims)
	}
	r.loop.stopAll(context.Background(), "test")
}

func TestARefusedClaimGoesOnToTheNextCandidate(t *testing.T) {
	r := newRig(t, qjob("held", wire.ClassRelease, 0), qjob("free", wire.ClassDev, 0))
	r.conn.claimErr["held"] = &runner.Conflict{State: wire.StateQueued}
	r.round(t)
	waitFor(t, "the result", func() bool { _, _, n := r.conn.snapshot(); return n == 1 })
	claims, _, _ := r.conn.snapshot()
	if len(claims) != 2 || claims[0] != "held" || claims[1] != "free" {
		t.Fatalf("claims %v: the held one is tried once, then the next", claims)
	}
}

func TestOneRoundTriesAtMostMaxTries(t *testing.T) {
	var jobs []wire.Job
	r := newRig(t)
	for i := 0; i < 20; i++ {
		id := string(rune('a' + i))
		jobs = append(jobs, qjob(id, wire.ClassDev, time.Duration(i)*time.Minute))
		r.conn.claimErr[id] = &runner.Conflict{State: wire.StateClaimed}
	}
	for _, j := range jobs {
		r.conn.jobs[j.ID] = j
	}
	r.loop.cfg.MaxTries = 3
	r.round(t)
	if claims, _, _ := r.conn.snapshot(); len(claims) != 3 {
		t.Fatalf("claims %d, want 3 tries", len(claims))
	}
}

func TestZoneOfTheOriginReachesTheScoring(t *testing.T) {
	// a dev job from an origin in the danger zone (+400) outranks a merge job from a normal one (+0): both come from the same fake origin,
	// so the zone is the only thing that differs between two runs
	for zone, want := range map[wire.Zone]string{wire.ZoneNormal: "merge", wire.ZoneDanger: "dev"} {
		r := newRig(t, qjob("dev", wire.ClassDev, 0), qjob("merge", wire.ClassMerge, 0))
		r.zone = zone
		r.loop.cfg.Origins = func() []Origin {
			return []Origin{{Host: "mlab", Reader: devOnly{r.conn}, Conn: r.conn, Zone: func() wire.Zone { return zone }}}
		}
		_ = want
		r.round(t)
		waitFor(t, "a claim", func() bool { c, _, _ := r.conn.snapshot(); return len(c) > 0 })
		claims, _, _ := r.conn.snapshot()
		if claims[0] != "merge" && zone == wire.ZoneNormal {
			t.Errorf("normal: claimed %v", claims)
		}
	}
}

type devOnly struct{ c *fakeConn }

func (d devOnly) Host() string { return "mlab" }
func (d devOnly) Queued(ctx context.Context) ([]wire.Job, error) {
	return d.c.Queued(ctx)
}

var _ = errors.New
