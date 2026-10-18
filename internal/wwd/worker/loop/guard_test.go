package loop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
	"github.com/wake/purdex/internal/wwd/worker/quarantine"
	"github.com/wake/purdex/internal/wwd/worker/runner"
)

// guarded is a quarantine rig whose runner has the guard over the real store.
func guarded(t *testing.T, conns ...*fakeConn) *qrig {
	t.Helper()
	q := newQRig(t, conns...)
	g := NewGuard(q.store, true, time.Now, func(string, ...any) {})
	q.loop.cfg.Ctl = runner.New(runner.Config{Worker: "mlab", Locks: q.tbl, Exec: q.ex, Guard: g, Unit: unit})
	return q
}

func TestAMarkerExistsWhileASandboxedCommandRunsAndIsGoneWhenItEndedCleanly(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0, "mysql"))
	q := guarded(t, c)
	release := make(chan struct{})
	q.ex.run = func(ctx context.Context, _ wire.Job) run.Result {
		<-release
		return run.Result{Outcome: run.Exited}
	}
	q.round()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() == 1 })
	held := q.store.Held()
	if len(held) != 1 || held[0].Owner != "mlab/a" || held[0].Unit != "wwd-a" || held[0].Dir != filepath.Join(q.root, "a") || len(held[0].Keys) != 1 {
		t.Fatalf("held %+v: the marker is there before the command can end", held)
	}
	if files, _ := os.ReadDir(q.mdir); len(files) != 1 {
		t.Fatalf("marker files %v", files)
	}
	close(release)
	waitFor(t, "the result", func() bool { _, _, n := c.seen(); return n == 1 })
	waitFor(t, "the marker to go", func() bool { return len(q.store.Held()) == 0 })
	if files, _ := os.ReadDir(q.mdir); len(files) != 0 {
		t.Fatalf("marker files %v: a command that ended with its unit proven gone leaves nothing", files)
	}
}

func TestADaemonThatDiesWhileACommandRunsLeavesItsKeysAndWorkspaceToTheNextOne(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0, "mysql"))
	q := guarded(t, c)
	q.ex.run = blockUntilDone
	q.round()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() == 1 })

	// the next process: nothing in memory, the same marker directory and work root, and the unit still going
	tbl2 := locks.New()
	store2, err := quarantine.Open(q.mdir, tbl2, q.units, time.Now, quarantine.WithWorkRoot(q.root))
	if err != nil {
		t.Fatal(err)
	}
	if err := store2.Restore(); err != nil {
		t.Fatal(err)
	}
	if owner, ok := tbl2.Holder("mysql"); !ok || owner != "mlab/a" {
		t.Fatalf("holder %q %v: the keys of the job that was running come back", owner, ok)
	}
	if n, _ := store2.Sweep(context.Background()); n != 0 {
		t.Fatal("the unit is still running: nothing is cleared")
	}
	if _, err := os.Stat(filepath.Join(q.root, "a")); err != nil {
		t.Fatalf("the workspace stays while the unit runs: %v", err)
	}
	q.units.mu.Lock()
	q.units.gone["wwd-a"] = true
	q.units.mu.Unlock()
	if n, err := store2.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("cleared %d err %v", n, err)
	}
	if _, ok := tbl2.Holder("mysql"); ok {
		t.Fatal("the unit is gone: the keys are free")
	}
	if _, err := os.Stat(filepath.Join(q.root, "a")); !os.IsNotExist(err) {
		t.Fatalf("the workspace goes with it: %v", err)
	}
	q.loop.stopAll(context.Background(), "test")
}

func TestARunningJobsMarkerIsNotSweptEvenIfItsUnitIsNotThereYet(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0, "mysql"))
	q := guarded(t, c)
	q.ex.run = blockUntilDone
	q.units.mu.Lock()
	q.units.gone["wwd-a"] = true // no unit yet / already gone: absent
	q.units.mu.Unlock()
	q.round()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() == 1 })
	q.loop.Sweep(context.Background())
	if _, ok := q.tbl.Holder("mysql"); !ok || len(q.store.Held()) != 1 {
		t.Fatal("a job that is running in this process is not cleared by a sweep")
	}
	if _, err := os.Stat(filepath.Join(q.root, "a")); err != nil {
		t.Fatalf("its workspace stays: %v", err)
	}
	q.loop.stopAll(context.Background(), "test")
}

func TestAQuarantinedRunKeepsItsMarkerThroughTheGuardAndTheFinish(t *testing.T) {
	c := newConn("mlab", qjob("stuck", wire.ClassDev, 0, "mysql"))
	q := guarded(t, c)
	q.ex.run = notProven
	q.round()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() == 1 })
	q.loop.stopAll(context.Background(), "test")
	waitFor(t, "the release", func() bool { _, rel, _ := c.seen(); return len(rel) == 1 })
	if len(q.store.Held()) != 1 {
		t.Fatal("the marker stays: the unit was not proven stopped")
	}
	q.loop.Sweep(context.Background())
	if len(q.store.Held()) != 1 {
		t.Fatal("not gone yet")
	}
	q.units.mu.Lock()
	q.units.gone["wwd-stuck"] = true
	q.units.mu.Unlock()
	q.loop.Sweep(context.Background())
	if len(q.store.Held()) != 0 {
		t.Fatal("once its run has ended (Finish settled it) a sweep clears it when the unit is gone")
	}
}

type failingMarker struct{}

func (failingMarker) Begin(quarantine.Entry) error { return errors.New("disk full") }
func (failingMarker) Discard(string) error         { return nil }

func TestACommandIsNotStartedWhenItsMarkerCannotBeWritten(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0))
	q := newQRig(t, c)
	q.loop.cfg.Ctl = runner.New(runner.Config{Worker: "mlab", Locks: q.tbl, Exec: q.ex, Guard: NewGuard(failingMarker{}, true, time.Now, func(string, ...any) {}), Unit: unit})
	q.round()
	waitFor(t, "the release", func() bool { _, rel, _ := c.seen(); return len(rel) == 1 })
	if q.ex.ran.Load() != 0 {
		t.Fatal("no marker, no unit")
	}
	if _, rel, _ := c.seen(); rel[0].Reason != wire.ReleaseInfra {
		t.Fatalf("release %+v", rel[0])
	}
}

func TestNoGuardOnAHostWithoutSandboxUnitsOrWithoutAStore(t *testing.T) {
	q := newQRig(t, newConn("mlab"))
	if g := NewGuard(q.store, false, time.Now, nil); g != nil {
		t.Fatalf("guard %v: a marker nothing could ever clear would hold the keys for good", g)
	}
	if g := NewGuard(nil, true, time.Now, nil); g != nil {
		t.Fatalf("guard %v", g)
	}
}

func TestGuardEndKeepsTheMarkerOfAQuarantinedRunAndDropsOtherwise(t *testing.T) {
	q := newQRig(t, newConn("mlab"))
	g := NewGuard(q.store, true, time.Now, nil)
	e := runner.Ended{Source: "mlab", Job: qjob("a", wire.ClassDev, 0, "mysql"), ClaimID: claimID, Root: filepath.Join(q.root, "a", "ws")}
	if err := g.Begin(e); err != nil {
		t.Fatal(err)
	}
	g.End(e, true)
	if len(q.store.Held()) != 1 {
		t.Fatal("a quarantined run's marker survives End: it is the only record until Finish settles it")
	}
	g.End(e, false)
	if len(q.store.Held()) != 0 {
		t.Fatal("a run whose unit is proven gone leaves no marker")
	}
}
