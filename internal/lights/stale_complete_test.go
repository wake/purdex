package lights

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

func playAt(s *StreamState, seq int64, typ, data string, at time.Time) {
	ev := e(typ, data)
	ev.Seq, ev.At = seq, at.UnixMilli()
	s.Apply(ev, at)
}

// synthetic A complete → B start → the engine's late A complete: B keeps running, and A's abort is not re-recorded.
func TestStaleComplete_ALateCompletionOfAnEarlierTurnDoesNotEndTheNextOne(t *testing.T) {
	s := NewStreamState("s")
	playAt(s, 1, modevents.TypeTurnStart, `{"turn_id":"A"}`, t0)
	playAt(s, 2, modevents.TypeTurnComplete, `{"turn_id":"A","reason":"aborted","aborted":true}`, t0.Add(time.Second))
	playAt(s, 3, modevents.TypeTurnStart, `{"turn_id":"B"}`, t0.Add(2*time.Second))
	if s.TurnID != "B" || s.Status() != "running" || !s.AbortedAt.IsZero() {
		t.Fatalf("turn %q status %q abortedAt %v before the late completion", s.TurnID, s.Status(), s.AbortedAt)
	}
	playAt(s, 4, modevents.TypeTurnComplete, `{"turn_id":"A","reason":"aborted","aborted":true}`, t0.Add(3*time.Second))
	if s.TurnID != "B" || s.Status() != "running" {
		t.Errorf("a late completion of A ended B: turn %q status %q", s.TurnID, s.Status())
	}
	if !s.AbortedAt.IsZero() {
		t.Errorf("AbortedAt = %v: a stale completion must not date an abort against B", s.AbortedAt)
	}
	playAt(s, 5, modevents.TypeTurnComplete, `{"turn_id":"B","reason":"answer"}`, t0.Add(4*time.Second))
	if s.TurnID != "" || s.Status() != "idle" {
		t.Errorf("B's own completion did not end it: turn %q status %q", s.TurnID, s.Status())
	}
}

// B's turn.start was lost: its aborted completion names an id the stream has never closed, so it is NOT late — the
// interruption must still be recorded (inequality with the running id alone is no evidence).
func TestStaleComplete_ALostTurnStartDoesNotDiscardTheAbortedCompletion(t *testing.T) {
	s := NewStreamState("s")
	playAt(s, 1, modevents.TypeTurnStart, `{"turn_id":"A"}`, t0)
	playAt(s, 2, modevents.TypeTurnComplete, `{"turn_id":"A","reason":"answer"}`, t0.Add(time.Second))
	playAt(s, 3, modevents.TypeTurnStart, `{"turn_id":"A2"}`, t0.Add(2*time.Second)) // the running id the stream holds
	// B's turn.start never arrived; B is aborted:
	at := t0.Add(3 * time.Second)
	playAt(s, 5, modevents.TypeTurnComplete, `{"turn_id":"B","reason":"aborted","aborted":true}`, at)
	if s.TurnID != "" || s.Status() != "idle" {
		t.Errorf("the aborted completion was dropped: turn %q status %q", s.TurnID, s.Status())
	}
	if !s.AbortedAt.Equal(at) {
		t.Errorf("AbortedAt = %v, want %v: the interruption outcome was lost", s.AbortedAt, at)
	}
}

func TestStaleComplete_OnlyAKnownClosedIdIsStale(t *testing.T) {
	s := NewStreamState("s")
	if s.staleComplete("x") || s.staleComplete("") {
		t.Error("nothing is stale before any turn closed")
	}
	s.closeTurn("A")
	s.TurnID = "B"
	if !s.staleComplete("A") || s.staleComplete("") || s.staleComplete("B") || s.staleComplete("C") {
		t.Error("only a closed id other than the running one is stale")
	}
	s.TurnID = "A" // the same id started again: it is the running turn, not a late one
	if s.staleComplete("A") {
		t.Error("the running turn's own completion is never stale")
	}
}

func TestStaleComplete_TheClosedSetIsBoundedAndClearedByANewSession(t *testing.T) {
	s := NewStreamState("s")
	for i := 0; i < closedTurnsKept+5; i++ {
		s.closeTurn(string(rune('a' + i)))
	}
	if len(s.Closed) != closedTurnsKept {
		t.Errorf("kept %d ids, want %d", len(s.Closed), closedTurnsKept)
	}
	playAt(s, 1, modevents.TypeSessionSwitch, `{}`, t0)
	if len(s.Closed) != 0 {
		t.Errorf("a new session kept %v", s.Closed)
	}
}
