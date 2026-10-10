package ccnorm

import (
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

func TestMarkInterrupted_ATurnThatBeganInTheSameMillisecondIsNotTheAbortedOne(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"), assistantText("a1", 2, "w"))
	if ch := n.MarkInterrupted(ms(1)); len(ch) != 0 { // the turn began exactly at the abort time
		t.Errorf("changed %+v", ch)
	}
}

func TestMarkInterrupted_RowsTheAbortLetThroughStillBelongToTheTurn(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"), assistantText("a1", 2, "w"))
	n.MarkInterrupted(ms(5))
	feed(t, n, assistantText("a2", 5.4, "last words"))
	tr := validated(t, n).Turns[0]
	if tr.Outcome != convmodel.OutcomeInterrupted {
		t.Fatalf("outcome = %q", tr.Outcome)
	}
	if endedAt(t, tr) != ms(5.4) {
		t.Errorf("ended_at = %d, want the last row's time %d (no item after the end)", *tr.EndedAt, ms(5.4))
	}
	if len(tr.Items) != 3 {
		t.Errorf("items = %d, want the user row and both assistant rows", len(tr.Items))
	}
}
