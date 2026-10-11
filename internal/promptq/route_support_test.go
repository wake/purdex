package promptq

import (
	"context"
	"testing"
	"time"
)

// K-1a-2 support: what the /submit route needs from the ledger and the owner registry.

// An Enter that tmux definitively refused (nothing sent) steps entering back to filled so the composer can be cleaned; a
// row that was marked entered, or never entering, cannot.
func TestMarkEnterRefused_EnteringGoesBackToFilledAndIsClearable(t *testing.T) {
	q, _ := newQ(t)
	fillOK(t, q, "c1", "hello", 1)
	if err := q.MarkEnterRefused("c1"); err != ErrBadTransition {
		t.Fatalf("from filled: %v", err)
	}
	if err := q.MarkEntering("c1"); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkEnterRefused("c1"); err != nil {
		t.Fatal(err)
	}
	if r, _ := q.Lookup("c1"); r.Phase != PhaseFilled {
		t.Fatalf("phase = %s", r.Phase)
	}
	ch := make(chan answer, 1)
	go func() { r, err := q.ClearFilled(context.Background(), "c1"); ch <- answer{r, err} }()
	j := next(t, q, "mod1", "s1")
	if j.Kind != KindClearIf || j.Text != "hello" {
		t.Fatalf("job = %+v", j)
	}
	q.Result("mod1", j.ID, Outcome{Status: Accepted})
	take(t, ch)
	if r, _ := q.Lookup("c1"); r.Phase != PhaseCleared {
		t.Fatalf("phase = %s", r.Phase)
	}
	fillOK(t, q, "c2", "x", 1)
	q.MarkEntering("c2")
	q.MarkEntered("c2")
	if err := q.MarkEnterRefused("c2"); err != ErrBadTransition {
		t.Fatalf("an entered row must stay entered: %v", err)
	}
}

// A cleanup that failed must still end: the row becomes cleared with reason cleanup_gave_up and stops counting toward the
// open typed rows (so 256 failed cleanups do not turn every later fill into busy).
func TestMarkGaveUp_SettlesAClearableRowAndFreesTheOpenCount(t *testing.T) {
	q, _ := newQ(t)
	fillOK(t, q, "c1", "hello", 1)
	q.mu.Lock()
	before := q.openTyped()
	q.mu.Unlock()
	if err := q.MarkGaveUp("c1"); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	after := q.openTyped()
	q.mu.Unlock()
	if before != 1 || after != 0 {
		t.Fatalf("open typed rows %d -> %d", before, after)
	}
	if r, _ := q.Lookup("c1"); r.Phase != PhaseCleared || r.Reason != "cleanup_gave_up" {
		t.Fatalf("row = %+v", r)
	}
	// a row that may have been sent is not settled this way
	fillOK(t, q, "c2", "x", 1)
	q.MarkEntering("c2")
	if err := q.MarkGaveUp("c2"); err != ErrBadTransition {
		t.Fatalf("entering row: %v", err)
	}
}

// NoOwnerFor: zero while a mod owns the session; else how long since an owner was last seen (the queue's start counts as
// the first sighting, so a freshly started daemon does not call a session ownerless before its mod could connect).
func TestNoOwnerFor(t *testing.T) {
	q, o := newQ(t)
	now := time.Unix(1000, 0)
	q.Now = func() time.Time { return now }
	q.started = now
	if d := q.NoOwnerFor("s1"); d != 0 {
		t.Fatalf("owned: %v", d)
	}
	now = now.Add(3 * time.Second)
	o.set("s1", "")
	if d := q.NoOwnerFor("s1"); d != 3*time.Second {
		t.Fatalf("lost owner at the last sighting (t0): %v", d)
	}
	now = now.Add(20 * time.Second)
	if d := q.NoOwnerFor("s1"); d != 23*time.Second {
		t.Fatalf("later: %v", d)
	}
	o.set("s1", "mod1")
	q.HasOwner("s1") // a sighting
	o.set("s1", "")
	now = now.Add(4 * time.Second)
	if d := q.NoOwnerFor("s1"); d != 4*time.Second {
		t.Fatalf("after a new sighting: %v", d)
	}
	if d := q.NoOwnerFor("never-seen"); d != 27*time.Second {
		t.Fatalf("never seen counts from the queue start: %v", d)
	}
}
