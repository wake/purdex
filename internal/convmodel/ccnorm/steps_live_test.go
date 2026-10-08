package ccnorm

import (
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

func stepChanges(ch []Change, id string) []Change {
	var out []Change
	for _, c := range ch {
		if c.ItemID == id {
			out = append(out, c)
		}
	}
	return out
}

func TestSetLive_FalseDeniesResultlessStepsAndTrueRestoresThem(t *testing.T) {
	n := norm(t,
		userRow("u1", 1, "go"),
		toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "a"}),
		toolCall("a2", 2.1, "toolu_2", "Bash", obj{"command": "b"}),
		resultRow("r1", 3, "toolu_1", "ok", false),
	)
	posBefore, _ := n.Position("u1", "toolu_2")

	ch := n.SetLive(false)
	c := validated(t, n)
	if s := stepNamed(t, c, "toolu_2"); s.Status != convmodel.StepDenied || s.Denial != "interrupted" {
		t.Errorf("after SetLive(false): %q %q", s.Status, s.Denial)
	}
	if s := stepNamed(t, c, "toolu_1"); s.Status != convmodel.StepDone {
		t.Errorf("a step with its result changed: %q", s.Status)
	}
	if c.Turns[0].Outcome != convmodel.OutcomeDone {
		t.Errorf("outcome = %q", c.Turns[0].Outcome)
	}
	got := stepChanges(ch, "toolu_2")
	if len(got) != 1 || got[0].TurnID != "u1" || got[0].Offset != -1 {
		t.Errorf("changes for the denied step: %+v (all: %+v)", got, ch)
	}
	if len(stepChanges(ch, "toolu_1")) != 0 {
		t.Errorf("a step that did not change was reported: %+v", ch)
	}
	if posAfter, _ := n.Position("u1", "toolu_2"); posAfter != posBefore {
		t.Errorf("SetLive is not a row: position %+v → %+v", posBefore, posAfter)
	}

	ch = n.SetLive(true)
	c = validated(t, n)
	if s := stepNamed(t, c, "toolu_2"); s.Status != convmodel.StepRunning || s.Denial != "" {
		t.Errorf("after SetLive(true): %q %q", s.Status, s.Denial)
	}
	if c.Turns[0].Outcome != convmodel.OutcomeRunning {
		t.Errorf("outcome = %q", c.Turns[0].Outcome)
	}
	if got := stepChanges(ch, "toolu_2"); len(got) != 1 || got[0].Offset != -1 {
		t.Errorf("changes on reopen: %+v", ch)
	}
}

func TestSetLive_DoesNotTouchStepsOfAnEndedTurn(t *testing.T) {
	// the turn ended by turn_duration: its step is already denied{interrupted},
	// and neither SetLive changes or reports it
	n := norm(t, userRow("u1", 1, "go"), toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "a"}), turnDuration("d1", 3, 1))
	for _, live := range []bool{false, true} {
		ch := n.SetLive(live)
		if len(ch) != 0 {
			t.Errorf("SetLive(%v) on an ended turn reported %+v", live, ch)
		}
		if s := stepNamed(t, validated(t, n), "toolu_1"); s.Status != convmodel.StepDenied || s.Denial != "interrupted" {
			t.Errorf("SetLive(%v): %q %q", live, s.Status, s.Denial)
		}
	}
}

func TestSetLive_ALateResultWhileClosedStaysAfterReopen(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"), toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "a"}))
	n.SetLive(false)
	feed(t, n, resultRow("r1", 5, "toolu_1", "late", false))
	if s := stepNamed(t, validated(t, n), "toolu_1"); s.Status != convmodel.StepDone {
		t.Fatalf("late result: %q %q", s.Status, s.Denial)
	}
	n.SetLive(true)
	if s := stepNamed(t, validated(t, n), "toolu_1"); s.Status != convmodel.StepDone {
		t.Errorf("reopen undid a result: %q %q", s.Status, s.Denial)
	}
}

func TestSetLive_NewStepInAClosedTurnIsDeniedAndReopensWithIt(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"))
	n.SetLive(false)
	feed(t, n, toolCall("a1", 2, "toolu_1", "Bash", obj{"command": "a"}))
	if s := stepNamed(t, validated(t, n), "toolu_1"); s.Status != convmodel.StepDenied {
		t.Fatalf("step in a closed turn: %q", s.Status)
	}
	n.SetLive(true)
	if s := stepNamed(t, validated(t, n), "toolu_1"); s.Status != convmodel.StepRunning {
		t.Errorf("after reopen: %q", s.Status)
	}
}
