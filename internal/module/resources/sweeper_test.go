package resourcesmod

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/resources"
)

// fakeView is the process table the sweeper judges holders against.
type fakeView struct {
	procs    map[int]time.Time // pid -> start
	startErr map[int]error     // pid -> Start fails (the pid is still alive)
}

func (v *fakeView) Alive(pid int) bool {
	_, ok := v.procs[pid]
	return ok
}

func (v *fakeView) Start(pid int) (time.Time, error) {
	if err := v.startErr[pid]; err != nil {
		return time.Time{}, err
	}
	t, ok := v.procs[pid]
	if !ok {
		return time.Time{}, iagent.ErrNotInSnapshot
	}
	return t, nil
}

// sweepFix is a module on a real resources.db with a fixed clock, a settable
// process table and a counter of the snapshots the sweeper took.
type sweepFix struct {
	t     *testing.T
	m     *Module
	clock *fakeClock
	set   *fakeSettings
	logs  *logSink
	view  *fakeView
	// snapErr, when set, makes the process snapshot fail.
	snapErr error
	snaps   atomic.Int64
}

// procStart is when the fixture's holders started: an hour before the clock.
var procStart = time.Date(2026, 10, 9, 11, 0, 0, 0, time.UTC)

// startText is a start time as ps prints it: the machine's local clock.
func startText(t time.Time) string { return t.In(time.Local).Format(ipeers.ProcStartLayout) }

func newSweepFix(t *testing.T, mode string) *sweepFix {
	t.Helper()
	f := &sweepFix{t: t, clock: newFakeClock(), view: &fakeView{procs: map[int]time.Time{}, startErr: map[int]error{}}}
	f.set = &fakeSettings{}
	f.set.set(resources.Settings{Mode: mode})
	f.m, f.logs = initedModule(t, t.TempDir(), f.set, idleSampler())
	f.m.now = f.clock.now
	// The first sample has been published: from then on holders are judged.
	// TestSweeper_NoLivenessBeforeFirstSample clears it again.
	f.m.publish(&resources.Snapshot{Available: true, Capacity: resources.Capacity, Mode: mode})
	f.m.sweepView = func(context.Context) (procView, error) {
		f.snaps.Add(1)
		if f.snapErr != nil {
			return nil, f.snapErr
		}
		return f.view, nil
	}
	return f
}

func (f *sweepFix) nowMS() int64 { return f.clock.now().UnixMilli() }

// waiting creates a waiting row whose lease runs until leaseUntil.
func (f *sweepFix) waiting(id string, leaseUntil int64) {
	f.t.Helper()
	r := baseRow(id, "c-"+id)
	r.CreatedAt, r.DeadlineAt, r.LeaseUntil = f.nowMS()-60000, f.nowMS()+300000, leaseUntil
	mustCreate(f.t, f.m.store, r)
}

// held creates a row granted at grantedAt for a holder pid with the start text.
func (f *sweepFix) held(id string, pid int, start string, grantedAt int64) {
	f.t.Helper()
	r := baseRow(id, "c-"+id)
	r.HolderPID, r.HolderStart = pid, start
	r.CreatedAt, r.DeadlineAt, r.LeaseUntil = grantedAt-1000, grantedAt+300000, grantedAt+30000
	mustCreate(f.t, f.m.store, r)
	if ok, err := f.m.store.Grant(id, grantedAt, false, false); !ok || err != nil {
		f.t.Fatalf("grant %s: %v %v", id, ok, err)
	}
}

// alive puts pid in the process table with the start time.
func (f *sweepFix) alive(pid int, start time.Time) { f.view.procs[pid] = start }

func (f *sweepFix) row(id string) leaseRow {
	f.t.Helper()
	r, ok, err := f.m.store.Get(id)
	if err != nil || !ok {
		f.t.Fatalf("get %s: ok=%v err=%v", id, ok, err)
	}
	return r
}

func (f *sweepFix) sweep() { f.m.sweepOnce(context.Background()) }

func (f *sweepFix) wantEnded(id, reason string) {
	f.t.Helper()
	if r := f.row(id); r.State != resources.StateEnded || r.EndReason != reason {
		f.t.Fatalf("%s = %s/%q, want ended/%q", id, r.State, r.EndReason, reason)
	}
}

func (f *sweepFix) wantState(id, state string) {
	f.t.Helper()
	if r := f.row(id); r.State != state {
		f.t.Fatalf("%s = %s/%q, want %s", id, r.State, r.EndReason, state)
	}
}

func TestSweeper_AbandonedWaiter(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.waiting("gone", f.nowMS()-1)
	f.waiting("exact", f.nowMS()) // lease_until <= now: expired
	f.waiting("polled", f.nowMS()+30000)
	f.sweep()
	f.wantEnded("gone", resources.EndAbandoned)
	f.wantEnded("exact", resources.EndAbandoned)
	f.wantState("polled", resources.StateWaiting)
	if r := f.row("gone"); r.EndedAt != f.nowMS() || r.WaitedMS != 60000 {
		t.Fatalf("gone = ended_at %d waited_ms %d", r.EndedAt, r.WaitedMS)
	}
}

func TestSweeper_RenewLosesNever(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.waiting("w", f.nowMS()-1)
	// The poll that renews the lease lands after the sweeper read the row and
	// before its close: the close must lose.
	f.m.sweepHook = func(r leaseRow) {
		if err := f.m.store.RenewLease(r.ID, f.nowMS()+30000); err != nil {
			t.Error(err)
		}
	}
	ch := f.m.genChan()
	f.sweep()
	f.wantState("w", resources.StateWaiting)
	if closed(ch) {
		t.Fatal("a lost close must not wake the pollers")
	}
	if f.logs.count("sweep") != 0 {
		t.Fatalf("a lost close is not an error: %v", f.logs.lines)
	}
}

func TestSweeper_HolderGone(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.held("dead", 4242, startText(procStart), f.nowMS()-10000)
	f.held("live", 4343, startText(procStart), f.nowMS()-10000)
	f.alive(4343, procStart)
	f.sweep()
	f.wantEnded("dead", resources.EndHolderGone)
	f.wantState("live", resources.StateHeld)
	if f.snaps.Load() != 1 {
		t.Fatalf("snapshots = %d, want one per sweep", f.snaps.Load())
	}
}

func TestSweeper_HolderStartMismatchIsGone(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.held("reused", 4242, startText(procStart), f.nowMS()-10000)
	f.alive(4242, procStart.Add(time.Hour)) // another process took the pid
	f.sweep()
	f.wantEnded("reused", resources.EndHolderGone)
}

func TestSweeper_SecondPrecisionStart(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.held("same", 1001, startText(procStart), f.nowMS()-10000)
	f.alive(1001, procStart.Add(900*time.Millisecond)) // same second
	f.held("next", 1002, startText(procStart), f.nowMS()-10000)
	f.alive(1002, procStart.Add(time.Second)) // the next second
	f.sweep()
	f.wantState("same", resources.StateHeld)
	f.wantEnded("next", resources.EndHolderGone)
}

func TestSweeper_UnknownIsAlive(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.held("no-start-text", 1001, "", f.nowMS()-10000)
	f.alive(1001, procStart)
	f.held("bad-start-text", 1002, "not a time", f.nowMS()-10000)
	f.alive(1002, procStart)
	f.held("no-start-text-no-pid", 1003, "", f.nowMS()-10000) // nothing vouches either way
	f.held("start-unreadable", 1004, startText(procStart), f.nowMS()-10000)
	f.alive(1004, procStart)
	f.view.startErr[1004] = errors.New("ps: no start time")
	f.held("no-pid", 0, startText(procStart), f.nowMS()-10000)
	f.sweep()
	for _, id := range []string{"no-start-text", "bad-start-text", "no-start-text-no-pid", "start-unreadable", "no-pid"} {
		f.wantState(id, resources.StateHeld)
	}

	// A snapshot that cannot be taken judges nobody.
	f.held("dead-but-unseen", 4242, startText(procStart), f.nowMS()-10000)
	f.snapErr = errors.New("ps failed")
	f.sweep()
	f.wantState("dead-but-unseen", resources.StateHeld)
}

func TestSweeper_NoLivenessWithoutSnapshot(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.held("h1", 4242, startText(procStart), f.nowMS()-10000)
	f.held("h2", 4343, startText(procStart), f.nowMS()-10000)
	f.snapErr = errors.New("ps failed")
	for i := 0; i < 3; i++ {
		f.sweep()
	}
	f.wantState("h1", resources.StateHeld)
	f.wantState("h2", resources.StateHeld)
	if got := f.logs.count("process table"); got != 1 {
		t.Fatalf("a standing snapshot failure must log once, logged %d: %v", got, f.logs.lines)
	}
	f.snapErr = nil
	f.sweep() // the table is readable and the holders are not in it
	f.wantEnded("h1", resources.EndHolderGone)
	if f.logs.count("readable again") != 1 {
		t.Fatalf("the recovery must be logged: %v", f.logs.lines)
	}
}

// Codex attack (high): after a restart the sweeper's first tick can come
// before the sampler has published anything; plan Task 1.5 judges holders
// only after the first sample. Until then even a holder missing from a
// readable table is left alone, and no table is read for it.
func TestSweeper_NoLivenessBeforeFirstSample(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.m.latest.Store(nil) // the sampler has published nothing yet
	f.held("h1", 4242, startText(procStart), f.nowMS()-10000)
	for i := 0; i < 3; i++ {
		f.sweep()
	}
	f.wantState("h1", resources.StateHeld)
	if f.snaps.Load() != 0 {
		t.Fatalf("the process table was read %d times before the first sample", f.snaps.Load())
	}
	f.m.publish(&resources.Snapshot{Available: true, Capacity: resources.Capacity})
	f.sweep()
	f.wantEnded("h1", resources.EndHolderGone)
}

// Codex R1 + attack (high): the process table is read before stateMu is
// taken, so a read that stalls cannot hold the lock every lease operation
// needs (D-5: they must still make progress).
func TestSweeper_ProcessTableIsReadOutsideStateMu(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.held("h1", 4242, startText(procStart), f.nowMS()-10000)
	f.alive(4242, procStart)
	f.m.sweepView = func(context.Context) (procView, error) {
		f.snaps.Add(1)
		if !f.m.stateMu.TryLock() {
			t.Error("stateMu is held while the process table is read")
		} else {
			f.m.stateMu.Unlock()
		}
		return f.view, nil
	}
	f.sweep()
	if f.snaps.Load() != 1 {
		t.Fatalf("the table was read %d times, want once", f.snaps.Load())
	}
	f.wantState("h1", resources.StateHeld)
}

// The table is read before the rows are judged, so a holder granted after the
// read began may not be in it yet: it is not judged by that table. (The
// reason the first version read the table under the lock.)
func TestSweeper_HolderGrantedAfterTheTableIsReadIsNotJudgedByIt(t *testing.T) {
	// 0 ms is the boundary (re-review P1): a grant in the same millisecond the
	// read began cannot be told from one before it, so it is not judged either.
	for _, advance := range []int64{0, 1, 5} {
		t.Run(fmt.Sprintf("granted %d ms after the read began", advance), func(t *testing.T) {
			f := newSweepFix(t, resources.ModeLease)
			f.held("old", 4242, startText(procStart), f.nowMS()-10000) // makes the sweeper read the table
			f.alive(4242, procStart)
			f.m.sweepView = func(context.Context) (procView, error) {
				f.snaps.Add(1)
				f.clock.ms.Add(advance) // a holder is granted; the table predates it
				f.held("new", 5151, startText(procStart), f.nowMS())
				return f.view, nil
			}
			f.sweep()
			f.wantState("old", resources.StateHeld)
			f.wantState("new", resources.StateHeld)
		})
	}
}

func TestSweeper_DefaultViewReadsProcSnapshot(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.m.sweepView = nil
	var calls atomic.Int64
	var snapErr error
	f.m.procSnapshot = func(context.Context) (*iagent.ProcessSnapshot, error) {
		calls.Add(1)
		if snapErr != nil {
			return nil, snapErr
		}
		return &iagent.ProcessSnapshot{}, nil // an empty table: nobody is alive
	}
	f.held("h", 4242, startText(procStart), f.nowMS()-10000)
	snapErr = errors.New("ps failed")
	f.sweep()
	f.wantState("h", resources.StateHeld)
	snapErr = nil
	f.sweep()
	f.wantEnded("h", resources.EndHolderGone)
	if calls.Load() != 2 {
		t.Fatalf("procSnapshot calls = %d, want 2", calls.Load())
	}
}

func TestSweeper_MaxHold(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	hold := 120
	f.set.set(resources.Settings{Mode: resources.ModeLease, MaxHoldS: &hold})
	f.held("old", 1001, startText(procStart), f.nowMS()-120000) // exactly max_hold
	f.held("young", 1002, startText(procStart), f.nowMS()-119000)
	f.alive(1001, procStart)
	f.alive(1002, procStart)
	f.sweep()
	f.wantEnded("old", resources.EndExpired)
	f.wantState("young", resources.StateHeld)
	// The setting is read on every tick.
	hold = 60
	f.set.set(resources.Settings{Mode: resources.ModeLease, MaxHoldS: &hold})
	f.sweep()
	f.wantEnded("young", resources.EndExpired)
}

func TestSweeper_NoSnapshotWithoutHeldLeases(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	var calls atomic.Int64
	f.m.sweepView = nil
	f.m.procSnapshot = func(context.Context) (*iagent.ProcessSnapshot, error) {
		calls.Add(1)
		return &iagent.ProcessSnapshot{}, nil
	}
	f.waiting("w", f.nowMS()+30000)
	for i := 0; i < 3; i++ {
		f.sweep()
	}
	if calls.Load() != 0 {
		t.Fatalf("procSnapshot called %d times with no held lease", calls.Load())
	}
	f.held("h", 4242, startText(procStart), f.nowMS()-10000)
	f.sweep()
	if calls.Load() != 1 {
		t.Fatalf("procSnapshot calls = %d with a held lease, want 1", calls.Load())
	}
}

func TestSweeper_ModeOffAndMeasureDoNotEnd(t *testing.T) {
	f := newSweepFix(t, resources.ModeOff)
	f.waiting("w", f.nowMS()-1)
	f.held("h", 4242, startText(procStart), f.nowMS()-10000)
	old := baseRow("old", "c-old")
	old.CreatedAt = 1000
	mustCreate(t, f.m.store, old)
	if ok, err := f.m.store.End("old", resources.EndReleased, f.nowMS()-15*24*3600*1000); !ok || err != nil {
		t.Fatal(ok, err)
	}
	ch := f.m.genChan()
	for _, mode := range []string{resources.ModeOff, resources.ModeMeasure} {
		f.set.set(resources.Settings{Mode: mode})
		f.sweep()
		f.wantState("w", resources.StateWaiting)
		f.wantState("h", resources.StateHeld)
		if f.snaps.Load() != 0 {
			t.Fatalf("mode %s took a snapshot", mode)
		}
		if closed(ch) {
			t.Fatalf("mode %s woke the pollers", mode)
		}
	}
	// Retention still runs: it is housekeeping, not a judgement.
	if _, ok, _ := f.m.store.Get("old"); ok {
		t.Fatal("the retention prune did not run in mode off/measure")
	}
	// Back to lease: the rows are judged then.
	f.set.set(resources.Settings{Mode: resources.ModeLease})
	f.sweep()
	f.wantEnded("w", resources.EndAbandoned)
	f.wantEnded("h", resources.EndHolderGone)
}

func TestSweeper_AdviseModeEnds(t *testing.T) {
	f := newSweepFix(t, resources.ModeAdvise)
	f.held("h", 4242, startText(procStart), f.nowMS()-10000)
	f.sweep()
	f.wantEnded("h", resources.EndHolderGone)
}

func TestSweeper_PruneHourly(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	addOld := func(id string) {
		r := baseRow(id, "c-"+id)
		r.CreatedAt = 1000
		mustCreate(t, f.m.store, r)
		if ok, err := f.m.store.End(id, resources.EndReleased, f.nowMS()-retention.Milliseconds()-1000); !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	exists := func(id string) bool {
		_, ok, err := f.m.store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	addOld("a")
	f.sweep()
	if exists("a") {
		t.Fatal("the first sweep must prune")
	}
	addOld("b")
	f.clock.ms.Add(59 * 60 * 1000)
	f.sweep()
	if !exists("b") {
		t.Fatal("pruned again inside the hour")
	}
	f.clock.ms.Add(60 * 1000)
	f.sweep()
	if exists("b") {
		t.Fatal("not pruned after an hour")
	}
	// A row inside the retention is never pruned.
	r := baseRow("fresh", "c-fresh")
	r.CreatedAt = 1000
	mustCreate(t, f.m.store, r)
	if ok, err := f.m.store.End("fresh", resources.EndReleased, f.nowMS()-1000); !ok || err != nil {
		t.Fatal(ok, err)
	}
	f.clock.ms.Add(3600 * 1000)
	f.sweep()
	if !exists("fresh") {
		t.Fatal("a row inside the retention was pruned")
	}
}

func TestSweeper_EndWakesPollers(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.held("live", 1001, startText(procStart), f.nowMS()-10000)
	f.alive(1001, procStart)
	ch := f.m.genChan()
	f.sweep() // nothing to end
	if closed(ch) {
		t.Fatal("a sweep that ended nothing woke the pollers")
	}
	f.waiting("w", f.nowMS()-1)
	f.sweep()
	if !closed(ch) {
		t.Fatal("ending a waiter did not wake the pollers")
	}
	ch = f.m.genChan()
	f.held("dead", 4242, startText(procStart), f.nowMS()-10000)
	f.sweep()
	if !closed(ch) {
		t.Fatal("ending a holder did not wake the pollers")
	}
}

func TestSweeper_EndedByOtherWriterIsNotAnError(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.waiting("w", f.nowMS()-1)
	f.held("h", 4242, startText(procStart), f.nowMS()-10000)
	// Someone else (a release, a cancel) ends each row between the sweeper's
	// read and its own close.
	f.m.sweepHook = func(r leaseRow) {
		if ok, err := f.m.store.End(r.ID, resources.EndReleased, f.nowMS()); !ok || err != nil {
			t.Errorf("other writer: %v %v", ok, err)
		}
	}
	f.sweep()
	f.wantEnded("w", resources.EndReleased)
	f.wantEnded("h", resources.EndReleased)
	if f.logs.count("sweep") != 0 || f.logs.count("ended") != 0 {
		t.Fatalf("a lost race must be silent: %v", f.logs.lines)
	}
}

func TestSweeper_StopJoins(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.m.sweepEvery = time.Millisecond
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.m.sweepHook = func(leaseRow) {
		once.Do(func() { close(entered) })
		<-release
	}
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.waiting("w", f.nowMS()-1) // after Start: the boot grace would have extended its lease
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the sweeper never ran")
	}
	// The sweeper is inside a sweep: Stop must wait for it, not return early.
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := f.m.Stop(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop returned %v while the sweeper was still running", err)
	}
	close(release)
	if err := f.m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after the sweeper was released: %v", err)
	}
	if err := f.m.Start(context.Background()); !errors.Is(err, errStopped) {
		t.Fatalf("Start after Stop = %v, want errStopped", err)
	}
}

func TestSweeper_RunsAfterStartAndEndsRows(t *testing.T) {
	f := newSweepFix(t, resources.ModeLease)
	f.m.sweepEvery = time.Millisecond
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.waiting("w", f.nowMS()-1) // after Start: the boot grace would have extended its lease
	waitFor(t, "the sweeper to abandon the waiter", func() bool {
		r, ok, _ := f.m.store.Get("w")
		return ok && r.State == resources.StateEnded
	})
	f.wantEnded("w", resources.EndAbandoned)
}
