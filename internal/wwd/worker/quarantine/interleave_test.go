package quarantine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every operation A that does something outside the store lock (Sweep asks the unit checker; Clear, Restore and Sweep look at a snapshot
// first; Add and Begin have no such stretch, see below) is stopped in that stretch, and each operation B is run in it. Whatever A does
// next, the invariants of the package doc must hold. The outcome of A and B themselves is not compared (either may be refused); the
// invariants are.

type interleaving struct {
	a       string
	start   state
	restart bool // the hold was released before A (a restart): only Restore/Sweep may put it back
}

// window runs B in A's outside-the-lock stretch and then lets A go on.
func runInterleaving(t *testing.T, iv interleaving, b op) {
	r := newRig(t)
	s := r.open()
	e := setUp(t, r, s, iv.start)
	if iv.start == stNone {
		if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil { // the runner's hold, taken before Begin
			t.Fatal(err)
		}
	}
	if iv.restart {
		r.locks.ReleaseOwner(e.Owner)
	}
	r.checker.gone["wwd-a"] = true
	once := false
	runB := func() {
		if once {
			return
		}
		once = true
		apply(t, r, s, e, b)
	}
	ctx := context.Background()
	switch iv.a {
	case "Sweep (unit check)":
		r.checker.onGone = runB
		s.Sweep(ctx)
	case "Sweep (restore step)":
		for _, h := range s.snapshot() {
			runB()
			s.restoreOne(h)
		}
	case "Clear (snapshot)":
		for _, h := range s.snapshot() {
			runB()
			s.retire(h, true)
		}
	case "Restore (snapshot)":
		for _, h := range s.snapshot() {
			runB()
			s.restoreOne(h)
		}
	default:
		t.Fatalf("unknown A %q", iv.a)
	}
	checkInvariants(t, r, s, e, iv, b)
}

func checkInvariants(t *testing.T, r *rig, s *Store, e Entry, iv interleaving, b op) {
	t.Helper()
	files := markerFiles(t, r.markers)
	var markers []string
	for _, f := range files {
		if strings.Contains(f, ".tmp-") {
			t.Errorf("a prepared marker file was left behind: %s", f)
			continue
		}
		markers = append(markers, f)
	}
	held := s.Held()
	// (a) an entry has its marker, a marker has its entry
	if len(markers) != len(held) {
		t.Errorf("(a) %d markers on disk, %d entries", len(markers), len(held))
	}
	for _, h := range held {
		if _, err := os.Lstat(filepath.Join(r.markers, markerName(h.Owner))); err != nil {
			t.Errorf("(a) entry %s has no marker: %v", h.Owner, err)
		}
	}
	// (a) a quarantined entry has its hold (after a restart: Restore or Sweep gives it back, and until then it holds nothing, never other keys)
	s.mu.Lock()
	recs := map[string]rec{}
	for k, v := range s.entries {
		recs[k] = v
	}
	s.mu.Unlock()
	holds := map[string]bool{}
	for k, owner := range r.locks.Held() {
		if owner == e.Owner {
			holds[k] = true
		}
	}
	if rec, ok := recs[e.Owner]; ok {
		if len(holds) > 0 || !iv.restart || iv.a == "Restore (snapshot)" {
			for _, k := range rec.e.Keys {
				if !holds[k] && !iv.restart {
					t.Errorf("(a) entry %s lacks the hold on %s", e.Owner, k)
				}
			}
		}
		for k := range holds {
			found := false
			for _, kk := range rec.e.Keys {
				found = found || kk == k
			}
			if !found {
				t.Errorf("(a) %s holds %s, which its marker does not name", e.Owner, k)
			}
		}
	} else if len(holds) > 0 && b != opDiscard {
		// (b) no entry: no hold either, except the runner's own after a Discard
		t.Errorf("(b) %s has no entry but holds %v", e.Owner, holds)
	}
	// (b) an entry that is gone is not back with a directory removed under a running one
	if rec, ok := recs[e.Owner]; ok && rec.live && !exists(e.Dir) {
		t.Errorf("(c) a running entry lost its directory")
	}
}

func TestEveryOperationInEveryOutsideTheLockStretchKeepsTheInvariants(t *testing.T) {
	as := []interleaving{
		{a: "Sweep (unit check)", start: stQuarantined},
		{a: "Sweep (restore step)", start: stQuarantined, restart: true},
		{a: "Clear (snapshot)", start: stRunning},
		{a: "Clear (snapshot)", start: stQuarantined},
		{a: "Restore (snapshot)", start: stQuarantined, restart: true},
	}
	bs := []op{opBegin, opAdd, opSettle, opDiscard, opSweepGone, opSweepNot, opClear, opRestore}
	for _, iv := range as {
		for _, b := range bs {
			t.Run(iv.a+"/"+string(iv.start)+"/"+string(b), func(t *testing.T) { runInterleaving(t, iv, b) })
		}
	}
}

// The three the reviews found, written out: each one failed before the generation moved with every transition and the hold was taken back
// only under the lock.
func TestARestoreOrSweepThatSawAnEntryDoesNotBringBackAHoldAfterClear(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := setUp(t, r, s, stQuarantined)
	r.locks.ReleaseOwner(e.Owner) // a restart
	h := s.snapshot()[0]
	if err := s.Clear("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.restoreOne(h); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.locks.Holder("mysql"); ok {
		t.Fatal("the hold of a cleared entry was taken again")
	}
}

func TestAClearThatSawARunningEntryCannotClearItOnceSettled(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := setUp(t, r, s, stRunning)
	h := s.snapshot()[0]
	s.Settle(e.Owner) // same entry, now quarantined: a new generation
	if ok, err := s.retire(h, true); ok || err != nil {
		t.Fatalf("retired %v err %v: the snapshot saw a running entry", ok, err)
	}
	if observe(s, e.Owner) != stQuarantined || !exists(e.Dir) {
		t.Fatal("the entry and its directory stay")
	}
}

// A Sweep whose unit check was answered while the entry was replaced does not act on the answer.
func TestASweepAnswerForAReplacedEntryIsNotApplied(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := setUp(t, r, s, stQuarantined)
	r.checker.gone["wwd-a"] = true
	r.checker.onGone = func() {
		if err := s.Clear("a"); err != nil {
			t.Error(err)
		}
		e2 := r.entry("a", "mysql")
		if _, err := r.locks.Acquire(e2.Owner, e2.Keys, nil); err != nil {
			t.Error(err)
		}
		if err := s.Begin(e2); err != nil {
			t.Error(err)
		}
	}
	n, err := s.Sweep(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("cleared %d err %v: the entry the checker answered for is gone", n, err)
	}
	if observe(s, e.Owner) != stRunning {
		t.Fatal("the new run's entry stays")
	}
	if _, ok := r.locks.Holder("mysql"); !ok || !exists(e.Dir) {
		t.Fatal("the new run's hold and directory stay")
	}
}

// A snapshot of an entry that was cleared and came back under other keys does not take the old keys, and does not fail on the new ones.
func TestARestoreOfAnOlderGenerationDoesNotTouchTheNewerEntry(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := setUp(t, r, s, stQuarantined)
	r.locks.ReleaseOwner(e.Owner) // a restart
	h := s.snapshot()[0]
	if err := s.Clear("a"); err != nil {
		t.Fatal(err)
	}
	e2 := r.entry("a", "redis")
	if _, err := r.locks.Acquire(e2.Owner, e2.Keys, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Begin(e2); err != nil {
		t.Fatal(err)
	}
	s.Settle(e2.Owner)
	if err := s.restoreOne(h); err != nil {
		t.Fatalf("the old snapshot acted on the newer entry: %v", err)
	}
	if _, ok := r.locks.Holder("mysql"); ok {
		t.Fatal("the old entry's key was taken")
	}
}

// A running entry's hold is the runner's: Restore does not take it back for the runner.
func TestRestoreLeavesTheHoldOfARunningEntryToTheRunner(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := setUp(t, r, s, stRunning)
	r.locks.ReleaseOwner(e.Owner)
	if err := s.Restore(); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.locks.Holder("mysql"); ok {
		t.Fatal("Restore took the hold of a running entry")
	}
}

// A Clear whose entry changed while it was looking at it says so, and clears nothing.
func TestAClearOfAnEntryThatChangedSaysSo(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := setUp(t, r, s, stQuarantined)
	s.window = func() {
		s.window = nil
		if err := s.Add(e); err != nil { // rewritten: a new generation
			t.Error(err)
		}
	}
	if err := s.Clear("a"); err == nil {
		t.Fatal("want an error: the entry changed, nothing was cleared")
	}
	if observe(s, e.Owner) != stQuarantined || !exists(e.Dir) {
		t.Fatal("the entry stays")
	}
}

// Add and Begin are done whole under the store lock: nothing runs between their check and their commit, so there is no stretch in which
// to put another operation. (Clear's window hook is the only one there is; it is never called by Add or Begin.)
func TestAddAndBeginHaveNoStretchOutsideTheLock(t *testing.T) {
	r := newRig(t)
	s := r.open()
	called := false
	s.window = func() { called = true }
	e := r.entry("a", "mysql")
	if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Begin(e); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(e); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("Add or Begin called the outside-the-lock hook: they have a stretch outside the lock again")
	}
	// the lock is held for the whole of them: a goroutine that takes it waits until they are done
	s.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- s.Add(e) }()
	select {
	case <-done:
		t.Fatal("Add finished while the store lock was held by someone else")
	case <-time.After(50 * time.Millisecond):
	}
	s.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Begin writes no marker for an owner that does not hold exactly its keys (the hold was released before it got the lock, or never taken).
func TestBeginRefusesAnOwnerThatDoesNotHoldItsKeys(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := r.entry("a", "mysql")
	if err := s.Begin(e); err == nil {
		t.Fatal("no hold: want a refusal")
	}
	if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil {
		t.Fatal(err)
	}
	r.locks.ReleaseOwner(e.Owner)
	if err := s.Begin(e); err == nil {
		t.Fatal("hold released: want a refusal")
	}
	if len(markerFiles(t, r.markers)) != 0 || len(s.Held()) != 0 {
		t.Fatalf("a refused Begin left a marker %v or an entry", markerFiles(t, r.markers))
	}
}

// The old Add that was prepared before a Clear and a newer Add cannot overwrite the newer entry: Add has no prepared state to carry over.
// Written as the sequence: Add(A) | Clear | hold again | Add(B) | Add(A) again is a new, checked call.
func TestACompletedAddCannotBeReplayedOverANewerEntry(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := setUp(t, r, s, stQuarantined)
	if err := s.Clear("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(e); err == nil { // the hold was given back by Clear
		t.Fatal("an Add for a cleared owner with no hold must be refused")
	}
	b := r.entry("a", "mysql")
	b.ClaimID = "newer"
	if err := r.add(s, b); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(e); err != nil { // same owner and keys, checked afresh: it is a new write, not a stale one
		t.Fatal(err)
	}
	if got := s.Held(); len(got) != 1 || got[0].ClaimID != e.ClaimID {
		t.Fatalf("held %v", got)
	}
}

// A Sweep and a Clear and an Add running at the same time keep the invariants (run with -race).
func TestConcurrentOperationsKeepTheInvariants(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := setUp(t, r, s, stQuarantined)
	r.checker.gone["wwd-a"] = true
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 40; i++ {
			r.add(s, e)
			s.Settle(e.Owner)
			s.Restore()
		}
	}()
	for i := 0; i < 40; i++ {
		s.Sweep(context.Background())
		s.Clear("a")
	}
	<-done
	checkInvariants(t, r, s, e, interleaving{a: "concurrent", start: stQuarantined}, opAdd)
}
