package loop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/ident"
	"github.com/wake/purdex/internal/wwd/worker/locks"
	"github.com/wake/purdex/internal/wwd/worker/pick"
	"github.com/wake/purdex/internal/wwd/worker/quarantine"
	"github.com/wake/purdex/internal/wwd/worker/report"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

const unit = 20 * time.Millisecond

// mlabUnit is the sandbox unit of a job that came in on the connection "mlab": the name carries the origin's tag (two origins can send
// one job id), and it is what the quarantine entry of such a job names.
func mlabUnit(id string) string { return run.UnitName(ident.OriginTag("mlab"), id) }

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
	// releaseHangs makes a release wait for its context; onRelease is called when one arrives
	releaseHangs bool
	onRelease    func()
	releaseTries int
	// resultErr makes every result refuse; resultTries counts the attempts
	resultErr   error
	resultTries int
	claims      []string
	starts      int
	results     []wire.ResultRequest
	releases    []wire.ReleaseRequest
	// onQueued is called, without the lock, each time the queue is read (a test changes the world while a round collects)
	onQueued func()
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
	if c.onQueued != nil {
		c.onQueued()
	}
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
	c.resultTries++
	if c.resultErr != nil {
		return c.resultErr
	}
	c.results = append(c.results, req)
	return nil
}

func (c *fakeConn) PutLog(context.Context, string, string, io.Reader) error { return nil }

func (c *fakeConn) Release(ctx context.Context, _ string, req wire.ReleaseRequest) error {
	c.mu.Lock()
	c.releaseTries++
	c.mu.Unlock()
	if c.onRelease != nil {
		c.onRelease()
	}
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
	// workRoot, when set, makes Materialize create <workRoot>/<job id>/ws as a real directory (what a quarantine marker names)
	workRoot    string
	run         func(ctx context.Context, job wire.Job) run.Result
	materialize func(job wire.Job) error
	ran         atomic.Int32
	mu          sync.Mutex
	order       []string
}

func (e *fakeExec) Materialize(_ context.Context, _ ident.Ref, job wire.Job, _ string) (snapshot.Workspace, error) {
	if e.materialize != nil {
		if err := e.materialize(job); err != nil {
			return snapshot.Workspace{}, err
		}
	}
	if e.workRoot != "" {
		dir := filepath.Join(e.workRoot, job.ID, "ws")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return snapshot.Workspace{}, err
		}
		return snapshot.Workspace{Dir: dir, Cleanup: func() error { return os.RemoveAll(filepath.Dir(dir)) }}, nil
	}
	return snapshot.Workspace{Dir: "/ws"}, nil
}

func (e *fakeExec) Prepare(context.Context, ident.Ref, wire.Job, snapshot.Workspace) (runner.Plan, error) {
	return runner.Plan{Argv: []string{"true"}, Dir: "/ws", Timeout: time.Minute}, nil
}

func (e *fakeExec) Run(ctx context.Context, _ ident.Ref, job wire.Job, _ snapshot.Workspace, _ runner.Plan) run.Result {
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
	conns   []*fakeConn
	zones   map[string]wire.Zone
	ex      *fakeExec
	tbl     *locks.Table
	loop    *Loop
	mu      sync.Mutex
	st      wire.WorkerSettings
	facts   *wire.Facts
	logs    []string
	repLogs []string // what the reporter said
	// freezeAt, when set, is the At of every facts record the loop reads (a sampler that has stopped); else it is "now".
	freezeAt time.Time
}

func newRig(t *testing.T, conns ...*fakeConn) *rig { return newRigWith(t, nil, conns...) }

// newRigWith is newRig with the reporter's configuration changed by cfg (nil: as it is).
func newRigWith(t *testing.T, cfg func(*report.Config), conns ...*fakeConn) *rig {
	t.Helper()
	r := &rig{conns: conns, zones: map[string]wire.Zone{}, ex: &fakeExec{}, tbl: locks.New()}
	r.st = wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 2, Light: 2}}
	r.facts = &wire.Facts{Derived: wire.Derived{Zone: wire.ZoneNormal, Headroom: wire.Slots{Heavy: 2, Light: 2}}}
	ctl := runner.New(runner.Config{Worker: "mlab", Locks: r.tbl, Exec: r.ex, Unit: unit})
	repCfg := report.Config{Logf: func(f string, a ...any) {
		r.mu.Lock()
		r.repLogs = append(r.repLogs, fmt.Sprintf(f, a...))
		r.mu.Unlock()
	}}
	if cfg != nil {
		cfg(&repCfg)
	}
	rep := report.New(repCfg)
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
		"off":            func(st *wire.WorkerSettings, f *wire.Facts) { st.Enabled = false },
		"no heavy slots": func(st *wire.WorkerSettings, f *wire.Facts) { st.Slots.Heavy = 0 },
		"no headroom":    func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Headroom.Heavy = 0 },
		"owner is active": func(st *wire.WorkerSettings, f *wire.Facts) {
			st.OwnerRule = "air26"
			f.Owner = &wire.OwnerState{Active: true, Reason: "hid"}
		},
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

// A command that is not on this host holds off its task, not the repo (#3908 b): the task's jobs are not claimed again, another task of the
// same repo is.
func TestAMissingCommandHoldsOffThatTaskAndNotTheRepo(t *testing.T) {
	mod1, mod2, lint := qjob("mod1", wire.ClassDev, 2*time.Minute), qjob("mod2", wire.ClassDev, time.Minute), qjob("lint", wire.ClassDev, 0)
	mod1.Task, mod2.Task, lint.Task = "mod-hooks", "mod-hooks", "lint"
	c := newConn("mlab", mod1)
	r := newRig(t, c)
	r.ex.run = func(_ context.Context, job wire.Job) run.Result {
		if job.Task == "mod-hooks" {
			return run.Result{Outcome: run.StartFailed, ExitCode: -1, Err: fmt.Errorf("run: %q not found in PATH: %w", "claude", exec.ErrNotFound)}
		}
		return run.Result{Outcome: run.Exited}
	}
	r.round()
	waitFor(t, "the release of mod1", func() bool { _, rel, _ := c.seen(); return len(rel) == 1 })
	if _, rel, _ := c.seen(); rel[0].Reason != wire.ReleaseInfra {
		t.Fatalf("release %+v: the reason stays infra", rel[0])
	}
	c.mu.Lock()
	c.jobs["mod2"], c.jobs["lint"] = mod2, lint
	c.mu.Unlock()
	time.Sleep(3 * unit)
	r.round()
	waitFor(t, "lint to run", func() bool { return r.ex.ran.Load() >= 2 })
	time.Sleep(3 * unit)
	for _, id := range r.ex.ranOrder() {
		if id == "mod2" {
			t.Fatalf("ran %v: mod2 is of the held task and must not be claimed", r.ex.ranOrder())
		}
	}
	claims, _, _ := c.seen()
	got := map[string]int{}
	for _, id := range claims {
		got[id]++
	}
	if got["lint"] != 1 || got["mod2"] != 0 {
		t.Fatalf("claims %v: lint (same repo, another task) must be taken, mod2 (the held task) must not", claims)
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

	r.set(func(st *wire.WorkerSettings, f *wire.Facts) {
		st.OwnerRule = "air26"
		f.Owner = &wire.OwnerState{Active: true, Reason: "hid"}
	})
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
		st.OwnerRule = "air26"
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
	if !got["mlab/running"] || !got["quarantined"] {
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

// The sweepers are the worker's beat for what is on the disk whether or not it takes jobs (the build cache's timed trim, #3833): a worker that
// is switched off (or a host that yields to its owner) still runs them, and claims nothing.
func TestRunSweepsWhileTheWorkerIsSwitchedOff(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	r := newRig(t, c)
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { st.Enabled = false })
	sw := &fakeSweeper{}
	r.loop.cfg.Sweepers = []Sweeper{sw}
	r.loop.cfg.SweepEvery = unit
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.loop.Run(ctx); close(done) }()
	waitFor(t, "two sweeps", func() bool { sw.mu.Lock(); defer sw.mu.Unlock(); return len(sw.calls) >= 2 })
	cancel()
	<-done
	if claims, _, _ := c.seen(); len(claims) != 0 {
		t.Fatalf("claims %v: a worker that is off takes nothing", claims)
	}
}

// The effective rule is the only basis: a reading that says active (the config has an owner source) is not a yield when the rule is empty.
func TestAnActiveOwnerReadingWithoutARuleIsNotAYield(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	r := newRig(t, c)
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Owner = &wire.OwnerState{Active: true, Reason: "hid"} })
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 1 {
		t.Fatalf("claims %v: with no owner rule the reading must not hold the worker", claims)
	}
	r.loop.stopAll(context.Background(), "test")
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

type unitChecker struct {
	mu   sync.Mutex
	gone map[string]bool
	seen []string
}

func (u *unitChecker) Gone(_ context.Context, unit string) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.seen = append(u.seen, unit)
	return u.gone[unit], nil
}

// qrig is a rig whose jobs make real workspaces under a real work root and whose loop has a real quarantine store.
type qrig struct {
	*rig
	root  string
	mdir  string
	store *quarantine.Store
	units *unitChecker
}

func newQRig(t *testing.T, conns ...*fakeConn) *qrig {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := newRig(t, conns...)
	r.ex.workRoot = root
	q := &qrig{rig: r, root: root, mdir: t.TempDir(), units: &unitChecker{gone: map[string]bool{}}}
	q.store, err = quarantine.Open(q.mdir, r.tbl, q.units, time.Now, quarantine.WithWorkRoot(root))
	if err != nil {
		t.Fatal(err)
	}
	r.loop.cfg.Quarantine = q.store
	r.loop.cfg.Sandboxed = true
	return q
}

func notProven(ctx context.Context, _ wire.Job) run.Result {
	<-ctx.Done()
	return run.Result{Outcome: run.Cancelled, ExitCode: -1, Err: errors.New("could not confirm the unit stopped")}
}

func TestAJobThatCannotBeProvenStoppedIsQuarantinedBeforeItIsDelivered(t *testing.T) {
	c := newConn("mlab", qjob("stuck", wire.ClassDev, 0, "mysql"))
	q := newQRig(t, c)
	q.ex.run = notProven
	q.round()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() == 1 })
	q.loop.stopAll(context.Background(), "test")
	waitFor(t, "the delivery", func() bool { _, rel, _ := c.seen(); return len(rel) == 1 })
	held := q.store.Held()
	if len(held) != 1 || held[0].JobID != "stuck" || held[0].Owner != "mlab/stuck" || held[0].Unit != mlabUnit("stuck") || len(held[0].Keys) != 1 {
		t.Fatalf("held %+v", held)
	}
	if want := filepath.Join(q.root, "stuck"); held[0].Dir != want {
		t.Fatalf("dir %q, want the job directory %q", held[0].Dir, want)
	}
	if _, err := os.Stat(held[0].Dir); err != nil {
		t.Fatalf("the workspace must stay: %v", err)
	}
	if _, ok := q.tbl.Holder("mysql"); !ok {
		t.Fatal("the lock keys must stay held")
	}
	if _, rel, _ := c.seen(); rel[0].Reason != wire.ReleaseInfra {
		t.Fatalf("release %+v", rel[0])
	}
}

// Two origins send the same job id: each job's entry has its own owner and its own unit (so neither can be proven gone by the other's
// unit), the store takes both, and a bare Clear of the id refuses and names both.
func TestTwoOriginsWithOneJobIdQuarantineUnderTheirOwnUnits(t *testing.T) {
	a := newConn("air26", qjob("dup", wire.ClassDev, 0))
	b := newConn("mserv", qjob("dup", wire.ClassDev, 0))
	q := newQRig(t, a, b)
	q.ex.run = notProven
	q.round()
	time.Sleep(2 * unit)
	q.round()
	waitFor(t, "two runs", func() bool { return q.ex.ran.Load() == 2 })
	q.loop.stopAll(context.Background(), "test")
	waitFor(t, "two entries", func() bool { return len(q.store.Held()) == 2 })
	units := map[string]string{}
	for _, e := range q.store.Held() {
		units[e.Owner] = e.Unit
	}
	wantA, wantB := run.UnitName(ident.OriginTag("air26"), "dup"), run.UnitName(ident.OriginTag("mserv"), "dup")
	if units["air26/dup"] != wantA || units["mserv/dup"] != wantB || wantA == wantB {
		t.Fatalf("units %v, want air26/dup=%s mserv/dup=%s", units, wantA, wantB)
	}
	// only air26's unit is gone: only air26's entry clears
	q.units.mu.Lock()
	q.units.gone[wantA] = true
	q.units.mu.Unlock()
	q.loop.Sweep(context.Background())
	held := q.store.Held()
	if len(held) != 1 || held[0].Owner != "mserv/dup" {
		t.Fatalf("held %+v: air26's unit is gone, mserv's is not", held)
	}
	q.units.mu.Lock()
	q.units.gone[wantB] = true
	q.units.mu.Unlock()
	q.loop.Sweep(context.Background())
	if len(q.store.Held()) != 0 {
		t.Fatalf("held %+v", q.store.Held())
	}
}

func TestTheMarkerExistsWhenTheDeliveryStarts(t *testing.T) {
	c := newConn("mlab", qjob("stuck", wire.ClassDev, 0))
	q := newQRig(t, c)
	q.ex.run = notProven
	var heldAtRelease atomic.Int32
	c.onRelease = func() { heldAtRelease.Store(int32(len(q.store.Held()))) }
	q.round()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() == 1 })
	q.loop.stopAll(context.Background(), "test")
	waitFor(t, "the release", func() bool { _, rel, _ := c.seen(); return len(rel) == 1 })
	if heldAtRelease.Load() != 1 {
		t.Fatal("the marker is written before the (retried) delivery, not after it")
	}
}

func TestRunDoesNotStartWhenTheHoldsCannotBePutBack(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	q := newQRig(t, c)
	if _, err := q.tbl.Acquire("mlab/old", []string{"mysql"}, nil); err != nil { // the runner's hold of the job that ended
		t.Fatal(err)
	}
	if err := q.store.Add(quarantine.Entry{Owner: "mlab/old", JobID: "old", Origin: "mlab", Dir: filepath.Join(q.root, "old"), Keys: []string{"mysql"}}); err != nil {
		t.Fatal(err)
	}
	q.tbl.ReleaseOwner("mlab/old")                                                   // a restart: the marker is on disk, the hold is not
	if _, err := q.tbl.Acquire("someone-else", []string{"mysql"}, nil); err != nil { // a key the old entry needs is taken
		t.Fatal(err)
	}
	err := q.loop.Run(context.Background())
	if err == nil {
		t.Fatal("Run must say why it did not start")
	}
	if claims, _, _ := c.seen(); len(claims) != 0 {
		t.Fatalf("claims %v: nothing may be taken while a quarantined job's keys are not protected", claims)
	}
}

func TestRestartPutsTheQuarantinedHoldsBackBeforeAnyClaim(t *testing.T) {
	c := newConn("mlab", qjob("needs-mysql", wire.ClassDev, 0, "mysql"), qjob("free", wire.ClassDev, time.Minute))
	q := newQRig(t, c)
	if _, err := q.tbl.Acquire("mlab/old", []string{"mysql"}, nil); err != nil { // the runner's hold of the job that ended
		t.Fatal(err)
	}
	if err := q.store.Add(quarantine.Entry{Owner: "mlab/old", JobID: "old", Origin: "mlab", Dir: filepath.Join(q.root, "old"), Keys: []string{"mysql"}, Unit: "wwd-old"}); err != nil {
		t.Fatal(err)
	}
	// a new process: an empty lock table and a store opened over the same markers
	q.tbl = locks.New()
	var err error
	if q.store, err = quarantine.Open(q.mdir, q.tbl, q.units, time.Now, quarantine.WithWorkRoot(q.root)); err != nil {
		t.Fatal(err)
	}
	ctl := runner.New(runner.Config{Worker: "mlab", Locks: q.tbl, Exec: q.ex, Unit: unit})
	q.loop.cfg.Locks, q.loop.cfg.Ctl, q.loop.cfg.Quarantine = q.tbl, ctl, q.store
	q.ex.run = blockUntilDone
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.loop.Run(ctx) }()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() >= 1 })
	if owner, ok := q.tbl.Holder("mysql"); !ok || owner != "mlab/old" {
		t.Fatalf("holder of mysql = %q %v: the quarantined job's key is held again", owner, ok)
	}
	if got := q.ex.ranOrder(); len(got) != 1 || got[0] != "free" {
		t.Fatalf("ran %v: the job that needs the quarantined key waits", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSweepClearsAQuarantinedJobOnceItsUnitIsGoneAndOtherSweepersSeeItAsHeldUntilThen(t *testing.T) {
	c := newConn("mlab", qjob("stuck", wire.ClassDev, 0, "mysql"))
	q := newQRig(t, c)
	q.ex.run = notProven
	sw := &fakeSweeper{}
	q.loop.cfg.Sweepers = []Sweeper{sw}
	q.round()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() == 1 })
	q.loop.stopAll(context.Background(), "test")
	waitFor(t, "the quarantine", func() bool { return len(q.store.Held()) == 1 })

	q.loop.Sweep(context.Background())
	sw.mu.Lock()
	first := sw.calls[0]
	sw.mu.Unlock()
	if len(first) != 1 || first[0] != "mlab/stuck" {
		t.Fatalf("live %v: a quarantined job's leftovers (its database) must be kept", first)
	}
	if _, ok := q.tbl.Holder("mysql"); !ok {
		t.Fatal("the unit is not proven gone: the hold stays")
	}

	q.units.mu.Lock()
	q.units.gone[mlabUnit("stuck")] = true
	q.units.mu.Unlock()
	q.loop.Sweep(context.Background())
	if _, ok := q.tbl.Holder("mysql"); ok || len(q.store.Held()) != 0 {
		t.Fatalf("held %v: proven gone, the hold and the entry go", q.store.Held())
	}
	if _, err := os.Stat(filepath.Join(q.root, "stuck")); !os.IsNotExist(err) {
		t.Fatalf("the workspace is removed once the unit is gone: %v", err)
	}
	sw.mu.Lock()
	last := sw.calls[len(sw.calls)-1]
	sw.mu.Unlock()
	if len(last) != 0 {
		t.Fatalf("live %v after the clear", last)
	}
}

func TestANonQuarantinedJobWritesNoMarker(t *testing.T) {
	c := newConn("mlab", qjob("fine", wire.ClassDev, 0))
	q := newQRig(t, c)
	q.round()
	waitFor(t, "the result", func() bool { _, _, n := c.seen(); return n == 1 })
	if held := q.store.Held(); len(held) != 0 {
		t.Fatalf("held %+v", held)
	}
}

func TestRunSweepsTheQuarantineByItselfEvenWithNoOtherSweeper(t *testing.T) {
	c := newConn("mlab", qjob("stuck", wire.ClassDev, 0, "mysql"))
	q := newQRig(t, c)
	q.ex.run = notProven
	q.loop.cfg.SweepEvery = unit
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.loop.Run(ctx) }()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() == 1 })
	// the owner's reclaim is not needed: stop the job the way a shutdown would not, by asking it to end through its Running
	q.loop.stopAll(context.Background(), "test")
	waitFor(t, "the quarantine", func() bool { return len(q.store.Held()) == 1 })
	q.units.mu.Lock()
	q.units.gone[mlabUnit("stuck")] = true
	q.units.mu.Unlock()
	waitFor(t, "the clear", func() bool { return len(q.store.Held()) == 0 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// flakyQuarantine fails Add a number of times, then works.
type flakyQuarantine struct {
	Quarantine
	mu    sync.Mutex
	fails int
	tries int
}

func (f *flakyQuarantine) Add(e quarantine.Entry) error {
	f.mu.Lock()
	f.tries++
	n := f.tries
	f.mu.Unlock()
	if n <= f.fails {
		return errors.New("disk full")
	}
	return f.Quarantine.Add(e)
}

func TestAJobIsNotGivenBackUntilItsMarkerIsWritten(t *testing.T) {
	c := newConn("mlab", qjob("stuck", wire.ClassDev, 0))
	q := newQRig(t, c)
	q.loop.cfg.MarkerRetry = unit
	f := &flakyQuarantine{Quarantine: q.store, fails: 3}
	q.loop.cfg.Quarantine = f
	q.ex.run = notProven
	start := time.Now()
	q.round()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() == 1 })
	q.loop.stopAll(context.Background(), "test")
	waitFor(t, "the release", func() bool { _, rel, _ := c.seen(); return len(rel) == 1 })
	f.mu.Lock()
	defer f.mu.Unlock()
	if time.Since(start) < 3*unit {
		t.Fatalf("%v: the retries are paced", time.Since(start))
	}
	if f.tries != 4 || len(q.store.Held()) != 1 {
		t.Fatalf("tries %d held %d: the marker is retried until it is written, and only then is the job released", f.tries, len(q.store.Held()))
	}
}

func TestAJobWhoseMarkerCannotBeWrittenIsNeverReleased(t *testing.T) {
	c := newConn("mlab", qjob("stuck", wire.ClassDev, 0))
	q := newQRig(t, c)
	q.loop.cfg.MarkerRetry = unit
	q.loop.cfg.Quarantine = &flakyQuarantine{Quarantine: q.store, fails: 1 << 30}
	q.ex.run = notProven
	q.round()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() == 1 })
	go q.loop.stopAll(context.Background(), "test")
	time.Sleep(20 * unit)
	if _, rel, res := c.seen(); len(rel) != 0 || res != 0 {
		t.Fatalf("releases %v results %d: another worker could run it next to a unit that may still be going", rel, res)
	}
}

func TestAWorkerThatEndsWhileAMarkerCannotBeWrittenDoesNotTryToReleaseEither(t *testing.T) {
	c := newConn("mlab", qjob("stuck", wire.ClassDev, 0))
	q := newQRig(t, c)
	q.loop.cfg.MarkerRetry = unit
	q.loop.cfg.ShutdownGrace = 3 * unit
	q.loop.cfg.Quarantine = &flakyQuarantine{Quarantine: q.store, fails: 1 << 30}
	q.ex.run = notProven
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.loop.Run(ctx) }()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.releaseTries != 0 {
		t.Fatalf("%d release attempts: a quarantined job with no marker is never given back, not even when the worker ends", c.releaseTries)
	}
}

func TestARunWhoseResultTheOriginRefusedIsNotRecorded(t *testing.T) {
	for name, tc := range map[string]struct {
		refuse error
		want   int
	}{"taken": {nil, 1}, "refused 409": {&runner.Conflict{State: wire.StateQueued}, 0}, "refused 404": {runner.ErrNotFound, 0}} {
		c := newConn("mlab", qjob("a", wire.ClassDev, 0))
		c.resultErr = tc.refuse
		r := newRig(t, c)
		rec := &recorder{conn: c}
		r.loop.cfg.Usage = rec
		r.round()
		waitFor(t, name+": the result attempt", func() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.resultTries >= 1 })
		time.Sleep(4 * unit)
		rec.mu.Lock()
		n := len(rec.got)
		rec.mu.Unlock()
		if n != tc.want {
			t.Errorf("%s: recorded %d runs, want %d: only a run whose result the origin took is the job's run", name, n, tc.want)
		}
	}
}

func TestAReleasedJobIsNotRecorded(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	r := newRig(t, c)
	rec := &recorder{conn: c}
	r.loop.cfg.Usage = rec
	r.ex.materialize = func(wire.Job) error { return errors.New("disk full") } // infra: released, no result
	r.round()
	waitFor(t, "the release", func() bool { _, rel, _ := c.seen(); return len(rel) == 1 })
	time.Sleep(4 * unit)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.got) != 0 {
		t.Fatalf("recorded %d: a job that went back to the queue has no run to record yet", len(rec.got))
	}
}

// The origin answers the snapshot request with a 500 for one job (#3379): the job goes back as a reclaim with the origin's words, this
// host is not held off the repo (another job of it runs at once), the one job is not picked again until its wait is over, and then it
// is tried again, in the same worker, with the same claim path.
func TestAnOriginErrorGivesTheJobBackWithoutHoldingTheRepoOff(t *testing.T) {
	bad, other := qjob("bad", wire.ClassDev, time.Minute), qjob("ok", wire.ClassDev, 0)
	c := newConn("mlab", bad, other)
	c.onRelease = func() { c.mu.Lock(); c.jobs[bad.ID] = bad; c.mu.Unlock() } // the origin puts a released job back in its queue
	wait := 300 * unit
	r := newRigWith(t, func(cfg *report.Config) { cfg.OriginBackoff, cfg.OriginBackoffMax = wait, wait }, c)
	failing := true
	r.ex.materialize = func(j wire.Job) error {
		if j.ID == "bad" && failing {
			return &snapshot.OriginError{Status: 500, Body: "the commit of this job is not pinned"}
		}
		return nil
	}

	r.round() // the older job first: the origin refuses its snapshot
	waitFor(t, "the release", func() bool { _, rel, _ := c.seen(); return len(rel) == 1 })
	claims, rel, _ := c.seen()
	if claims[0] != "bad" || rel[0].Reason != wire.ReleaseReclaim || !strings.Contains(rel[0].Detail, "origin snapshot 500: the commit of this job is not pinned") {
		t.Fatalf("claims %v release %+v", claims, rel[0])
	}
	if !r.logged("500") {
		t.Fatalf("the origin's answer is not in the log: %v", r.repLogs)
	}

	time.Sleep(3 * unit)
	r.round() // the repo is not suspended: the other job runs; the one the origin refused waits
	waitFor(t, "the other job's result", func() bool { _, _, n := c.seen(); return n == 1 })
	if claims, _, _ := c.seen(); len(claims) != 2 || claims[1] != "ok" {
		t.Fatalf("claims %v: want bad, then ok", claims)
	}

	time.Sleep(3 * unit)
	r.round() // still inside the wait: nothing is claimed
	if claims, _, _ := c.seen(); len(claims) != 2 {
		t.Fatalf("claims %v: the refused job is held off for its wait", claims)
	}

	failing = false
	time.Sleep(wait)
	r.round() // the wait is over, the origin is mended: the same job is claimed and runs
	waitFor(t, "the job's result", func() bool { _, _, n := c.seen(); return n == 2 })
	if claims, _, _ := c.seen(); len(claims) != 3 || claims[2] != "bad" {
		t.Fatalf("claims %v: want the refused job again after its wait", claims)
	}
}

func (r *rig) logged(sub string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.repLogs {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}
