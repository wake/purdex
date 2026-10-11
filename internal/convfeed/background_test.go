package convfeed

import "testing"

// c9d9c7-67 PR2 (#2592): the background work Claude Code still runs (the Stop hook's background_tasks) is not in the transcript; the module sets it
// on the entry after every refresh, and a change moves the header like a title change.

func TestEntry_SetBackgroundBumpsTheHeaderOnlyOnAChange(t *testing.T) {
	m := newMem(idle(1)...)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false))
	rev := e.Revision()
	if e.SetBackground(Background{}) {
		t.Fatal("setting what is already there reported a change")
	}
	if e.Revision() != rev {
		t.Fatalf("revision moved to %d without a change", e.Revision())
	}
	if !e.SetBackground(Background{Shell: 1}) {
		t.Fatal("a new count reported no change")
	}
	if e.Revision() != rev+1 || !e.HeaderChangedSince(rev) {
		t.Fatalf("rev %d (want %d), headerChanged %v", e.Revision(), rev+1, e.HeaderChangedSince(rev))
	}
	if b := e.Header().Background; b != (Background{Shell: 1}) {
		t.Fatalf("header background = %+v", b)
	}
	if ch := e.ChangesSince(rev); len(ch) != 0 {
		t.Errorf("background work changes no turn, got %+v", ch)
	}
	rev = e.Revision()
	if e.SetBackground(Background{Shell: 1}) || e.Revision() != rev {
		t.Fatal("the same count again moved the revision")
	}
	if !e.SetBackground(Background{Shell: 1, Subagent: 2}) || e.Revision() != rev+1 {
		t.Fatalf("a different count did not move the revision (rev %d)", e.Revision())
	}
	if !e.SetBackground(Background{}) || !e.Header().Background.Zero() {
		t.Fatal("clearing did not report a change / left counts in the header")
	}
}

func TestBackground_Zero(t *testing.T) {
	if !(Background{}).Zero() || (Background{Other: 1}).Zero() {
		t.Fatal("Zero is wrong")
	}
}
