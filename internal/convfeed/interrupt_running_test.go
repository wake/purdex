package convfeed

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/convmodel"
)

// The mod's own interrupt leaves no marker in the transcript: the caller says when.
func TestEntry_InterruptRunning_EndsTheRunningTurnAndMovesTheRevision(t *testing.T) {
	m := newMem(userRow("u0", 0, "write a story"), toolUseRow("a0", 1, "toolu_1", "sleep 99"))
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", true))
	rev := e.Revision()
	if got := e.conv().Turns[0].Outcome; got != convmodel.OutcomeRunning {
		t.Fatalf("before: %q", got)
	}
	if !e.InterruptRunning(time.UnixMilli(t0 + 5000)) {
		t.Fatal("InterruptRunning reported no change")
	}
	if got := e.conv().Turns[0].Outcome; got != convmodel.OutcomeInterrupted {
		t.Errorf("after: %q, want interrupted", got)
	}
	if e.Revision() <= rev {
		t.Errorf("revision %d did not move past %d: the stream would not push the change", e.Revision(), rev)
	}
	if ch := e.ChangesSince(rev); len(ch) != 1 || !ch[0].HeaderChanged {
		t.Errorf("changes = %+v, want the turn header", ch)
	}
	if e.InterruptRunning(time.UnixMilli(t0 + 6000)) {
		t.Error("a second call reported a change")
	}
}

func TestEntry_InterruptRunning_NothingRunningNothingChanges(t *testing.T) {
	m := newMem(idle(1)...)
	e := NewEntry(sidA)
	refresh(t, e, src(m, "f1", false)) // the session is over: the last turn is closed as done
	rev := e.Revision()
	if e.InterruptRunning(time.UnixMilli(t0+9000)) || e.Revision() != rev {
		t.Errorf("an answered turn was touched (rev %d → %d)", rev, e.Revision())
	}
}
