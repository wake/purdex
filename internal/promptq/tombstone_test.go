package promptq

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// At-most-once after eviction: EVERY evicted typed row leaves a tombstone holding its recorded answer.

func evictAfterAnHour(t *testing.T, q *Queue, id string) {
	t.Helper()
	now := time.Now()
	q.Now = func() time.Time { return now.Add(time.Hour) }
	sweepNow(q)
	q.mu.Lock()
	_, kept := q.ledger[id]
	q.mu.Unlock()
	if kept {
		t.Fatalf("%s should have been evicted", id)
	}
}

func replayNeverTypes(t *testing.T, q *Queue, id, text string) Result {
	t.Helper()
	r, err := q.SubmitFill(context.Background(), "s1", id, text)
	if err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	queued := len(q.queues["s1"])
	q.mu.Unlock()
	if queued != 0 {
		t.Fatalf("a fill job was created for a replay of %s", id)
	}
	return r
}

func TestTombstone_EnteredRowReplaysAsEntered(t *testing.T) {
	q, _ := newQ(t)
	fillOK(t, q, "e1", "hello", 1)
	q.MarkEntering("e1")
	q.MarkEntered("e1")
	evictAfterAnHour(t, q, "e1")
	if r, ok := q.Lookup("e1"); !ok || r.Phase != PhaseEntered {
		t.Fatalf("lookup = %+v %v, want entered", r, ok)
	}
	if r := replayNeverTypes(t, q, "e1", "hello"); r.Phase != PhaseEntered {
		t.Fatalf("replay = %+v, want entered", r)
	}
}

func TestTombstone_ClearedAndGaveUpKeepTheirReason(t *testing.T) {
	q, _ := newQ(t)
	fillOK(t, q, "c1", "a", 1)
	q.MarkCleared("c1")
	fillOK(t, q, "g1", "b", 1)
	q.MarkGaveUp("g1")
	evictAfterAnHour(t, q, "c1")
	if r, _ := q.Lookup("c1"); r.Phase != PhaseCleared {
		t.Fatalf("c1 = %+v", r)
	}
	if r, ok := q.Lookup("g1"); !ok || r.Phase != PhaseCleared || r.Reason != "cleanup_gave_up" {
		t.Fatalf("g1 = %+v %v", r, ok)
	}
	if r := replayNeverTypes(t, q, "g1", "b"); r.Reason != "cleanup_gave_up" || r.Phase != PhaseCleared {
		t.Fatalf("replay = %+v", r)
	}
}

func TestTombstone_DroppedFillKeepsItsReason(t *testing.T) {
	q, _ := newQ(t)
	ch := fillAsync(q, "s1", "d1", "x")
	j := next(t, q, "mod1", "s1")
	if err := q.Result("mod1", j.ID, Outcome{Status: Dropped, Reason: "dialog_open"}); err != nil {
		t.Fatal(err)
	}
	take(t, ch)
	evictAfterAnHour(t, q, "d1")
	r, ok := q.Lookup("d1")
	if !ok || r.Status != Dropped || r.Reason != "dialog_open" {
		t.Fatalf("lookup = %+v %v", r, ok)
	}
}

func TestTombstone_OnlyTypedRowsLeaveOne(t *testing.T) {
	q, _ := newQ(t)
	ch := submitAsync(q, "s1", "plain", "hi")
	j := next(t, q, "mod1", "s1")
	_ = q.Result("mod1", j.ID, Outcome{Status: Accepted})
	take(t, ch)
	evictAfterAnHour(t, q, "plain")
	if _, ok := q.Lookup("plain"); ok {
		t.Fatal("a non-typed row must not leave a tombstone")
	}
}

func TestTombstone_FullDropsTheOldestByTime(t *testing.T) {
	q, _ := newQ(t)
	now := time.Unix(1000, 0)
	q.Now = func() time.Time { return now }
	q.mu.Lock()
	for i := 0; i < maxTombstones; i++ {
		now = now.Add(time.Second)
		q.tombstone("id"+strconv.Itoa(i), Result{Status: Unknown, Phase: PhaseUnknown})
	}
	now = now.Add(time.Second)
	q.tombstone("newest", Result{Status: Unknown, Phase: PhaseUnknown})
	_, oldest := q.tombs["id0"]
	_, second := q.tombs["id1"]
	n := len(q.tombs)
	q.mu.Unlock()
	if oldest || !second || n != maxTombstones {
		t.Fatalf("oldest kept=%v second kept=%v n=%d", oldest, second, n)
	}
}
