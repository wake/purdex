package runner

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
)

const unit = 20 * time.Millisecond

const claimID = "0123456789abcdef0123456789abcdef"

func testJob(id string, lockKeys ...string) wire.Job {
	return wire.Job{ID: id, Origin: "mlab", Task: "test", Snapshot: wire.Snapshot{Commit: "abc"}, Class: wire.ClassDev,
		State: wire.StateQueued, Repo: wire.Repo{Name: "purdex"}, Locks: lockKeys}
}

func scored(j wire.Job) pick.Scored {
	return pick.Scored{Candidate: pick.Candidate{Source: "mlab", Job: j}}
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
	return wire.ClaimResponse{Job: testJob(id), ClaimID: claimID, LeaseS: 5, RenewS: 1}, nil
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
	mu          sync.Mutex
}

func (e *fakeExec) note(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.order != nil {
		*e.order = append(*e.order, s)
	}
}

func (e *fakeExec) Materialize(ctx context.Context, _ wire.Job, _ string) (snapshot.Workspace, error) {
	if e.materialize != nil {
		return e.materialize(ctx)
	}
	return snapshot.Workspace{Dir: "/ws", Cleanup: func() error { e.cleaned.Add(1); e.note("cleanup"); return nil }}, nil
}

func (e *fakeExec) Prepare(context.Context, wire.Job, snapshot.Workspace) (Plan, error) {
	if e.prepare != nil {
		return e.prepare()
	}
	return Plan{Argv: []string{"true"}, Dir: "/ws", Timeout: time.Minute}, nil
}

func (e *fakeExec) Run(ctx context.Context, _ wire.Job, _ snapshot.Workspace, plan Plan) run.Result {
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
		return wire.ClaimResponse{Job: testJob(id), ClaimID: claimID, LeaseS: 4, RenewS: 1}, nil
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
	e := c.Work(context.Background(), o, scored(testJob("j")), func(ctx context.Context, e Ended) { finishCtxErr = ctx.Err() })
	if e.Kind != KindLost || finishCtxErr == nil {
		t.Fatalf("kind=%s finishCtxErr=%v: once the lease is gone nothing may be posted", e.Kind, finishCtxErr)
	}
}

func TestNoRenewalBeforeOwnDeadlineStopsTheCommand(t *testing.T) {
	ex := &fakeExec{run: blockUntilDone}
	o := &fakeOrigin{}
	o.heartbeat = func(int) (wire.HeartbeatResponse, error) { return wire.HeartbeatResponse{}, errors.New("network down") }
	o.claim = func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: testJob(id), ClaimID: claimID, LeaseS: 6, RenewS: 1}, nil
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
		return wire.ClaimResponse{Job: testJob(id), ClaimID: claimID, LeaseS: 20, RenewS: 5}, nil
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
