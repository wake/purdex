package runner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
)

func TestStartReturnsOnceTheClaimIsTakenAndTheJobGoesOn(t *testing.T) {
	ex := &fakeExec{run: blockUntilDone}
	c, _ := newCtl(ex)
	r := c.Start(context.Background(), &fakeOrigin{}, scored(testJob("j")), nil)
	if !r.Claimed() || r.ID() != "j" || r.Source() != "mlab" || r.ClaimID() != claimID {
		t.Fatalf("claimed=%v id=%s source=%s claim=%s", r.Claimed(), r.ID(), r.Source(), r.ClaimID())
	}
	select {
	case <-r.Done():
		t.Fatal("the job runs in the background: Start must not wait for it")
	default:
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStartOfAJobThatWasNotTakenIsDoneAtOnce(t *testing.T) {
	ex := &fakeExec{}
	o := &fakeOrigin{claim: func(string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{}, &Conflict{State: wire.StateQueued}
	}}
	c, tbl := newCtl(ex)
	r := c.Start(context.Background(), o, scored(testJob("j", "mysql")), nil)
	select {
	case <-r.Done():
	default:
		t.Fatal("a refused claim is over when Start returns")
	}
	e := r.Ended()
	var conf *Conflict
	if r.Claimed() || e.Kind != KindSkipped || !errors.As(e.Err, &conf) || tbl.Owners() != 0 {
		t.Fatalf("claimed=%v kind=%s err=%v", r.Claimed(), e.Kind, e.Err)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("stopping what never started is not an error: %v", err)
	}
}

func TestStopEndsTheCommandKeepsTheLeaseAndNothingIsDelivered(t *testing.T) {
	ex := &fakeExec{run: blockUntilDone}
	o := &fakeOrigin{}
	o.claim = func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: claimed(id), ClaimID: claimID, LeaseS: 5, RenewS: 1}, nil
	}
	c, _ := newCtl(ex)
	var mu sync.Mutex
	var finishKind Kind
	var finishAlive bool
	r := c.Start(context.Background(), o, scored(testJob("j")), func(ctx context.Context, e Ended) {
		mu.Lock()
		finishKind, finishAlive = e.Kind, ctx.Err() == nil
		mu.Unlock()
	})
	time.Sleep(2 * unit)
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := r.Ended()
	mu.Lock()
	defer mu.Unlock()
	if e.Kind != KindReclaimed || finishKind != KindReclaimed || !finishAlive {
		t.Fatalf("kind=%s finish=%s alive=%v: the lease must be alive for the caller's release", e.Kind, finishKind, finishAlive)
	}
	if e.Run.Outcome != run.Cancelled {
		t.Fatalf("run %+v", e.Run)
	}
}

func TestStopBeforeTheCommandStartsMeansItNeverRuns(t *testing.T) {
	ex := &fakeExec{}
	gate := make(chan struct{})
	ex.prepare = func() (Plan, error) { <-gate; return Plan{Argv: []string{"x"}}, nil }
	o := &fakeOrigin{}
	c, _ := newCtl(ex)
	r := c.Start(context.Background(), o, scored(testJob("j")), nil)
	stopped := make(chan error, 1)
	go func() { stopped <- r.Stop(context.Background()) }()
	time.Sleep(2 * unit)
	close(gate)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if _, st, _ := o.count(); r.Ended().Kind != KindReclaimed || ex.ran.Load() != 0 || st != 0 {
		t.Fatalf("kind=%s ran=%d starts=%d", r.Ended().Kind, ex.ran.Load(), st)
	}
}

func TestStopOfAQuarantinedJobIsAnErrorSoItIsNotGivenBack(t *testing.T) {
	started := make(chan struct{})
	ex := &fakeExec{run: func(ctx context.Context) run.Result {
		close(started)
		<-ctx.Done()
		return run.Result{Outcome: run.Cancelled, Err: errors.New("could not confirm the unit stopped")}
	}}
	c, tbl := newCtl(ex)
	r := c.Start(context.Background(), &fakeOrigin{}, scored(testJob("j", "mysql")), nil)
	<-started
	if err := r.Stop(context.Background()); err == nil {
		t.Fatal("a job that cannot be proven stopped must not look stopped")
	}
	if !r.Ended().Quarantined || ex.cleaned.Load() != 0 {
		t.Fatalf("ended %+v cleaned %d", r.Ended(), ex.cleaned.Load())
	}
	if _, held := tbl.Holder("mysql"); !held {
		t.Fatal("the lock keys must stay held")
	}
}

func TestStopGivesUpWhenItsContextEnds(t *testing.T) {
	release, started := make(chan struct{}), make(chan struct{})
	ex := &fakeExec{run: func(ctx context.Context) run.Result {
		close(started)
		<-release
		return run.Result{Outcome: run.Exited}
	}}
	c, _ := newCtl(ex)
	r := c.Start(context.Background(), &fakeOrigin{}, scored(testJob("j")), nil)
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 3*unit)
	defer cancel()
	if err := r.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v: a command that does not end must not hold the caller for ever", err)
	}
	close(release)
	<-r.Done()
}

func TestAStopAfterTheJobIsOverIsFine(t *testing.T) {
	c, _ := newCtl(&fakeExec{})
	r := c.Start(context.Background(), &fakeOrigin{}, scored(testJob("j")), nil)
	<-r.Done()
	if err := r.Stop(context.Background()); err != nil || r.Ended().Kind != KindRan {
		t.Fatalf("err=%v kind=%s", err, r.Ended().Kind)
	}
}

func TestACancelAfterAReclaimIsTheEnding(t *testing.T) {
	// the origin's cancel is final (a cancelled result is posted); a reclaim is only the owner wanting the machine
	started := make(chan struct{})
	ex := &fakeExec{run: func(ctx context.Context) run.Result {
		close(started)
		<-ctx.Done()
		time.Sleep(4 * unit)
		return run.Result{Outcome: run.Cancelled}
	}}
	o := &fakeOrigin{heartbeat: func(n int) (wire.HeartbeatResponse, error) {
		return wire.HeartbeatResponse{LeaseS: 30, RenewS: 1, Cancel: n >= 2}, nil
	}}
	o.claim = func(id string) (wire.ClaimResponse, error) {
		return wire.ClaimResponse{Job: claimed(id), ClaimID: claimID, LeaseS: 30, RenewS: 1}, nil
	}
	c, _ := newCtl(ex)
	r := c.Start(context.Background(), o, scored(testJob("j")), nil)
	<-started
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if k := r.Ended().Kind; k != KindCancelled {
		t.Fatalf("kind=%s", k)
	}
}

func TestALostLeaseAfterAReclaimStaysLost(t *testing.T) {
	started := make(chan struct{})
	ex := &fakeExec{run: func(ctx context.Context) run.Result {
		close(started)
		<-ctx.Done()
		time.Sleep(5 * unit)
		return run.Result{Outcome: run.Cancelled}
	}}
	o := &fakeOrigin{heartbeat: func(n int) (wire.HeartbeatResponse, error) {
		return wire.HeartbeatResponse{}, &Conflict{State: wire.StateQueued}
	}}
	c, _ := newCtl(ex)
	r := c.Start(context.Background(), o, scored(testJob("j")), nil)
	<-started
	_ = r.Stop(context.Background())
	if k := r.Ended().Kind; k != KindLost {
		t.Fatalf("kind=%s: nothing may be given back once the origin has the job again", k)
	}
}

type fakeGuard struct {
	mu       sync.Mutex
	begin    func() error
	events   []string
	quar     []bool
	ranAtEnd int32
	ex       *fakeExec
}

func (g *fakeGuard) Begin(e Ended) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = append(g.events, "begin:"+e.Job.ID+":"+e.Root+":"+e.ClaimID)
	if g.begin != nil {
		return g.begin()
	}
	return nil
}

func (g *fakeGuard) End(e Ended, quarantined bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = append(g.events, "end")
	g.quar = append(g.quar, quarantined)
	if g.ex != nil {
		g.ranAtEnd = g.ex.ran.Load()
	}
}

func newGuarded(ex Exec, g Guard) (*Controller, *locks.Table) {
	t := locks.New()
	return New(Config{Worker: "mlab", Locks: t, Exec: ex, Guard: g, Unit: unit}), t
}

func TestTheGuardWrapsTheRunBeginBeforeAndEndAfter(t *testing.T) {
	ex := &fakeExec{}
	g := &fakeGuard{ex: ex}
	c, _ := newGuarded(ex, g)
	if e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j")), nil); e.Kind != KindRan {
		t.Fatalf("kind=%s", e.Kind)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.events) != 2 || g.events[0] != "begin:j:/ws:"+claimID || g.events[1] != "end" || g.quar[0] {
		t.Fatalf("events %v quarantined %v", g.events, g.quar)
	}
	if g.ranAtEnd != 1 {
		t.Fatal("End comes after the command has run")
	}
}

func TestTheGuardIsToldWhenTheRunCouldNotBeProvenStopped(t *testing.T) {
	ex := &fakeExec{run: func(ctx context.Context) run.Result {
		return run.Result{Outcome: run.Exited, Err: errors.New("unit not proven stopped")}
	}}
	g := &fakeGuard{}
	c, tbl := newGuarded(ex, g)
	e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j", "mysql")), nil)
	g.mu.Lock()
	defer g.mu.Unlock()
	if !e.Quarantined || len(g.quar) != 1 || !g.quar[0] {
		t.Fatalf("quarantined=%v guard %v", e.Quarantined, g.quar)
	}
	if _, held := tbl.Holder("mysql"); !held {
		t.Fatal("the hold stays")
	}
}

func TestAStartFailedRunIsNotQuarantinedForTheGuardEither(t *testing.T) {
	ex := &fakeExec{run: func(ctx context.Context) run.Result {
		return run.Result{Outcome: run.StartFailed, Err: errors.New("not found")}
	}}
	g := &fakeGuard{}
	c, _ := newGuarded(ex, g)
	c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j")), nil)
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.quar) != 1 || g.quar[0] {
		t.Fatalf("guard %v: a command that never started has nothing to keep", g.quar)
	}
}

func TestACommandIsNotStartedWhenTheGuardCannotWriteItsMarker(t *testing.T) {
	ex := &fakeExec{}
	g := &fakeGuard{begin: func() error { return errors.New("disk full") }}
	c, tbl := newGuarded(ex, g)
	e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j", "mysql")), nil)
	g.mu.Lock()
	defer g.mu.Unlock()
	if e.Kind != KindInfra || ex.ran.Load() != 0 || e.Quarantined || len(g.quar) != 0 {
		t.Fatalf("kind=%s ran=%d quarantined=%v end=%v: no marker, no unit", e.Kind, ex.ran.Load(), e.Quarantined, g.quar)
	}
	if tbl.Owners() != 0 {
		t.Fatal("nothing ran: the keys are free")
	}
}

func TestAPanickingRunLeavesTheGuardsMarkerAndTheHold(t *testing.T) {
	ex := &fakeExec{run: func(ctx context.Context) run.Result { panic("boom") }}
	g := &fakeGuard{}
	c, tbl := newGuarded(ex, g)
	func() {
		defer func() { _ = recover() }()
		c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j", "mysql")), nil)
	}()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.events) != 1 {
		t.Fatalf("events %v: End is not called after a panic, so what Begin wrote stays", g.events)
	}
	if _, held := tbl.Holder("mysql"); !held {
		t.Fatal("the hold stays")
	}
}

func TestNoGuardNoCalls(t *testing.T) {
	c, _ := newCtl(&fakeExec{})
	if e := c.Work(context.Background(), &fakeOrigin{}, scored(testJob("j")), nil); e.Kind != KindRan {
		t.Fatalf("kind=%s", e.Kind)
	}
}

func TestACommandIsNotStartedForAJobThatWasStoppedBeforeTheGuardWasAsked(t *testing.T) {
	ex := &fakeExec{prepare: func() (Plan, error) { time.Sleep(3 * unit); return Plan{Argv: []string{"x"}}, nil }}
	g := &fakeGuard{}
	c, _ := newGuarded(ex, g)
	r := c.Start(context.Background(), &fakeOrigin{}, scored(testJob("j")), nil)
	_ = r.Stop(context.Background())
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.events) != 0 || ex.ran.Load() != 0 {
		t.Fatalf("events %v ran %d: a job stopped before its run writes no marker", g.events, ex.ran.Load())
	}
}
