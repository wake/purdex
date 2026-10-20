package wwdalerts

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// An episode that waited behind a running call and cleared meanwhile is not told when the call ends.
func TestAQueuedEpisodeThatClearedIsNotSent(t *testing.T) {
	e, w, _ := wakeEnv(t)
	w.block = make(chan struct{})
	w.entered = make(chan struct{}, 8)
	e.baseline(t)
	first := alertOf("first", wire.AlertThrash, wire.AlertDigest, t0, "x")
	e.src.set("mlab", first)
	e.pass(t)
	<-w.entered // the first call is running
	e.src.set("mlab", first, alertOf("second", wire.AlertOrphans, wire.AlertDigest, t0, "later"))
	e.pass(t) // the second is queued behind it
	e.src.set("mlab", first)
	e.pass(t) // and clears while it waits
	close(w.block)
	e.settle(t)
	if calls := w.got(); len(calls) != 1 {
		t.Fatalf("calls = %v, want only the first", calls)
	}
}

// The ledger is the second line of defence: even an item that is still in the queue is dropped when the ledger no longer has it pending.
func TestTheWorkerRechecksTheLedgerBeforeTheCall(t *testing.T) {
	e := newEnv(t)
	w := startWakeFor(t, e)
	e.baseline(t)
	ep := mkEp("mlab", "gone", t0)
	if _, err := e.m.store.claimWake(ep, e.now, wakeThrottle, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.m.store.dropPending(ep); err != nil {
		t.Fatal(err)
	}
	e.m.enqueueWake(ep, "mlab：x")
	e.settle(t)
	if n := len(w.got()); n != 0 {
		t.Fatalf("an episode given up was told: %v", w.got())
	}
}

// The results of one call are written together: a write that fails half way leaves nothing half done.
func TestTheResultOfOneCallIsWrittenAtomically(t *testing.T) {
	e := newEnv(t)
	a, b := mkEp("mlab", "a", t0), mkEp("mlab", "b", t0)
	for _, ep := range []wakeEp{a, b} {
		if _, err := e.m.store.claimWake(ep, e.now, wakeThrottle, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.m.store.db.Exec(`CREATE TRIGGER boom BEFORE UPDATE ON wake_ledger WHEN NEW.key = 'b' AND NEW.pending = 0 BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if err := e.m.store.markWoken([]wakeEp{a, b}, e.now.Add(time.Minute)); err == nil {
		t.Fatal("the failing write was not reported")
	}
	for _, ep := range []wakeEp{a, b} {
		live, err := e.m.store.stillPending([]wakeEp{ep})
		if err != nil || len(live) != 1 {
			t.Fatalf("%s is not pending after a failed group write (%v, %v)", ep.key, live, err)
		}
	}
	if _, err := e.m.store.db.Exec(`DROP TRIGGER boom`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.store.db.Exec(`CREATE TRIGGER boom BEFORE UPDATE ON wake_ledger WHEN NEW.key = 'b' AND NEW.attempts > 0 BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if err := e.m.store.markWakeFailed([]wakeEp{a, b}, e.now); err == nil {
		t.Fatal("the failing write was not reported")
	}
	if e.m.store.pendingDue(a, e.now.Add(time.Second)) != true {
		t.Fatal("the first episode kept a backoff from a group write that failed")
	}
}
