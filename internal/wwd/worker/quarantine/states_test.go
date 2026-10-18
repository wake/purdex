package quarantine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The tables here are the one in the package doc, cell by cell: every (state, operation) pair is run and the result, the new state, the
// marker, the hold and the directory are compared with what the table says. A pair that is not in the table is a failure of the table.

type state string

const (
	stNone        state = "none"
	stRunning     state = "running"
	stQuarantined state = "quarantined"
)

type op string

const (
	opBegin     op = "Begin"
	opAdd       op = "Add"
	opSettle    op = "Settle"
	opDiscard   op = "Discard"
	opSweepGone op = "Sweep (unit gone)"
	opSweepNot  op = "Sweep (unit not gone)"
	opClear     op = "Clear"
	opRestore   op = "Restore"
)

// cell is what the table says an operation does in a state.
type cell struct {
	err      error // errors.Is; nil = no error
	anyErr   bool  // some error (Clear of nothing)
	after    state
	marker   bool // a marker file after
	hold     bool // the owner holds its key after
	dir      bool // the directory exists after
	asked    bool // the unit checker was asked
	holdGone bool // before the op the hold is dropped (a restart), to see Restore take it again
	// the fixture makes the directory in every state (the executor's), so "dir" is true unless the operation removes it
}

var table = map[state]map[op]cell{
	stNone: {
		opBegin:     {anyErr: true, after: stNone, dir: true}, // no hold: refused
		opAdd:       {anyErr: true, after: stNone, dir: true}, // no hold: a marker without its hold is refused
		opSettle:    {after: stNone, dir: true},
		opDiscard:   {after: stNone, dir: true},
		opSweepGone: {after: stNone, dir: true},
		opSweepNot:  {after: stNone, dir: true},
		opClear:     {anyErr: true, after: stNone, dir: true},
		opRestore:   {after: stNone, dir: true},
	},
	stRunning: {
		opBegin:     {err: ErrExists, after: stRunning, marker: true, hold: true, dir: true},
		opAdd:       {after: stQuarantined, marker: true, hold: true, dir: true},
		opSettle:    {after: stQuarantined, marker: true, hold: true, dir: true},
		opDiscard:   {after: stNone, hold: true, dir: true},
		opSweepGone: {after: stRunning, marker: true, hold: true, dir: true},
		opSweepNot:  {after: stRunning, marker: true, hold: true, dir: true},
		opClear:     {err: ErrRunning, after: stRunning, marker: true, hold: true, dir: true},
		opRestore:   {after: stRunning, marker: true, hold: true, dir: true},
	},
	stQuarantined: {
		opBegin:     {err: ErrExists, after: stQuarantined, marker: true, hold: true, dir: true},
		opAdd:       {after: stQuarantined, marker: true, hold: true, dir: true},
		opSettle:    {after: stQuarantined, marker: true, hold: true, dir: true},
		opDiscard:   {err: ErrNotRunning, after: stQuarantined, marker: true, hold: true, dir: true},
		opSweepGone: {after: stNone, asked: true},
		opSweepNot:  {after: stQuarantined, marker: true, hold: true, dir: true, asked: true},
		opClear:     {after: stNone},
		opRestore:   {after: stQuarantined, marker: true, hold: true, dir: true, holdGone: true},
	},
}

// setUp puts the owner "src/a" (key "mysql") in a state the way the code does: running by Begin, quarantined by Begin and Settle.
func setUp(t *testing.T, r *rig, s *Store, st state) Entry {
	t.Helper()
	e := r.entry("a", "mysql")
	switch st {
	case stNone:
	case stRunning, stQuarantined:
		if _, err := r.locks.Acquire(e.Owner, e.Keys, nil); err != nil { // the runner's hold
			t.Fatal(err)
		}
		if err := s.Begin(e); err != nil {
			t.Fatal(err)
		}
		if st == stQuarantined {
			s.Settle(e.Owner)
		}
	}
	return e
}

// observe says which state the store has the owner in.
func observe(s *Store, owner string) state {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.entries[owner]
	switch {
	case !ok:
		return stNone
	case r.live:
		return stRunning
	}
	return stQuarantined
}

func apply(t *testing.T, r *rig, s *Store, e Entry, o op) error {
	t.Helper()
	ctx := context.Background()
	switch o {
	case opBegin:
		return s.Begin(e)
	case opAdd:
		return s.Add(e)
	case opSettle:
		s.Settle(e.Owner)
		return nil
	case opDiscard:
		return s.Discard(e.Owner)
	case opSweepGone:
		r.checker.gone["wwd-a"] = true
		_, err := s.Sweep(ctx)
		return err
	case opSweepNot:
		r.checker.gone["wwd-a"] = false
		_, err := s.Sweep(ctx)
		return err
	case opClear:
		return s.Clear("a")
	case opRestore:
		return s.Restore()
	}
	t.Fatalf("operation %q is not in the table", o)
	return nil
}

func TestEveryStateAndOperationDoesWhatTheTableSays(t *testing.T) {
	for st, row := range table {
		for o, want := range row {
			t.Run(string(st)+"/"+string(o), func(t *testing.T) {
				r := newRig(t)
				s := r.open()
				e := setUp(t, r, s, st)
				if want.holdGone {
					r.locks.ReleaseOwner(e.Owner) // a restart: the entry is on disk, the hold is not
				}
				err := apply(t, r, s, e, o)
				switch {
				case want.err != nil:
					if !errors.Is(err, want.err) {
						t.Fatalf("err = %v, want %v", err, want.err)
					}
				case want.anyErr:
					if err == nil {
						t.Fatal("want an error")
					}
				case err != nil:
					t.Fatalf("err = %v", err)
				}
				if got := observe(s, e.Owner); got != want.after {
					t.Errorf("state after = %s, want %s", got, want.after)
				}
				if got := len(markerFiles(t, r.markers)) == 1; got != want.marker {
					t.Errorf("marker on disk = %v, want %v", got, want.marker)
				}
				if _, got := r.locks.Holder("mysql"); got != want.hold {
					t.Errorf("hold = %v, want %v", got, want.hold)
				}
				_, derr := os.Stat(e.Dir)
				if got := derr == nil; got != want.dir {
					t.Errorf("directory exists = %v, want %v", got, want.dir)
				}
				r.checker.mu.Lock()
				asked := len(r.checker.calls) > 0
				r.checker.mu.Unlock()
				if asked != want.asked {
					t.Errorf("unit checker asked = %v, want %v", asked, want.asked)
				}
			})
		}
	}
}

func TestTheTableCoversEveryStateAndOperation(t *testing.T) {
	for _, st := range []state{stNone, stRunning, stQuarantined} {
		for _, o := range []op{opBegin, opAdd, opSettle, opDiscard, opSweepGone, opSweepNot, opClear, opRestore} {
			if _, ok := table[st][o]; !ok {
				t.Errorf("the table has no cell for %s / %s: an operation not in the table must be refused, and it must be written down", st, o)
			}
		}
	}
}

// (b): an operation acts on the entry it saw, never on a later one.
func TestAnOperationThatLookedAtAnEarlierEntryDoesNothingToALaterOne(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := setUp(t, r, s, stQuarantined)
	old := s.snapshot()[0] // what a Sweep or a Clear saw
	if err := s.Clear("a"); err != nil {
		t.Fatal(err)
	}
	// the next attempt of the same job: a new entry for the same owner
	e2 := r.entry("a", "mysql")
	if _, err := r.locks.Acquire(e2.Owner, e2.Keys, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Begin(e2); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.retire(old, false); ok || err != nil {
		t.Fatalf("retired %v err %v: a snapshot of a retired entry must not touch the one that came after", ok, err)
	}
	if ok, err := s.retire(old, true); ok || err != nil {
		t.Fatalf("retired %v err %v (human)", ok, err)
	}
	if got := observe(s, e2.Owner); got != stRunning {
		t.Fatalf("state %s", got)
	}
	if _, ok := r.locks.Holder("mysql"); !ok || !exists(e2.Dir) {
		t.Fatal("the new entry's hold and directory are untouched")
	}
	_ = e
}

// (b) again: a replaced entry (Add of the same owner) is not retired by a snapshot of the one it replaced.
func TestAReplacedEntryIsNotRetiredByASnapshotOfTheOldOne(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := setUp(t, r, s, stQuarantined)
	old := s.snapshot()[0]
	if err := s.Add(e); err != nil { // rewritten: a new generation
		t.Fatal(err)
	}
	r.checker.gone["wwd-a"] = true
	if ok, _ := s.retire(old, false); ok {
		t.Fatal("the snapshot is of the older generation")
	}
	if observe(s, e.Owner) != stQuarantined {
		t.Fatal("still quarantined")
	}
}

// (a): after a restart every readable marker is an entry that is not running, and Restore gives its keys back.
func TestAfterARestartEveryMarkerIsAQuarantinedEntryWhoseKeysComeBack(t *testing.T) {
	r := newRig(t)
	s := r.open()
	setUp(t, r, s, stRunning) // this process dies while it runs
	r.locks.ReleaseOwner("src/a")
	s2 := r.open()
	if observe(s2, "src/a") != stQuarantined {
		t.Fatalf("state %s: a marker loaded from disk is never running", observe(s2, "src/a"))
	}
	if err := s2.Restore(); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.locks.Holder("mysql"); !ok {
		t.Fatal("the keys are back")
	}
}

// (a) when a key is taken by someone else: Restore says so, the entry stays, and Sweep keeps trying.
func TestRestoreThatCannotTakeAKeyReportsItAndKeepsTheEntry(t *testing.T) {
	r := newRig(t)
	s := r.open()
	e := setUp(t, r, s, stQuarantined)
	r.locks.ReleaseOwner(e.Owner)
	if _, err := r.locks.Acquire("someone-else", []string{"mysql"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(); err == nil {
		t.Fatal("want an error: the key is held by another owner")
	}
	if observe(s, e.Owner) != stQuarantined || len(markerFiles(t, r.markers)) != 1 {
		t.Fatal("the entry and its marker stay")
	}
	_ = filepath.Join // (kept for the path helpers above)
}
