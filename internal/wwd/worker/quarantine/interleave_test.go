package quarantine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every operation A that does something outside the store lock (Sweep asks the unit checker; Clear, Restore and Sweep look at a snapshot
// first; Add and Begin prepare the marker file first) is stopped in that stretch, and each operation B is run in it. Whatever A does
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
	case "Add (marker prepared)":
		s.window = runB
		s.Add(e)
	case "Begin (marker prepared)":
		s.window = runB
		s.Begin(e)
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
	} else if len(holds) > 0 && b != opDiscard && !(iv.a == "Add (marker prepared)" || iv.a == "Begin (marker prepared)") {
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
		{a: "Add (marker prepared)", start: stRunning},
		{a: "Add (marker prepared)", start: stQuarantined},
		{a: "Begin (marker prepared)", start: stNone},
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
