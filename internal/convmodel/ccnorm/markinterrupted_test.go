package ccnorm

import (
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

// The Purdex mod's own interrupt ($.turn.abort) writes no marker row: the caller says when it happened.
func TestMarkInterrupted_EndsTheRunningLastTurn(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"), assistantText("a1", 2, "writing"))
	if got := n.Conversation().Turns[0].Outcome; got != convmodel.OutcomeRunning {
		t.Fatalf("before: %q, want running", got)
	}
	ch := n.MarkInterrupted(ms(5))
	if len(ch) != 1 || ch[0].Offset != -1 {
		t.Fatalf("changes = %+v, want the one turn with Offset -1", ch)
	}
	tr := validated(t, n).Turns[0]
	if tr.Outcome != convmodel.OutcomeInterrupted || endedAt(t, tr) != ms(5) {
		t.Errorf("after: %q ended %v, want interrupted at the abort time", tr.Outcome, tr.EndedAt)
	}
	if again := n.MarkInterrupted(ms(6)); len(again) != 0 {
		t.Errorf("a second call changed %+v", again)
	}
}

func TestMarkInterrupted_LeavesAnEndedTurnAlone(t *testing.T) {
	n := norm(t, userRow("u1", 1, "hi"), assistantText("a1", 2, "yo"), stopHookSummary("h1", 3), turnDuration("d1", 3.1, 2100))
	if ch := n.MarkInterrupted(ms(9)); len(ch) != 0 {
		t.Errorf("changed an answered turn: %+v", ch)
	}
	if got := n.Conversation().Turns[0].Outcome; got != convmodel.OutcomeDone {
		t.Errorf("outcome = %q, want done", got)
	}
}

func TestMarkInterrupted_IgnoresATurnThatBeganAfterTheAbort(t *testing.T) {
	n := norm(t, userRow("u1", 1, "first"), assistantText("a1", 2, "x"), turnDuration("d1", 2.5, 1500), userRow("u2", 10, "second"), assistantText("a2", 11, "y"))
	if ch := n.MarkInterrupted(ms(5)); len(ch) != 0 {
		t.Errorf("an abort from before turn two ended turn two: %+v", ch)
	}
	if got := n.Conversation().Turns[1].Outcome; got != convmodel.OutcomeRunning {
		t.Errorf("turn two = %q, want running", got)
	}
}

func TestMarkInterrupted_NoTurns(t *testing.T) {
	if ch := New(Options{SessionID: sidA}).MarkInterrupted(ms(1)); ch != nil {
		t.Errorf("changes = %+v", ch)
	}
}

func TestMarkInterrupted_AMarkerWrittenLaterChangesNothing(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"), assistantText("a1", 2, "w"))
	n.MarkInterrupted(ms(5))
	feed(t, n, interruptRow("i1", 6, false))
	if got := validated(t, n).Turns[0].Outcome; got != convmodel.OutcomeInterrupted {
		t.Errorf("outcome = %q, want interrupted", got)
	}
}

func TestMarkInterrupted_OpenStepsFollowTheTurn(t *testing.T) {
	n := norm(t, userRow("u1", 1, "run"), assistantRow("a1", 2, "claude-haiku", toolUseBlock("toolu_1", "Bash", obj{"command": "sleep 99"})))
	n.MarkInterrupted(ms(5))
	for _, it := range validated(t, n).Turns[0].Items {
		if it.Step != nil && it.Step.Status == convmodel.StepRunning {
			t.Errorf("a step is still running in an interrupted turn: %+v", it.Step)
		}
	}
}
