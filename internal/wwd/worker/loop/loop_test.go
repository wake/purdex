package loop

import (
	"context"
	"errors"
	"io"
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
	host     string
	mu       sync.Mutex
	jobs     map[string]wire.Job
	claimErr map[string]error
	readErr  error
	// releaseHangs makes a release wait for its context
	releaseHangs bool
	claims       []string
	starts       int
	results      []wire.ResultRequest
	releases     []wire.ReleaseRequest
}

func newConn(host string, jobs ...wire.Job) *fakeConn {
	c := &fakeConn{host: host, jobs: map[string]wire.Job{}, claimErr: map[string]error{}}
	for _, j := range jobs {
		c.jobs[j.ID] = j
	}
	return c
}

func (c *fakeConn) Host() string { return c.host }

func (c *fakeConn) Queued(context.Context) ([]wire.Job, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil {
		return nil, c.readErr
	}
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

func (c *fakeConn) PutLog(context.Context, string, string, io.Reader) error { return nil }

func (c *fakeConn) Release(ctx context.Context, _ string, req wire.ReleaseRequest) error {
	if c.releaseHangs {
		<-ctx.Done() // an origin that does not answer: only the context ends the call
	}
	if err := ctx.Err(); err != nil {
		return err // a release made under an ended context does not reach the origin
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releases = append(c.releases, req)
	return nil
}

func (c *fakeConn) seen() (claims []string, releases []wire.ReleaseRequest, results int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.claims...), append([]wire.ReleaseRequest(nil), c.releases...), len(c.results)
}

type fakeExec struct {
	run         func(ctx context.Context, job wire.Job) run.Result
	materialize func(job wire.Job) error
	ran         atomic.Int32
	mu          sync.Mutex
	order       []string
}

func (e *fakeExec) Materialize(_ context.Context, job wire.Job, _ string) (snapshot.Workspace, error) {
	if e.materialize != nil {
		if err := e.materialize(job); err != nil {
			return snapshot.Workspace{}, err
		}
	}
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

type queuedAdapter struct{ c *fakeConn }

func (q queuedAdapter) Queued(ctx context.Context, _ bool) ([]wire.Job, error) {
	return q.c.Queued(ctx)
}

type rig struct {
	conns []*fakeConn
	zones map[string]wire.Zone
	ex    *fakeExec
	tbl   *locks.Table
	loop  *Loop
	mu    sync.Mutex
	st    wire.WorkerSettings
	facts *wire.Facts
	logs  []string
	// freezeAt, when set, is the At of every facts record the loop reads (a sampler that has stopped); else it is "now".
	freezeAt time.Time
}

func newRig(t *testing.T, conns ...*fakeConn) *rig {
	t.Helper()
	r := &rig{conns: conns, zones: map[string]wire.Zone{}, ex: &fakeExec{}, tbl: locks.New()}
	r.st = wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 2, Light: 2}}
	r.facts = &wire.Facts{Derived: wire.Derived{Zone: wire.ZoneNormal, Headroom: wire.Slots{Heavy: 2, Light: 2}}}
	ctl := runner.New(runner.Config{Worker: "mlab", Locks: r.tbl, Exec: r.ex, Unit: unit})
	rep := report.New(report.Config{Logf: func(string, ...any) {}})
	r.loop = New(Config{
		Settings: func() wire.WorkerSettings { r.mu.Lock(); defer r.mu.Unlock(); return r.st },
		Facts: func() *wire.Facts {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.facts == nil {
				return nil
			}
			f := *r.facts
			f.At = time.Now()
			if !r.freezeAt.IsZero() {
				f.At = r.freezeAt
			}
			return &f
		},
		Origins: func() []Origin {
			var out []Origin
			for _, c := range r.conns {
				host := c.host
				out = append(out, Origin{Host: host, Reader: pick.NewLocalSource(host, queuedAdapter{c}), Conn: c, Zone: func() wire.Zone {
					r.mu.Lock()
					defer r.mu.Unlock()
					return r.zones[host]
				}})
			}
			return out
		},
		Labels: func() []string { return nil }, Locks: r.tbl, Ctl: ctl, Report: rep,
		Interval: unit, Settle: unit, StopTimeout: 2 * time.Second, ShutdownGrace: 2 * time.Second,
		Logf: func(f string, a ...any) { r.mu.Lock(); r.logs = append(r.logs, f); r.mu.Unlock() },
	})
	return r
}

func (r *rig) set(f func(st *wire.WorkerSettings, facts *wire.Facts)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f(&r.st, r.facts)
}

func (r *rig) round() { r.loop.Round(context.Background(), context.Background()) }

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
	c := newConn("mlab", qjob("dev", wire.ClassDev, time.Minute), qjob("rel", wire.ClassRelease, 0))
	r := newRig(t, c)
	r.round()
	waitFor(t, "the result", func() bool { _, _, n := c.seen(); return n == 1 })
	if claims, _, _ := c.seen(); len(claims) != 1 || claims[0] != "rel" {
		t.Fatalf("claims %v: the release job scores higher", claims)
	}
}

func TestAtMostOneClaimPerRound(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0), qjob("b", wire.ClassDev, 0), qjob("c", wire.ClassDev, 0))
	r := newRig(t, c)
	r.ex.run = blockUntilDone
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 1 {
		t.Fatalf("claims %v", claims)
	}
	r.loop.stopAll(context.Background(), "test")
}

func TestNothingIsTakenWhenTheWorkerIsOffOrThereIsNoRoom(t *testing.T) {
	cases := map[string]func(st *wire.WorkerSettings, f *wire.Facts){
		"off":             func(st *wire.WorkerSettings, f *wire.Facts) { st.Enabled = false },
		"no heavy slots":  func(st *wire.WorkerSettings, f *wire.Facts) { st.Slots.Heavy = 0 },
		"no headroom":     func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Headroom.Heavy = 0 },
		"owner is active": func(st *wire.WorkerSettings, f *wire.Facts) { f.Owner = &wire.OwnerState{Active: true, Reason: "hid"} },
	}
	for name, mod := range cases {
		c := newConn("mlab", qjob("a", wire.ClassDev, 0))
		r := newRig(t, c)
		r.set(mod)
		r.round()
		if claims, _, _ := c.seen(); len(claims) != 0 {
			t.Errorf("%s: claimed %v", name, claims)
		}
	}
}

func TestNoFactsYetMeansNoClaim(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	r := newRig(t, c)
	r.mu.Lock()
	r.facts = nil
	r.mu.Unlock()
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 0 {
		t.Fatalf("claimed %v before the first facts sample", claims)
	}
}

func TestAHostWithAnOwnerRuleTakesNothingBeforeItsFirstReading(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	r := newRig(t, c)
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { st.OwnerRule = "air26" }) // facts.owner is nil: no reading yet
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 0 {
		t.Fatalf("claimed %v: the owner is not yet shown to be away", claims)
	}
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Owner = &wire.OwnerState{Active: false} })
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 1 {
		t.Fatalf("claims %v after the owner was shown away", claims)
	}
	r.loop.stopAll(context.Background(), "test")
}

func TestSlotsLimitTheJobsRunningAtOnce(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0), qjob("b", wire.ClassDev, 0), qjob("c", wire.ClassDev, 0))
	r := newRig(t, c)
	r.ex.run = blockUntilDone
	for i := 0; i < 4; i++ {
		r.round()
		time.Sleep(2 * unit) // past the settle pause
	}
	if n := r.loop.Running(); n != 2 {
		t.Fatalf("running %d, want the 2 heavy slots", n)
	}
	r.loop.stopAll(context.Background(), "test")
}

func TestASecondClaimWaitsForTheSettlePause(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0), qjob("b", wire.ClassDev, 0))
	r := newRig(t, c)
	r.loop.cfg.Settle = time.Hour
	r.ex.run = blockUntilDone
	r.round()
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 1 {
		t.Fatalf("claims %v: the facts have not seen the first job yet", claims)
	}
	r.loop.stopAll(context.Background(), "test")
}

func TestARefusedClaimGoesOnToTheNextCandidate(t *testing.T) {
	c := newConn("mlab", qjob("held", wire.ClassRelease, 0), qjob("free", wire.ClassDev, 0))
	c.claimErr["held"] = &runner.Conflict{State: wire.StateQueued}
	r := newRig(t, c)
	r.round()
	waitFor(t, "the result", func() bool { _, _, n := c.seen(); return n == 1 })
	if claims, _, _ := c.seen(); len(claims) != 2 || claims[0] != "held" || claims[1] != "free" {
		t.Fatalf("claims %v: the held one is tried once, then the next", claims)
	}
}

func TestOneRoundTriesAtMostMaxTries(t *testing.T) {
	c := newConn("mlab")
	for i := 0; i < 20; i++ {
		id := string(rune('a' + i))
		c.jobs[id] = qjob(id, wire.ClassDev, time.Duration(i)*time.Minute)
		c.claimErr[id] = &runner.Conflict{State: wire.StateClaimed}
	}
	r := newRig(t, c)
	r.loop.cfg.MaxTries = 3
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 3 {
		t.Fatalf("claims %d, want 3 tries", len(claims))
	}
}

func TestTheOriginsZoneDecidesBetweenTwoEqualJobs(t *testing.T) {
	// the same dev job on two origins: the one whose host is in the danger zone gets +400 (offload where it hurts), and it is the zone
	// the callback reports that decides, so flipping the zones flips the claim
	for _, danger := range []string{"air26", "mlab"} {
		a, b := newConn("mlab", qjob("on-mlab", wire.ClassDev, 0)), newConn("air26", qjob("on-air26", wire.ClassDev, 0))
		r := newRig(t, a, b)
		r.zones[danger] = wire.ZoneDanger
		r.ex.run = blockUntilDone
		r.round()
		want := map[string]string{"air26": "on-air26", "mlab": "on-mlab"}[danger]
		waitFor(t, "a run", func() bool { return len(r.ex.ranOrder()) > 0 })
		if got := r.ex.ranOrder()[0]; got != want {
			t.Errorf("danger on %s: ran %s, want %s", danger, got, want)
		}
		r.loop.stopAll(context.Background(), "test")
	}
}

func TestAnUnreadableOriginDoesNotStopTheOthers(t *testing.T) {
	down := newConn("air26")
	down.readErr = errors.New("connection refused")
	up := newConn("mlab", qjob("a", wire.ClassDev, 0))
	r := newRig(t, down, up)
	r.round()
	waitFor(t, "the result", func() bool { _, _, n := up.seen(); return n == 1 })
	r.mu.Lock()
	defer r.mu.Unlock()
	found := false
	for _, l := range r.logs {
		if l == "worker: reading the queue of %s: %v" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the failing origin must be logged: %v", r.logs)
	}
}

func TestJobsWhoseLocksAreHeldAreNotClaimed(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0, "mysql"), qjob("b", wire.ClassDev, time.Minute, "tmux"))
	r := newRig(t, c)
	if _, err := r.tbl.Acquire("fast-lane", []string{"tmux"}, nil); err != nil {
		t.Fatal(err)
	}
	r.loop.cfg.MaxTries = 1 // the one try must go to a job that can run, so the locked one is left out before any try
	r.round()
	waitFor(t, "the result", func() bool { _, _, n := c.seen(); return n == 1 })
	if claims, _, _ := c.seen(); len(claims) != 1 || claims[0] != "a" {
		t.Fatalf("claims %v: the job whose lock key is held by the fast lane must not even be claimed", claims)
	}
}

func TestAnInfraFailureHoldsThatOriginsRepoOff(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0), qjob("b", wire.ClassDev, time.Minute))
	r := newRig(t, c)
	r.ex.materialize = func(wire.Job) error { return errors.New("disk full") }
	r.round()
	waitFor(t, "the release", func() bool { _, rel, _ := c.seen(); return len(rel) == 1 })
	if _, rel, _ := c.seen(); rel[0].Reason != wire.ReleaseInfra {
		t.Fatalf("release %+v", rel[0])
	}
	time.Sleep(3 * unit)
	r.round() // the repo is suspended for five minutes
	if claims, _, _ := c.seen(); len(claims) != 1 {
		t.Fatalf("claims %v: the repo is held off after an infrastructure failure", claims)
	}
}

func TestTheOwnerTakingTheMachineStopsAndGivesBackEveryJobAndTakesNothingMore(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0), qjob("b", wire.ClassDev, 0), qjob("c", wire.ClassDev, 0))
	r := newRig(t, c)
	r.ex.run = blockUntilDone
	r.round()
	time.Sleep(2 * unit)
	r.round()
	waitFor(t, "two runs", func() bool { return r.ex.ran.Load() == 2 })

	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Owner = &wire.OwnerState{Active: true, Reason: "hid"} })
	r.round()
	claims, rel, _ := c.seen()
	if len(rel) != 2 || rel[0].Reason != wire.ReleaseReclaim || rel[1].Reason != wire.ReleaseReclaim {
		t.Fatalf("releases %+v: both jobs go back as reclaim", rel)
	}
	if len(claims) != 2 || r.loop.Running() != 0 {
		t.Fatalf("claims %v running %d: nothing is taken while the owner is here", claims, r.loop.Running())
	}
	r.round()
	if claims2, _, _ := c.seen(); len(claims2) != 2 {
		t.Fatalf("claims %v: still the owner's machine", claims2)
	}

	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Owner = &wire.OwnerState{Active: false} })
	time.Sleep(2 * unit)
	r.round()
	if claims3, _, _ := c.seen(); len(claims3) != 3 {
		t.Fatalf("claims %v: the worker takes jobs again once the owner is away", claims3)
	}
	r.loop.stopAll(context.Background(), "test")
}

func TestAJobThatDoesNotStopStaysTrackedAndNothingIsClaimedMeanwhile(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0), qjob("b", wire.ClassDev, 0))
	r := newRig(t, c)
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	r.ex.run = func(ctx context.Context, _ wire.Job) run.Result {
		started <- struct{}{}
		<-release // a command that does not end when it is asked to
		return run.Result{Outcome: run.Exited}
	}
	r.round()
	<-started
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) {
		f.Owner = &wire.OwnerState{Active: true, Reason: "build"}
	})
	r.loop.cfg.StopTimeout = 3 * unit
	r.round()
	if n := r.loop.Running(); n != 1 {
		t.Fatalf("running %d: a job that did not end is still the worker's", n)
	}
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Owner = &wire.OwnerState{Active: false} })
	time.Sleep(2 * unit)
	r.round() // the owner is away, the slot is taken by the job that is still running: one slot left, and it is used
	close(release)
	waitFor(t, "the job to end", func() bool { return r.loop.Running() <= 1 })
	r.loop.stopAll(context.Background(), "test")
}

func TestRunDoesRoundsAndThenStopsAndGivesBackOnShutdown(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	r := newRig(t, c)
	r.ex.run = blockUntilDone
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.loop.Run(ctx); close(done) }()
	waitFor(t, "a run", func() bool { return r.ex.ran.Load() == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	if _, rel, _ := c.seen(); len(rel) != 1 || rel[0].Reason != wire.ReleaseReclaim {
		t.Fatalf("releases %+v: a worker that ends gives its jobs back, with the lease alive", rel)
	}
}

type recorder struct {
	mu        sync.Mutex
	got       []runner.Ended
	panic     bool
	conn      *fakeConn
	delivered int
}

func (r *recorder) RecordRun(e runner.Ended) {
	_, _, n := r.conn.seen()
	r.mu.Lock()
	r.got = append(r.got, e)
	r.delivered = n
	r.mu.Unlock()
	if r.panic {
		panic("recorder broke")
	}
}

func TestTheUsageRecorderSeesTheWholeRunAfterTheResultAndCannotHurtIt(t *testing.T) {
	for _, boom := range []bool{false, true} {
		c := newConn("mlab", qjob("a", wire.ClassDev, 0))
		r := newRig(t, c)
		rec := &recorder{panic: boom, conn: c}
		r.loop.cfg.Usage = rec
		r.ex.run = func(ctx context.Context, _ wire.Job) run.Result {
			return run.Result{Outcome: run.Exited, ExitCode: 3, LogPath: "/out/log", Tail: []byte("t")}
		}
		r.round()
		waitFor(t, "the record", func() bool { rec.mu.Lock(); defer rec.mu.Unlock(); return len(rec.got) == 1 })
		_, _, results := c.seen()
		rec.mu.Lock()
		e := rec.got[0]
		rec.mu.Unlock()
		rec.mu.Lock()
		delivered := rec.delivered
		rec.mu.Unlock()
		if delivered != 1 {
			t.Fatalf("the recorder ran with %d results delivered: it comes after the delivery", delivered)
		}
		if results != 1 || e.Kind != runner.KindRan || e.Run.ExitCode != 3 || e.Run.LogPath != "/out/log" || string(e.Run.Tail) != "t" {
			t.Fatalf("panic=%v results=%d ended %+v: the result is delivered first and the whole run.Result reaches the recorder", boom, results, e)
		}
	}
}

func TestNoUsageRecorderMeansNoRecording(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	r := newRig(t, c)
	r.round()
	waitFor(t, "the result", func() bool { _, _, n := c.seen(); return n == 1 })
	time.Sleep(2 * unit)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.logs {
		if l == "worker: recording the run of %s: %v" {
			t.Fatal("with no recorder nothing may be attempted, not even a recovered panic")
		}
	}
}

type fakeSweeper struct {
	mu    sync.Mutex
	calls [][]string
	err   error
}

func (f *fakeSweeper) Sweep(_ context.Context, live []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), live...))
	return f.err
}

func TestSweepGetsEveryJobTheWorkerHolds(t *testing.T) {
	c := newConn("mlab", qjob("running", wire.ClassDev, 0))
	r := newRig(t, c)
	r.ex.run = blockUntilDone
	sw := &fakeSweeper{err: errors.New("db down")}
	r.loop.cfg.Sweepers = []Sweeper{sw}
	r.loop.cfg.Held = func() []string { return []string{"quarantined"} }
	r.round()
	waitFor(t, "a run", func() bool { return r.ex.ran.Load() == 1 })
	r.loop.Sweep(context.Background())
	r.loop.stopAll(context.Background(), "test")
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if len(sw.calls) != 1 || len(sw.calls[0]) != 2 {
		t.Fatalf("sweep calls %v", sw.calls)
	}
	got := map[string]bool{sw.calls[0][0]: true, sw.calls[0][1]: true}
	if !got["running"] || !got["quarantined"] {
		t.Fatalf("live %v: what runs and what is quarantined must both be kept", sw.calls[0])
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	logged := false
	for _, l := range r.logs {
		logged = logged || l == "worker: sweep: %v"
	}
	if !logged {
		t.Fatal("a failing sweep is logged")
	}
}

func TestSweepsRunOnTheirOwnPauseNotEveryRound(t *testing.T) {
	r := newRig(t, newConn("mlab"))
	sw := &fakeSweeper{}
	r.loop.cfg.Sweepers = []Sweeper{sw}
	r.loop.cfg.SweepEvery = time.Hour
	r.loop.sweepIfDue(context.Background())
	r.loop.sweepIfDue(context.Background())
	sw.mu.Lock()
	n := len(sw.calls)
	sw.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d sweeps, want 1 within the pause", n)
	}
	r.loop.cfg.SweepEvery = time.Nanosecond
	time.Sleep(unit)
	r.loop.sweepIfDue(context.Background())
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if len(sw.calls) != 2 {
		t.Fatalf("%d sweeps, want one more after the pause", len(sw.calls))
	}
}

func TestRunSweepsOnItsOwnPause(t *testing.T) {
	r := newRig(t, newConn("mlab"))
	sw := &fakeSweeper{}
	r.loop.cfg.Sweepers = []Sweeper{sw}
	r.loop.cfg.SweepEvery = unit
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.loop.Run(ctx); close(done) }()
	waitFor(t, "two sweeps", func() bool { sw.mu.Lock(); defer sw.mu.Unlock(); return len(sw.calls) >= 2 })
	cancel()
	<-done
}

func TestSwitchingTheOwnerRuleOffWhileYieldingFreesTheWorker(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	r := newRig(t, c)
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) {
		st.OwnerRule = "air26"
		f.Owner = &wire.OwnerState{Active: true, Reason: "hid"}
	})
	r.round() // yielding
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { st.OwnerRule = ""; f.Owner = nil })
	time.Sleep(2 * unit)
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 1 {
		t.Fatalf("claims %v: with the rule gone nobody is watched, and the worker must not stay stopped", claims)
	}
	r.loop.stopAll(context.Background(), "test")
}

func TestSwitchingTheOwnerRuleOnMakesTheWorkerWaitForAReading(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	r := newRig(t, c)
	r.round() // no rule: takes the job... use a second job to see the next round
	waitFor(t, "the result", func() bool { _, _, n := c.seen(); return n == 1 })
	c.mu.Lock()
	c.jobs["b"] = qjob("b", wire.ClassDev, 0)
	c.mu.Unlock()
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { st.OwnerRule = "air26" }) // no reading yet
	time.Sleep(2 * unit)
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 1 {
		t.Fatalf("claims %v: a host that has just been given an owner rule takes nothing before its first reading", claims)
	}
}

func TestASampleOlderThanTheLastClaimDoesNotOpenTheNextOne(t *testing.T) {
	// the sampler has stopped: the same record, time going on. The first claim is allowed (nothing was claimed before), the second needs a
	// sample taken after the first job could have shown in it
	c := newConn("mlab", qjob("a", wire.ClassDev, 0), qjob("b", wire.ClassDev, 0), qjob("c", wire.ClassDev, 0))
	r := newRig(t, c)
	r.freezeAt = time.Now()
	r.ex.run = blockUntilDone
	r.round()
	for i := 0; i < 3; i++ {
		time.Sleep(2 * unit)
		r.round()
	}
	if claims, _, _ := c.seen(); len(claims) != 1 {
		t.Fatalf("claims %v: the headroom of a sample older than the claim says nothing about the claimed job", claims)
	}
	r.loop.stopAll(context.Background(), "test")
}

func TestStaleFactsStopTheWorkerTakingJobs(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	r := newRig(t, c)
	r.loop.cfg.FactsMaxAge = time.Second
	r.freezeAt = time.Now().Add(-time.Minute)
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 0 {
		t.Fatalf("claims %v: a headroom last measured a minute ago is not a headroom", claims)
	}
	r.freezeAt = time.Now()
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 1 {
		t.Fatalf("claims %v: fresh facts, the worker takes the job", claims)
	}
	r.loop.stopAll(context.Background(), "test")
}

func TestAWorkerThatEndsWithAJobThatWillNotStopEndsItsCommandAndSaysSo(t *testing.T) {
	c := newConn("mlab", qjob("stuck", wire.ClassDev, 0))
	r := newRig(t, c)
	r.loop.cfg.ShutdownGrace = 3 * unit
	started := make(chan struct{}, 1)
	endedByCtx := make(chan struct{})
	var once sync.Once
	r.ex.run = func(ctx context.Context, _ wire.Job) run.Result {
		started <- struct{}{}
		// ignores the first ask (Stop's), obeys the second (the context of the jobs being cancelled for good)
		<-ctx.Done()
		time.Sleep(10 * unit)
		once.Do(func() { close(endedByCtx) })
		return run.Result{Outcome: run.Cancelled}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.loop.Run(ctx); close(done) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	select {
	case <-endedByCtx:
	default:
		t.Fatal("Run returned while the command was still running")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	said := false
	for _, l := range r.logs {
		said = said || l == "worker: %s/%s did not stop within the shutdown grace; its commands are ended and the origin gets the job back when its lease lapses"
	}
	if !said {
		t.Fatalf("a job left behind at shutdown is named in the log: %v", r.logs)
	}
}

func TestAWorkerThatEndsStopsRetryingReleasesThatDoNotAnswer(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	c.releaseHangs = true
	r := newRig(t, c)
	r.loop.cfg.ShutdownGrace = 3 * unit
	r.ex.run = blockUntilDone
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.loop.Run(ctx); close(done) }()
	waitFor(t, "a run", func() bool { return r.ex.ran.Load() == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run waited for a release that never answers: the jobs' contexts must be ended after the grace")
	}
}

func TestFactsFromTheFutureAreNotTrusted(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	r := newRig(t, c)
	r.freezeAt = time.Now().Add(time.Hour) // the clock was set back after the sample
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 0 {
		t.Fatalf("claims %v: a sample dated in the future is not a fresh one", claims)
	}
	r.freezeAt = time.Time{}
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 1 {
		t.Fatalf("claims %v: the first claim with a fresh sample goes through", claims)
	}
	r.loop.stopAll(context.Background(), "test")
}
