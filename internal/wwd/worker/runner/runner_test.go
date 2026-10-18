package runner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/snapshot"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
	"github.com/wake/purdex/internal/wwd/worker/pick"
)

const unit = 20 * time.Millisecond

const claimID = "0123456789abcdef0123456789abcdef"

func testJob(id string, lockKeys ...string) wire.Job {
	return wire.Job{ID: id, Origin: "mlab", Task: "test", Snapshot: wire.Snapshot{Commit: "abc"}, Class: wire.ClassDev,
		State: wire.StateQueued, Repo: wire.Repo{Name: "purdex"}, Locks: lockKeys}
}

// candidates remembers the job each scored() call made, so that a fake origin's default claim answers with the same job (its locks too).
var candidates sync.Map

func scored(j wire.Job) pick.Scored {
	candidates.Store(j.ID, j)
	return pick.Scored{Candidate: pick.Candidate{Source: "mlab", Job: j}}
}

func claimed(id string) wire.Job {
	if j, ok := candidates.Load(id); ok {
		return j.(wire.Job)
	}
	return testJob(id)
}

type fakeOrigin struct {
	mu         sync.Mutex
	claim      func(id string) (wire.ClaimResponse, error)
	start      func() (wire.Lease, error)
	heartbeat  func(n int) (wire.HeartbeatResponse, error)
	claims     int
	starts     int
	heartbeats int
}

func (o *fakeOrigin) Claim(_ context.Context, id string, _ wire.ClaimRequest) (wire.ClaimResponse, error) {
	o.mu.Lock()
	o.claims++
	f := o.claim
	o.mu.Unlock()
	if f != nil {
		return f(id)
	}
	return wire.ClaimResponse{Job: claimed(id), ClaimID: claimID, LeaseS: 5, RenewS: 1}, nil
}

func (o *fakeOrigin) Start(context.Context, string, string) (wire.Lease, error) {
	o.mu.Lock()
	o.starts++
	f := o.start
	o.mu.Unlock()
	if f != nil {
		return f()
	}
	return wire.Lease{LeaseS: 5, RenewS: 1}, nil
}

func (o *fakeOrigin) Heartbeat(ctx context.Context, _, _ string) (wire.HeartbeatResponse, error) {
	o.mu.Lock()
	o.heartbeats++
	n := o.heartbeats
	f := o.heartbeat
	o.mu.Unlock()
	if f != nil {
		return f(n)
	}
	return wire.HeartbeatResponse{LeaseS: 5, RenewS: 1}, nil
}

func (o *fakeOrigin) count() (claims, starts, heartbeats int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.claims, o.starts, o.heartbeats
}

type fakeExec struct {
	materialize func(ctx context.Context) (snapshot.Workspace, error)
	prepare     func() (Plan, error)
	run         func(ctx context.Context) run.Result
	cleaned     atomic.Int32
	ran         atomic.Int32
	plan        atomic.Pointer[Plan]
	order       *[]string
	origins     []string // Job.Origin as Materialize, Prepare and Run each saw it
	mu          sync.Mutex
}

func (e *fakeExec) sawOrigin(j wire.Job) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.origins = append(e.origins, j.Origin)
}

func (e *fakeExec) note(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.order != nil {
		*e.order = append(*e.order, s)
	}
}

func (e *fakeExec) Materialize(ctx context.Context, j wire.Job, _ string) (snapshot.Workspace, error) {
	e.sawOrigin(j)
	if e.materialize != nil {
		return e.materialize(ctx)
	}
	return snapshot.Workspace{Dir: "/ws", Cleanup: func() error { e.cleaned.Add(1); e.note("cleanup"); return nil }}, nil
}

func (e *fakeExec) Prepare(_ context.Context, j wire.Job, _ snapshot.Workspace) (Plan, error) {
	e.sawOrigin(j)
	if e.prepare != nil {
		return e.prepare()
	}
	return Plan{Argv: []string{"true"}, Dir: "/ws", Timeout: time.Minute}, nil
}

func (e *fakeExec) Run(ctx context.Context, j wire.Job, _ snapshot.Workspace, plan Plan) run.Result {
	e.sawOrigin(j)
	e.ran.Add(1)
	e.plan.Store(&plan)
	if e.run != nil {
		return e.run(ctx)
	}
	return run.Result{Outcome: run.Exited}
}

func newCtl(e Exec) (*Controller, *locks.Table) {
	t := locks.New()
	return New(Config{Worker: "mlab", Locks: t, Exec: e, Unit: unit}), t
}

func blockUntilDone(ctx context.Context) run.Result {
	<-ctx.Done()
	return run.Result{Outcome: run.Cancelled, ExitCode: -1}
}

func TestHappyPathRenewsWhileRunningAndFinishesBeforeCleanup(t *testing.T) {
	var order []string
	ex := &fakeExec{order: &order}
	ex.run = func(ctx context.Context) run.Result { time.Sleep(6 * unit); return run.Result{Outcome: run.Exited} }
	o := &fakeOrigin{}
	o.claim = func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: claimed(id), ClaimID: claimID, LeaseS: 4, RenewS: 1}, nil
	}
	c, tbl := newCtl(ex)
	var finishedKind Kind
	var finishCtxAlive bool
	e := c.Work(context.Background(), o, scored(testJob("j1", "mysql")), func(ctx context.Context, e Ended) {
		finishedKind, finishCtxAlive = e.Kind, ctx.Err() == nil
		if _, held := tbl.Holder("mysql"); held {
			t.Error("the lock keys must be free once the command has ended")
		}
		ex.note("finish")
	})
	if e.Kind != KindRan || finishedKind != KindRan || !finishCtxAlive {
		t.Fatalf("kind=%s finish=%s alive=%v err=%v", e.Kind, finishedKind, finishCtxAlive, e.Err)
	}
	if _, st, hb := o.count(); st != 1 || hb < 3 {
		t.Fatalf("starts=%d heartbeats=%d: the lease must be renewed while the command runs", st, hb)
	}
	if len(order) != 2 || order[0] != "finish" || order[1] != "cleanup" {
		t.Fatalf("order %v: the result is delivered before the workspace goes", order)
	}
	if e.ClaimID != claimID || e.Job.ID != "j1" {
		t.Fatalf("ended %+v", e)
	}
}

func TestClaimRefusedIsSkippedAndNothingStays(t *testing.T) {
	for name, claimErr := range map[string]error{
		"held job (409 queued)": &Conflict{State: wire.StateQueued},
		"taken (409 claimed)":   &Conflict{State: wire.StateClaimed},
		"not found":             ErrNotFound,
		"unreachable":           errors.New("dial tcp: refused"),
	} {
		ex := &fakeExec{}
		o := &fakeOrigin{claim: func(string) (wire.ClaimResponse, error) { return wire.ClaimResponse{}, claimErr }}
		c, tbl := newCtl(ex)
		called := false
		e := c.Work(context.Background(), o, scored(testJob("j", "mysql")), func(context.Context, Ended) { called = true })
		if e.Kind != KindSkipped || !errors.Is(e.Err, claimErr) || called {
			t.Errorf("%s: kind=%s err=%v finish=%v", name, e.Kind, e.Err, called)
		}
		if tbl.Owners() != 0 || ex.ran.Load() != 0 {
			t.Errorf("%s: a refused claim must leave no lock and run nothing", name)
		}
	}
}

func TestBusyLocksMeansNoClaim(t *testing.T) {
	ex := &fakeExec{}
	c, tbl := newCtl(ex)
	if _, err := tbl.Acquire("fast-lane", []string{"mysql"}, nil); err != nil {
		t.Fatal(err)
	}
	o := &fakeOrigin{}
	e := c.Work(context.Background(), o, scored(testJob("j", "mysql")), nil)
	var busy *locks.BusyError
	if e.Kind != KindSkipped || !errors.As(e.Err, &busy) {
		t.Fatalf("kind=%s err=%v", e.Kind, e.Err)
	}
	if cl, _, _ := o.count(); cl != 0 {
		t.Fatal("a job whose locks are busy must not even be claimed")
	}
}

func TestLocksAreOneStepWithClaim(t *testing.T) {
	// two jobs with the same key started together: exactly one claims.
	ex := &fakeExec{}
	ex.run = func(ctx context.Context) run.Result { time.Sleep(5 * unit); return run.Result{Outcome: run.Exited} }
	c, _ := newCtl(ex)
	o := &fakeOrigin{}
	var wg sync.WaitGroup
	kinds := make([]Kind, 2)
	for i := range kinds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			kinds[i] = c.Work(context.Background(), o, scored(testJob([]string{"a", "b"}[i], "mysql")), nil).Kind
		}()
	}
	wg.Wait()
	ran := 0
	for _, k := range kinds {
		if k == KindRan {
			ran++
		}
	}
	if ran != 1 {
		t.Fatalf("kinds %v: the same lock key must not be held by two jobs", kinds)
	}
}

func TestSameJobIdOnTwoOriginsDoesNotCollide(t *testing.T) {
	ex := &fakeExec{}
	ex.run = func(ctx context.Context) run.Result { time.Sleep(5 * unit); return run.Result{Outcome: run.Exited} }
	c, _ := newCtl(ex)
	var wg sync.WaitGroup
	kinds := make([]Kind, 2)
	for i, src := range []string{"mlab", "air26"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := scored(testJob("same"))
			s.Source = src
			kinds[i] = c.Work(context.Background(), &fakeOrigin{}, s, nil).Kind
		}()
	}
	wg.Wait()
	if kinds[0] != KindRan || kinds[1] != KindRan {
		t.Fatalf("kinds %v", kinds)
	}
}

func TestCancelStopsTheCommandAndKeepsRenewingForTheResult(t *testing.T) {
	ex := &fakeExec{run: blockUntilDone}
	o := &fakeOrigin{}
	o.heartbeat = func(n int) (wire.HeartbeatResponse, error) {
		return wire.HeartbeatResponse{LeaseS: 5, RenewS: 1, Cancel: n >= 2}, nil
	}
	c, _ := newCtl(ex)
	var hbAtFinish, hbAfter int
	var alive bool
	e := c.Work(context.Background(), o, scored(testJob("j")), func(ctx context.Context, e Ended) {
		_, _, hbAtFinish = o.count()
		time.Sleep(4 * unit)
		_, _, hbAfter = o.count()
		alive = ctx.Err() == nil
	})
	if e.Kind != KindCancelled || e.Run.Outcome != run.Cancelled {
		t.Fatalf("kind=%s run=%+v", e.Kind, e.Run)
	}
	if hbAfter <= hbAtFinish || !alive {
		t.Fatalf("heartbeats %d → %d alive=%v: the lease must go on while the cancelled result is posted", hbAtFinish, hbAfter, alive)
	}
}

func TestHeartbeat409MeansLostAndEndsDelivery(t *testing.T) {
	ex := &fakeExec{run: blockUntilDone}
	o := &fakeOrigin{}
	o.heartbeat = func(int) (wire.HeartbeatResponse, error) {
		return wire.HeartbeatResponse{}, &Conflict{State: wire.StateQueued}
	}
	c, _ := newCtl(ex)
	var finishCtxErr error
	start := time.Now()
	e := c.Work(context.Background(), o, scored(testJob("j")), func(ctx context.Context, e Ended) { finishCtxErr = ctx.Err() })
	if e.Kind != KindLost || finishCtxErr == nil {
		t.Fatalf("kind=%s finishCtxErr=%v: once the lease is gone nothing may be posted", e.Kind, finishCtxErr)
	}
	if d := time.Since(start); d > 3*unit {
		t.Fatalf("took %v: a 409 means stop at once, not at the lease deadline (%v)", d, 5*unit)
	}
}

func TestNoRenewalBeforeOwnDeadlineStopsTheCommand(t *testing.T) {
	ex := &fakeExec{run: blockUntilDone}
	o := &fakeOrigin{}
	o.heartbeat = func(int) (wire.HeartbeatResponse, error) { return wire.HeartbeatResponse{}, errors.New("network down") }
	o.claim = func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: claimed(id), ClaimID: claimID, LeaseS: 6, RenewS: 1}, nil
	}
	c, _ := newCtl(ex)
	start := time.Now()
	e := c.Work(context.Background(), o, scored(testJob("j")), nil)
	if e.Kind != KindLost {
		t.Fatalf("kind=%s", e.Kind)
	}
	if d := time.Since(start); d < 5*unit || d > 12*unit {
		t.Fatalf("stopped after %v, want about the lease (%v)", d, 6*unit)
	}
	if _, _, hb := o.count(); hb < 2 {
		t.Fatalf("heartbeats=%d: an unreachable origin must be retried before giving up", hb)
	}
}

func TestLeaseRunsFromBeforeTheClaimWasSent(t *testing.T) {
	// the claim call itself takes 10 units; the lease is 20 units. Counted from the answer the worker would stop at 30, from the send at 20.
	ex := &fakeExec{run: blockUntilDone}
	o := &fakeOrigin{}
	o.heartbeat = func(int) (wire.HeartbeatResponse, error) { return wire.HeartbeatResponse{}, errors.New("down") }
	o.claim = func(id string) (wire.ClaimResponse, error) {
		time.Sleep(10 * unit)
		return wire.ClaimResponse{Job: claimed(id), ClaimID: claimID, LeaseS: 20, RenewS: 5}, nil
	}
	c, _ := newCtl(ex)
	start := time.Now()
	c.Work(context.Background(), o, scored(testJob("j")), nil)
	if d := time.Since(start); d > 26*unit {
		t.Fatalf("stopped after %v: the deadline must count from before the claim was sent (%v)", d, 20*unit)
	}
}

func TestRejectedIsNotStartedOrRun(t *testing.T) {
	ex := &fakeExec{prepare: func() (Plan, error) { return Plan{}, &Rejected{Reason: "unknown task"} }}
	o := &fakeOrigin{}
	c, tbl := newCtl(ex)
	e := c.Work(context.Background(), o, scored(testJob("j", "mysql")), nil)
	var rej *Rejected
	if e.Kind != KindRejected || !errors.As(e.Err, &rej) || rej.Reason != "unknown task" {
		t.Fatalf("kind=%s err=%v", e.Kind, e.Err)
	}
	if _, st, _ := o.count(); st != 0 || ex.ran.Load() != 0 || ex.cleaned.Load() != 1 || tbl.Owners() != 0 {
		t.Fatalf("starts=%d ran=%d cleaned=%d owners=%d", st, ex.ran.Load(), ex.cleaned.Load(), tbl.Owners())
	}
}

func TestOtherValidateErrorIsInfra(t *testing.T) {
	ex := &fakeExec{prepare: func() (Plan, error) { return Plan{}, errors.New("manifest unreadable: disk") }}
	c, _ := newCtl(ex)
	if e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j")), nil); e.Kind != KindInfra {
		t.Fatalf("kind=%s", e.Kind)
	}
}

func TestMaterializeFailureIsInfraAndStaleClaimIsLost(t *testing.T) {
	ex := &fakeExec{materialize: func(context.Context) (snapshot.Workspace, error) {
		return snapshot.Workspace{}, errors.New("disk full")
	}}
	c, tbl := newCtl(ex)
	e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j", "k")), nil)
	if e.Kind != KindInfra || e.Err == nil || tbl.Owners() != 0 {
		t.Fatalf("kind=%s err=%v", e.Kind, e.Err)
	}
	ex.materialize = func(context.Context) (snapshot.Workspace, error) { return snapshot.Workspace{}, snapshot.ErrStaleClaim }
	if e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j")), nil); e.Kind != KindLost {
		t.Fatalf("stale claim: kind=%s", e.Kind)
	}
}

func TestStartRefusedIsLostStartUnreachableIsInfra(t *testing.T) {
	ex := &fakeExec{}
	c, _ := newCtl(ex)
	o := &fakeOrigin{start: func() (wire.Lease, error) { return wire.Lease{}, &Conflict{State: wire.StateQueued} }}
	if e := c.Work(context.Background(), o, scored(testJob("j")), nil); e.Kind != KindLost || ex.ran.Load() != 0 {
		t.Fatalf("kind=%s ran=%d", e.Kind, ex.ran.Load())
	}
	o = &fakeOrigin{start: func() (wire.Lease, error) { return wire.Lease{}, errors.New("timeout") }}
	if e := c.Work(context.Background(), o, scored(testJob("j")), nil); e.Kind != KindInfra || ex.ran.Load() != 0 {
		t.Fatalf("kind=%s", e.Kind)
	}
}

func TestCancelWhileTheWorkspaceIsBeingMadeSkipsTheRun(t *testing.T) {
	ex := &fakeExec{}
	ex.materialize = func(ctx context.Context) (snapshot.Workspace, error) {
		<-ctx.Done()
		return snapshot.Workspace{Dir: "/ws", Cleanup: func() error { ex.cleaned.Add(1); return nil }}, nil
	}
	o := &fakeOrigin{heartbeat: func(int) (wire.HeartbeatResponse, error) {
		return wire.HeartbeatResponse{LeaseS: 5, RenewS: 1, Cancel: true}, nil
	}}
	c, _ := newCtl(ex)
	e := c.Work(context.Background(), o, scored(testJob("j")), nil)
	if _, st, _ := o.count(); e.Kind != KindCancelled || st != 0 || ex.ran.Load() != 0 || ex.cleaned.Load() != 1 {
		t.Fatalf("kind=%s starts=%d ran=%d cleaned=%d", e.Kind, st, ex.ran.Load(), ex.cleaned.Load())
	}
}

func TestShutdownIsReportedSoTheJobCanBeReleased(t *testing.T) {
	ex := &fakeExec{run: blockUntilDone}
	c, _ := newCtl(ex)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(3 * unit); cancel() }()
	if e := c.Work(ctx, &fakeOrigin{}, scored(testJob("j")), nil); e.Kind != KindShutdown {
		t.Fatalf("kind=%s", e.Kind)
	}
}

func TestNoGoroutineOutlivesWork(t *testing.T) {
	ex := &fakeExec{}
	c, _ := newCtl(ex)
	o := &fakeOrigin{}
	for i := 0; i < 5; i++ {
		c.Work(context.Background(), o, scored(testJob("j")), nil)
	}
	_, _, before := o.count()
	time.Sleep(10 * unit)
	if _, _, after := o.count(); after != before {
		t.Fatalf("heartbeats went on after Work returned: %d → %d", before, after)
	}
}

func TestRunGetsThePlanPrepareMade(t *testing.T) {
	want := Plan{Argv: []string{"go", "test", "./a"}, Dir: "/ws/sub", Timeout: 3 * time.Minute, TaskEnv: []string{"A=1"}}
	ex := &fakeExec{prepare: func() (Plan, error) { return want, nil }}
	c, _ := newCtl(ex)
	if e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j")), nil); e.Kind != KindRan {
		t.Fatalf("kind=%s", e.Kind)
	}
	got := ex.plan.Load()
	if got == nil || len(got.Argv) != 3 || got.Argv[2] != "./a" || got.Dir != want.Dir || got.Timeout != want.Timeout || got.TaskEnv[0] != "A=1" {
		t.Fatalf("plan %+v", got)
	}
}

func TestNotFoundOnHeartbeatOrStartIsLostAtOnce(t *testing.T) {
	ex := &fakeExec{run: blockUntilDone}
	o := &fakeOrigin{heartbeat: func(int) (wire.HeartbeatResponse, error) { return wire.HeartbeatResponse{}, ErrNotFound }}
	c, _ := newCtl(ex)
	start := time.Now()
	if e := c.Work(context.Background(), o, scored(testJob("j")), nil); e.Kind != KindLost || time.Since(start) > 3*unit {
		t.Fatalf("heartbeat: kind=%s after %v", e.Kind, time.Since(start))
	}
	ex2 := &fakeExec{}
	o2 := &fakeOrigin{start: func() (wire.Lease, error) { return wire.Lease{}, ErrNotFound }}
	if e := c.cfgWork(ex2, o2); e.Kind != KindLost || ex2.ran.Load() != 0 {
		t.Fatalf("start: kind=%s ran=%d", e.Kind, ex2.ran.Load())
	}
}

func (c *Controller) cfgWork(ex Exec, o Origin) Ended {
	cc := New(Config{Worker: "mlab", Locks: locks.New(), Exec: ex, Unit: unit})
	return cc.Work(context.Background(), o, scored(testJob("j")), nil)
}

func TestLostAfterCancelStillMeansLost(t *testing.T) {
	// cancel is answered, the command takes a while to end, and meanwhile the lease is lost: nothing may be posted.
	ex := &fakeExec{run: func(ctx context.Context) run.Result {
		<-ctx.Done()
		time.Sleep(6 * unit)
		return run.Result{Outcome: run.Cancelled, ExitCode: -1}
	}}
	o := &fakeOrigin{heartbeat: func(n int) (wire.HeartbeatResponse, error) {
		if n == 1 {
			return wire.HeartbeatResponse{LeaseS: 5, RenewS: 1, Cancel: true}, nil
		}
		return wire.HeartbeatResponse{}, &Conflict{State: wire.StateQueued}
	}}
	c, _ := newCtl(ex)
	if e := c.Work(context.Background(), o, scored(testJob("j")), nil); e.Kind != KindLost {
		t.Fatalf("kind=%s", e.Kind)
	}
}

func TestCancelDuringPrepareDoesNotStartOrRun(t *testing.T) {
	ex := &fakeExec{prepare: func() (Plan, error) {
		time.Sleep(4 * unit) // the heartbeat's cancel arrives while the plan is being made
		return Plan{Argv: []string{"x"}}, nil
	}}
	o := &fakeOrigin{heartbeat: func(int) (wire.HeartbeatResponse, error) {
		return wire.HeartbeatResponse{LeaseS: 5, RenewS: 1, Cancel: true}, nil
	}}
	c, _ := newCtl(ex)
	e := c.Work(context.Background(), o, scored(testJob("j")), nil)
	if _, st, _ := o.count(); e.Kind != KindCancelled || ex.ran.Load() != 0 || st != 0 {
		t.Fatalf("kind=%s ran=%d starts=%d", e.Kind, ex.ran.Load(), st)
	}
}

func TestStartsLeaseReplacesTheClaimsShorterOne(t *testing.T) {
	ex := &fakeExec{run: func(ctx context.Context) run.Result { time.Sleep(8 * unit); return run.Result{Outcome: run.Exited} }}
	o := &fakeOrigin{
		claim: func(id string) (wire.ClaimResponse, error) {
			return wire.ClaimResponse{Job: claimed(id), ClaimID: claimID, LeaseS: 4, RenewS: 50}, nil
		},
		start: func() (wire.Lease, error) { return wire.Lease{LeaseS: 40, RenewS: 50}, nil },
	}
	c, _ := newCtl(ex)
	if e := c.Work(context.Background(), o, scored(testJob("j")), nil); e.Kind != KindRan {
		t.Fatalf("kind=%s: the lease start answers with is the one that counts from then on", e.Kind)
	}
}

func TestEndedCarriesPlanAndRootForTheReporter(t *testing.T) {
	want := Plan{Argv: []string{"go", "test"}, Dir: "/ws", Timeout: time.Minute, Summary: "go-json", OutDir: "/out/j"}
	ex := &fakeExec{prepare: func() (Plan, error) { return want, nil }}
	c, _ := newCtl(ex)
	e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j")), nil)
	if e.Kind != KindRan || e.Plan.Summary != "go-json" || e.Plan.OutDir != "/out/j" || e.Root != "/ws" {
		t.Fatalf("ended %+v", e)
	}
	ex2 := &fakeExec{prepare: func() (Plan, error) { return Plan{}, &Rejected{Reason: "x"} }}
	c2, _ := newCtl(ex2)
	e = c2.Work(context.Background(), &fakeOrigin{}, scored(testJob("j")), nil)
	if e.Kind != KindRejected || e.Plan.Summary != "" || e.Root != "/ws" {
		t.Fatalf("rejected ended %+v", e)
	}
}

func TestHungHeartbeatCannotOutliveTheDeadline(t *testing.T) {
	ex := &fakeExec{run: blockUntilDone}
	o := &fakeOrigin{}
	o.heartbeat = func(int) (wire.HeartbeatResponse, error) {
		return wire.HeartbeatResponse{}, errors.New("unreachable in this test")
	}
	o.claim = func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: claimed(id), ClaimID: claimID, LeaseS: 6, RenewS: 1}, nil
	}
	hang := make(chan struct{})
	h := &hangingHeartbeat{fakeOrigin: o, hang: hang}
	c, _ := newCtl(ex)
	done := make(chan Ended, 1)
	go func() { done <- c.Work(context.Background(), h, scored(testJob("j")), nil) }()
	select {
	case e := <-done:
		if e.Kind != KindLost {
			t.Fatalf("kind=%s", e.Kind)
		}
	case <-time.After(30 * unit):
		close(hang)
		t.Fatal("a heartbeat that never answers kept the command alive past the lease deadline")
	}
}

// hangingHeartbeat answers every heartbeat only when its ctx ends, as a half-open connection does.
type hangingHeartbeat struct {
	*fakeOrigin
	hang chan struct{}
}

func (h *hangingHeartbeat) Heartbeat(ctx context.Context, _, _ string) (wire.HeartbeatResponse, error) {
	select {
	case <-ctx.Done():
		return wire.HeartbeatResponse{}, ctx.Err()
	case <-h.hang:
		return wire.HeartbeatResponse{}, errors.New("released")
	}
}

func TestShorterLeaseFromHeartbeatReplacesTheOlder(t *testing.T) {
	ex := &fakeExec{run: blockUntilDone}
	o := &fakeOrigin{}
	o.heartbeat = func(int) (wire.HeartbeatResponse, error) { return wire.HeartbeatResponse{LeaseS: 2, RenewS: 50}, nil }
	o.claim = func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: claimed(id), ClaimID: claimID, LeaseS: 30, RenewS: 1}, nil
	}
	c, _ := newCtl(ex)
	start := time.Now()
	e := c.Work(context.Background(), o, scored(testJob("j")), nil)
	if d := time.Since(start); e.Kind != KindLost || d > 8*unit {
		t.Fatalf("kind=%s after %v: a lease the origin shortened must be obeyed (about %v)", e.Kind, d, 3*unit)
	}
}

func TestSlowHeartbeatAnswerCannotOverwriteStartsShorterLease(t *testing.T) {
	// a heartbeat is in flight (answered late, with a long lease) while start is called and answers with a short one: the calls are
	// one at a time, so start's answer is applied last and the short lease rules.
	ex := &fakeExec{run: blockUntilDone}
	ex.prepare = func() (Plan, error) { time.Sleep(3 * unit); return Plan{Argv: []string{"x"}}, nil }
	o := &fakeOrigin{
		claim: func(id string) (wire.ClaimResponse, error) {
			return wire.ClaimResponse{Job: claimed(id), ClaimID: claimID, LeaseS: 30, RenewS: 1}, nil
		},
		heartbeat: func(n int) (wire.HeartbeatResponse, error) {
			time.Sleep(4 * unit)
			return wire.HeartbeatResponse{LeaseS: 40, RenewS: 50}, nil
		},
		start: func() (wire.Lease, error) { return wire.Lease{LeaseS: 3, RenewS: 50}, nil },
	}
	c, _ := newCtl(ex)
	start := time.Now()
	e := c.Work(context.Background(), o, scored(testJob("j")), nil)
	if d := time.Since(start); e.Kind != KindLost || d > 20*unit {
		t.Fatalf("kind=%s after %v: start's shorter lease must rule (the long heartbeat answer came earlier)", e.Kind, d)
	}
}

func TestLeaseLostWhileFinishRunsMakesTheResultLost(t *testing.T) {
	ex := &fakeExec{}
	o := &fakeOrigin{}
	o.heartbeat = func(n int) (wire.HeartbeatResponse, error) {
		if n >= 2 {
			return wire.HeartbeatResponse{}, &Conflict{State: wire.StateQueued}
		}
		return wire.HeartbeatResponse{LeaseS: 5, RenewS: 1}, nil
	}
	c, _ := newCtl(ex)
	e := c.Work(context.Background(), o, scored(testJob("j")), func(ctx context.Context, e Ended) {
		if e.Kind != KindRan {
			t.Errorf("Finish began with kind %s", e.Kind)
		}
		<-ctx.Done() // delivery blocks until the lease is gone
	})
	if e.Kind != KindLost {
		t.Fatalf("kind=%s: a result that could not be delivered because the lease went is lost", e.Kind)
	}
}

func TestStartThatHangsPastTheLeaseIsLostNotInfra(t *testing.T) {
	// the origin may have taken the start and the claim's lease may have lapsed meanwhile: the job may already be somebody else's.
	ex := &fakeExec{}
	o := &startHangs{fakeOrigin: &fakeOrigin{claim: func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: claimed(id), ClaimID: claimID, LeaseS: 4, RenewS: 50}, nil
	}}}
	c, _ := newCtl(ex)
	done := make(chan Ended, 1)
	go func() { done <- c.Work(context.Background(), o, scored(testJob("j")), nil) }()
	select {
	case e := <-done:
		if e.Kind != KindLost || ex.ran.Load() != 0 {
			t.Fatalf("kind=%s err=%v ran=%d", e.Kind, e.Err, ex.ran.Load())
		}
	case <-time.After(30 * unit):
		t.Fatal("a start that never answers outlived the lease deadline")
	}
}

type startHangs struct{ *fakeOrigin }

func (s *startHangs) Start(ctx context.Context, _, _ string) (wire.Lease, error) {
	<-ctx.Done()
	return wire.Lease{}, ctx.Err()
}

func TestStartWaitingForTheLockSeesTheCancelTheHeartbeatAnswered(t *testing.T) {
	// the heartbeat is in flight (and holds the call lock) when Prepare finishes, so Start waits; the heartbeat answers cancel.
	ex := &fakeExec{prepare: func() (Plan, error) { time.Sleep(2 * unit); return Plan{Argv: []string{"x"}}, nil }}
	o := &fakeOrigin{heartbeat: func(int) (wire.HeartbeatResponse, error) {
		time.Sleep(5 * unit)
		return wire.HeartbeatResponse{LeaseS: 30, RenewS: 50, Cancel: true}, nil
	}}
	o.claim = func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: claimed(id), ClaimID: claimID, LeaseS: 30, RenewS: 1}, nil
	}
	c, _ := newCtl(ex)
	e := c.Work(context.Background(), o, scored(testJob("j")), nil)
	if _, st, _ := o.count(); e.Kind != KindCancelled || st != 0 || ex.ran.Load() != 0 {
		t.Fatalf("kind=%s starts=%d ran=%d: a cancelled job must not be started", e.Kind, st, ex.ran.Load())
	}
}

func TestRunErrQuarantinesWorkspaceAndLocks(t *testing.T) {
	for _, oc := range []run.Outcome{run.Exited, run.TimedOut, run.Cancelled} {
		ex := &fakeExec{run: func(ctx context.Context) run.Result {
			return run.Result{Outcome: oc, Err: errors.New("could not confirm the unit stopped")}
		}}
		c, tbl := newCtl(ex)
		e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j", "mysql")), nil)
		if !e.Quarantined || ex.cleaned.Load() != 0 {
			t.Errorf("%s: quarantined=%v cleaned=%d: the workspace must stay", oc, e.Quarantined, ex.cleaned.Load())
		}
		if _, held := tbl.Holder("mysql"); !held {
			t.Errorf("%s: the lock keys must stay held", oc)
		}
	}
}

func TestStartFailedErrIsNotQuarantine(t *testing.T) {
	ex := &fakeExec{run: func(ctx context.Context) run.Result {
		return run.Result{Outcome: run.StartFailed, Err: errors.New("not found")}
	}}
	c, tbl := newCtl(ex)
	e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j", "mysql")), nil)
	if e.Quarantined || ex.cleaned.Load() != 1 || tbl.Owners() != 0 {
		t.Fatalf("quarantined=%v cleaned=%d owners=%d", e.Quarantined, ex.cleaned.Load(), tbl.Owners())
	}
}

func TestCleanRunIsNotQuarantined(t *testing.T) {
	ex := &fakeExec{}
	c, tbl := newCtl(ex)
	e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j", "mysql")), nil)
	if e.Quarantined || ex.cleaned.Load() != 1 || tbl.Owners() != 0 {
		t.Fatalf("quarantined=%v cleaned=%d owners=%d", e.Quarantined, ex.cleaned.Load(), tbl.Owners())
	}
}

func TestRunPanicKeepsLocksAndWorkspace(t *testing.T) {
	ex := &fakeExec{run: func(ctx context.Context) run.Result { panic("boom") }}
	c, tbl := newCtl(ex)
	func() {
		defer func() { _ = recover() }()
		c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j", "mysql")), nil)
	}()
	if _, held := tbl.Holder("mysql"); !held || ex.cleaned.Load() != 0 {
		t.Fatalf("held=%v cleaned=%d: a Run that panicked proved nothing about its process tree", held, ex.cleaned.Load())
	}
}

type releasingOrigin struct {
	*fakeOrigin
	released chan wire.ReleaseRequest
}

func (o *releasingOrigin) Release(_ context.Context, _ string, req wire.ReleaseRequest) error {
	o.released <- req
	return nil
}

func TestClaimThatChangesLocksIsHandedBackAndNeverRuns(t *testing.T) {
	ex := &fakeExec{}
	o := &releasingOrigin{fakeOrigin: &fakeOrigin{}, released: make(chan wire.ReleaseRequest, 1)}
	o.claim = func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: testJob(id, "mysql", "tmux"), ClaimID: claimID, LeaseS: 5, RenewS: 1}, nil
	}
	c, tbl := newCtl(ex)
	e := c.Work(context.Background(), o, scored(testJob("j", "mysql")), nil)
	if e.Kind != KindSkipped || e.Err == nil || ex.ran.Load() != 0 || tbl.Owners() != 0 {
		t.Fatalf("kind=%s err=%v ran=%d owners=%d", e.Kind, e.Err, ex.ran.Load(), tbl.Owners())
	}
	select {
	case req := <-o.released:
		if req.ClaimID != claimID || req.Reason != wire.ReleaseInfra {
			t.Fatalf("release %+v", req)
		}
	default:
		t.Fatal("the claim of a job with other locks must be handed back")
	}
}

func TestLocksAreComparedAsASet(t *testing.T) {
	ex := &fakeExec{}
	o := &fakeOrigin{claim: func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: testJob(id, "tmux", "mysql", "tmux"), ClaimID: claimID, LeaseS: 5, RenewS: 1}, nil
	}}
	c, _ := newCtl(ex)
	if e := c.Work(context.Background(), o, scored(testJob("j", "mysql", "tmux")), nil); e.Kind != KindRan {
		t.Fatalf("kind=%s err=%v: same keys in another order are the same locks", e.Kind, e.Err)
	}
}

func TestFewerLocksThanListedIsAlsoAMismatch(t *testing.T) {
	ex := &fakeExec{}
	o := &fakeOrigin{claim: func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: testJob(id, "mysql"), ClaimID: claimID, LeaseS: 5, RenewS: 1}, nil
	}}
	c, _ := newCtl(ex)
	if e := c.Work(context.Background(), o, scored(testJob("j", "mysql", "tmux")), nil); e.Kind != KindSkipped || ex.ran.Load() != 0 {
		t.Fatalf("kind=%s", e.Kind)
	}
}

func TestOtherLockOfTheSameCountIsAMismatch(t *testing.T) {
	ex := &fakeExec{}
	o := &fakeOrigin{claim: func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: testJob(id, "tmux"), ClaimID: claimID, LeaseS: 5, RenewS: 1}, nil
	}}
	c, _ := newCtl(ex)
	if e := c.Work(context.Background(), o, scored(testJob("j", "mysql")), nil); e.Kind != KindSkipped || ex.ran.Load() != 0 {
		t.Fatalf("kind=%s", e.Kind)
	}
}

// An answer from the origin that the snapshot client cannot act on is the origin's failure, not this host's (#3379): the kind says so,
// with the origin's own words in Err; a failure of this host's own stays infra. The locks the job held are let go either way.
func TestMaterializeOriginErrorIsKindOriginAndLocalFailureStaysInfra(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want Kind
	}{
		"5xx":     {&snapshot.OriginError{Status: 500, Body: "the commit of this job is not pinned"}, KindOrigin},
		"busy":    {snapshot.ErrBusy, KindOrigin},
		"wrapped": {fmt.Errorf("fetching: %w", &snapshot.OriginError{Status: 502, Body: "bad gateway"}), KindOrigin},
		"disk":    {errors.New("disk full"), KindInfra},
		"network": {&net.OpError{Op: "dial", Err: errors.New("connection refused")}, KindInfra},
	} {
		t.Run(name, func(t *testing.T) {
			ex := &fakeExec{materialize: func(context.Context) (snapshot.Workspace, error) { return snapshot.Workspace{}, tc.err }}
			c, tbl := newCtl(ex)
			e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j", "k")), nil)
			if e.Kind != tc.want || !errors.Is(e.Err, tc.err) || tbl.Owners() != 0 {
				t.Fatalf("kind=%s err=%v owners=%d", e.Kind, e.Err, tbl.Owners())
			}
		})
	}
}

// The Origin of a job is the connection it came in on. The claim answer is the other host's JSON: a peer can write any name in it, and
// the executor picks the place to get the snapshot from by that name (cmd/wwd/worker.go fetchers), so it must be the worker's own name
// for the connection (cand.Source), as the lock owner already is.
func TestTheJobIsNamedAfterTheConnectionNotAfterWhatTheOtherHostWroteInIt(t *testing.T) {
	ex := &fakeExec{}
	c, _ := newCtl(ex)
	s := scored(testJob("named"))
	s.Source = "air26"
	o := &fakeOrigin{claim: func(id string) (wire.ClaimResponse, error) {
		j := claimed(id)
		j.Origin = "somebody-else" // what a hostile peer writes
		return wire.ClaimResponse{Job: j, ClaimID: claimID, LeaseS: 5, RenewS: 1}, nil
	}}
	ended := c.Work(context.Background(), o, s, nil)
	if ended.Kind != KindRan {
		t.Fatalf("kind %v err %v", ended.Kind, ended.Err)
	}
	if ended.Job.Origin != "air26" {
		t.Fatalf("Ended.Job.Origin = %q, want the connection's name", ended.Job.Origin)
	}
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if len(ex.origins) != 3 {
		t.Fatalf("the executor was called %d times (%v), want Materialize, Prepare and Run", len(ex.origins), ex.origins)
	}
	for _, got := range ex.origins {
		if got != "air26" {
			t.Fatalf("the executor saw Origin %q, want air26: %v", got, ex.origins)
		}
	}
}
