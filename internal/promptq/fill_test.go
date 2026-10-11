package promptq

import (
	"context"
	"testing"
	"time"
)

// K-1a-1: typed submits (fill), the cleanup (clear_if) and read go through the same queue; one state machine per
// client_msg_id: queued -> filled -> entering -> entered | cleared | unknown.

func fillAsync(q *Queue, sid, id, text string) chan answer {
	ch := make(chan answer, 1)
	go func() {
		r, err := q.SubmitFill(context.Background(), sid, id, text)
		ch <- answer{r, err}
	}()
	return ch
}

// fillOK runs a fill through the mod and leaves the ledger row in `filled`.
func fillOK(t *testing.T, q *Queue, id, text string, at int64) {
	t.Helper()
	ch := fillAsync(q, "s1", id, text)
	j := next(t, q, "mod1", "s1")
	if err := q.Result("mod1", j.ID, Outcome{Status: Accepted, FilledAt: at}); err != nil {
		t.Fatal(err)
	}
	take(t, ch)
}

// Mutation gate: drop FilledAt from the report, or hand the wrong kind -> red.
func TestFill_HandedAsFillAndReportsFilledAt(t *testing.T) {
	q, _ := newQ(t)
	ch := fillAsync(q, "s1", "c1", "hi\nthere ")
	j := next(t, q, "mod1", "s1")
	if j.Kind != KindFill || j.Text != "hi\nthere " || j.SessionID != "s1" {
		t.Fatalf("job = %+v", j)
	}
	if err := q.Result("mod1", j.ID, Outcome{Status: Accepted, FilledAt: 1234}); err != nil {
		t.Fatal(err)
	}
	r := take(t, ch)
	if r.Status != Accepted || r.FilledAt <= 0 || r.FilledAt != r.ReceivedAt || r.Phase != PhaseFilled {
		t.Fatalf("result = %+v", r)
	}
	if got, ok := q.Lookup("c1"); !ok || got.Phase != PhaseFilled || got.FilledAt <= 0 {
		t.Fatalf("lookup = %+v %v", got, ok)
	}
}

func TestFill_DroppedIsClearedAndKeepsTheReason(t *testing.T) {
	q, _ := newQ(t)
	ch := fillAsync(q, "s1", "c1", "x")
	j := next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Dropped, Reason: "input_not_empty"})
	r := take(t, ch)
	if r.Status != Dropped || r.Reason != "input_not_empty" || r.Phase != PhaseCleared {
		t.Fatalf("result = %+v", r)
	}
}

func TestFill_NoModWhenNoOwner(t *testing.T) {
	q, o := newQ(t)
	o.set("s1", "")
	if r, err := q.SubmitFill(context.Background(), "s1", "c1", "x"); err != nil || r.Status != NoMod {
		t.Fatalf("%+v %v", r, err)
	}
}

// Mutation gate: let a transition skip a step (entering without filled) -> red.
func TestFill_StateMachineTransitions(t *testing.T) {
	q, _ := newQ(t)
	ch := fillAsync(q, "s1", "c1", "x")
	j := next(t, q, "mod1", "s1")
	if err := q.MarkEntering("c1"); err != ErrBadTransition { // not filled yet
		t.Fatalf("entering before filled: %v", err)
	}
	q.Result("mod1", j.ID, Outcome{Status: Accepted, FilledAt: 1})
	take(t, ch)
	if err := q.MarkEntered("c1"); err != ErrBadTransition { // entered needs entering first
		t.Fatalf("entered before entering: %v", err)
	}
	if err := q.MarkEntering("c1"); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkEntering("c1"); err != ErrBadTransition { // the at-most-once line: Enter only once
		t.Fatalf("second entering: %v", err)
	}
	if err := q.MarkCleared("c1"); err != ErrBadTransition { // Enter may have gone: no longer clearable
		t.Fatalf("cleared after entering: %v", err)
	}
	if err := q.MarkEntered("c1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Lookup("c1"); got.Phase != PhaseEntered {
		t.Fatalf("phase = %s", got.Phase)
	}
	if err := q.MarkUnknown("c1"); err != ErrBadTransition { // a final state stays final
		t.Fatalf("unknown after entered: %v", err)
	}
	if err := q.MarkEntering("nope"); err != ErrBadTransition {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestFill_ClearedFromFilledAndUnknownFromEntering(t *testing.T) {
	q, _ := newQ(t)
	fillOK(t, q, "a", "x", 1)
	if err := q.MarkCleared("a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Lookup("a"); got.Phase != PhaseCleared {
		t.Fatalf("a: %s", got.Phase)
	}
	fillOK(t, q, "b", "x", 1)
	if err := q.MarkEntering("b"); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkUnknown("b"); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Lookup("b"); got.Phase != PhaseUnknown {
		t.Fatalf("b: %s", got.Phase)
	}
}

// A repeated id answers the recorded state and never types again.
// Mutation gate: re-queue a settled fill on replay -> red (a second job appears).
func TestFill_ReplayAnswersTheRecordedStateAndNeverReruns(t *testing.T) {
	q, _ := newQ(t)
	fillOK(t, q, "c1", "x", 7)
	q.MarkEntering("c1")
	r, err := q.SubmitFill(context.Background(), "s1", "c1", "x")
	if err != nil || r.Status != Accepted || r.Phase != PhaseEntering || r.FilledAt <= 0 {
		t.Fatalf("replay = %+v %v", r, err)
	}
	if _, ok := q.Next(context.Background(), "mod1", "s1", 60*time.Millisecond); ok {
		t.Fatal("a replay queued a second job")
	}
}

func TestFill_SameIDAsASubmitOrOtherTextIsReuse(t *testing.T) {
	q, _ := newQ(t)
	fillOK(t, q, "c1", "x", 1)
	if r, _ := q.SubmitFill(context.Background(), "s1", "c1", "other"); r.Reason != "client_msg_id_reused" {
		t.Fatalf("other text: %+v", r)
	}
	if r, _ := q.Submit(context.Background(), "s1", "c1", "x"); r.Reason != "client_msg_id_reused" {
		t.Fatalf("submit on a fill id: %+v", r)
	}
}

// Handed out, no result in time: unknown, and the ledger row says so; a late result is refused.
func TestFill_HandedWithoutResultIsUnknown(t *testing.T) {
	q, _ := newQ(t)
	ch := fillAsync(q, "s1", "c1", "x")
	j := next(t, q, "mod1", "s1")
	r := take(t, ch)
	if r.Status != Unknown || r.Phase != PhaseUnknown {
		t.Fatalf("result = %+v", r)
	}
	if got, ok := q.Lookup("c1"); !ok || got.Phase != PhaseUnknown {
		t.Fatalf("lookup = %+v %v", got, ok)
	}
	if err := q.Result("mod1", j.ID, Outcome{Status: Accepted, FilledAt: 1}); err != ErrNotLeased {
		t.Fatalf("late result: %v", err)
	}
}

// Never handed out: timeout, nothing recorded, may be sent again.
func TestFill_NeverHandedTimesOutAndMayBeResent(t *testing.T) {
	q, _ := newQ(t)
	if r := take(t, fillAsync(q, "s1", "c1", "x")); r.Status != Timeout {
		t.Fatalf("result = %+v", r)
	}
	if _, ok := q.Lookup("c1"); ok {
		t.Fatal("a withdrawn fill stayed in the ledger")
	}
}

func TestClearIf_GoesThroughTheModWithItsText(t *testing.T) {
	q, _ := newQ(t)
	ch := make(chan answer, 1)
	go func() { r, err := q.ClearIf(context.Background(), "s1", "draft"); ch <- answer{r, err} }()
	j := next(t, q, "mod1", "s1")
	if j.Kind != KindClearIf || j.Text != "draft" {
		t.Fatalf("job = %+v", j)
	}
	q.Result("mod1", j.ID, Outcome{Status: Dropped, Reason: "not_ours"})
	if r := take(t, ch); r.Status != Dropped || r.Reason != "not_ours" {
		t.Fatalf("result = %+v", r)
	}
}

func TestRead_ReturnsTheModsText(t *testing.T) {
	q, _ := newQ(t)
	ch := make(chan answer, 1)
	go func() { r, err := q.Read(context.Background(), "s1"); ch <- answer{r, err} }()
	j := next(t, q, "mod1", "s1")
	if j.Kind != KindRead || j.Text != "" {
		t.Fatalf("job = %+v", j)
	}
	q.Result("mod1", j.ID, Outcome{Status: Accepted, Text: "line1\nline2"})
	if r := take(t, ch); r.Status != Accepted || r.Text != "line1\nline2" {
		t.Fatalf("result = %+v", r)
	}
}
