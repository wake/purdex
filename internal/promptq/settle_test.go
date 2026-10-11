package promptq

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// Stuck typed rows are settled by the ledger sweep, and their ids live on in a bounded tombstone set.

func sweepNow(q *Queue) {
	q.mu.Lock()
	q.sweepLedger()
	q.mu.Unlock()
}

func openCount(q *Queue) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.openTyped()
}

// An unknown row whose Enter may have gone is settled by the sweep at once: it stops counting toward the open typed rows,
// stays answerable as unknown, and is no longer clearable.
func TestSweep_SettlesUnknownRowWithEnterMark(t *testing.T) {
	q, _ := newQ(t)
	fillOK(t, q, "c1", "hello", 1)
	q.MarkEntering("c1")
	q.MarkUnknown("c1")
	if openCount(q) != 1 {
		t.Fatal("precondition")
	}
	sweepNow(q)
	if openCount(q) != 0 {
		t.Fatalf("open = %d", openCount(q))
	}
	if r, ok := q.Lookup("c1"); !ok || r.Phase != PhaseUnknown {
		t.Fatalf("lookup = %+v %v", r, ok)
	}
	if r, _ := q.ClearFilled(context.Background(), "c1"); r.Reason != "not_clearable" {
		t.Fatalf("a settled unknown row must not be cleared: %+v", r)
	}
}

// Any open typed row older than 10 minutes is settled to unknown, so stuck rows cannot hold the 256-row cap forever.
func TestSweep_OpenTypedRowsFreeTheCountAfterTenMinutes(t *testing.T) {
	q, _ := newQ(t)
	now := time.Unix(1000, 0)
	q.Now = func() time.Time { return now }
	fillOK(t, q, "c1", "a", 1)
	fillOK(t, q, "c2", "b", 1)
	q.MarkEntering("c2")
	now = now.Add(9 * time.Minute)
	sweepNow(q)
	if openCount(q) != 2 {
		t.Fatalf("before 10 min: open = %d", openCount(q))
	}
	now = now.Add(2 * time.Minute)
	sweepNow(q)
	if openCount(q) != 0 {
		t.Fatalf("after 10 min: open = %d", openCount(q))
	}
	for _, id := range []string{"c1", "c2"} {
		if r, ok := q.Lookup(id); !ok || r.Phase != PhaseUnknown {
			t.Fatalf("%s: %+v %v", id, r, ok)
		}
	}
}

// After the settled row itself is evicted, its id lives on in a tombstone: a replay answers unknown and never creates a
// fill job. The tombstone expires after 24 h.
func TestTombstone_ReplayAfterEvictionNeverTypesAgain(t *testing.T) {
	q, _ := newQ(t)
	now := time.Unix(1000, 0)
	q.Now = func() time.Time { return now }
	fillOK(t, q, "c1", "hello", 1)
	now = now.Add(time.Hour)
	sweepNow(q)
	q.mu.Lock()
	_, rowKept := q.ledger["c1"]
	q.mu.Unlock()
	if rowKept {
		t.Fatal("the settled row should have been evicted")
	}
	if r, ok := q.Lookup("c1"); !ok || r.Phase != PhaseUnknown || r.Status != Unknown {
		t.Fatalf("lookup after eviction = %+v %v", r, ok)
	}
	r, err := q.SubmitFill(context.Background(), "s1", "c1", "hello")
	if err != nil || r.Status != Unknown || r.Phase != PhaseUnknown {
		t.Fatalf("replay = %+v %v", r, err)
	}
	q.mu.Lock()
	queued := len(q.queues["s1"])
	q.mu.Unlock()
	if queued != 0 {
		t.Fatalf("a fill job was created for a replay: %d", queued)
	}
	now = now.Add(25 * time.Hour)
	sweepNow(q)
	if _, ok := q.Lookup("c1"); ok {
		t.Fatal("tombstone should expire after 24 h")
	}
}

func TestTombstone_Capped(t *testing.T) {
	q, _ := newQ(t)
	q.mu.Lock()
	for i := 0; i < maxTombstones+50; i++ {
		q.tombstone("id"+strconv.Itoa(i), Result{})
	}
	n := len(q.tombs)
	q.mu.Unlock()
	if n > maxTombstones {
		t.Fatalf("tombstones = %d", n)
	}
}
