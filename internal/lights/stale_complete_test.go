package lights

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

// synthetic A complete → B start → the engine's late A complete: B keeps running, and A's abort is not re-recorded.
func TestStaleComplete_ALateCompletionOfAnEarlierTurnDoesNotEndTheNextOne(t *testing.T) {
	s := NewStreamState("s")
	play1 := func(seq int64, typ, data string, at time.Time) {
		ev := e(typ, data)
		ev.Seq, ev.At = seq, at.UnixMilli()
		s.Apply(ev, at)
	}
	play1(1, modevents.TypeTurnStart, `{"turn_id":"A"}`, t0)
	play1(2, modevents.TypeTurnComplete, `{"turn_id":"A","reason":"aborted","aborted":true}`, t0.Add(time.Second))
	play1(3, modevents.TypeTurnStart, `{"turn_id":"B"}`, t0.Add(2*time.Second))
	if s.TurnID != "B" || s.Status() != "running" {
		t.Fatalf("turn %q status %q before the late completion", s.TurnID, s.Status())
	}
	play1(4, modevents.TypeTurnComplete, `{"turn_id":"A","reason":"aborted","aborted":true}`, t0.Add(3*time.Second))
	if s.TurnID != "B" || s.Status() != "running" {
		t.Errorf("a late completion of A ended B: turn %q status %q", s.TurnID, s.Status())
	}
	if !s.AbortedAt.IsZero() {
		t.Errorf("AbortedAt = %v: a stale completion must not date an abort against B", s.AbortedAt)
	}
	play1(5, modevents.TypeTurnComplete, `{"turn_id":"B","reason":"answer"}`, t0.Add(4*time.Second))
	if s.TurnID != "" || s.Status() != "idle" {
		t.Errorf("B's own completion did not end it: turn %q status %q", s.TurnID, s.Status())
	}
}

func TestStaleComplete_NoIdOrNoTurnIsNeverStale(t *testing.T) {
	s := NewStreamState("s")
	if s.staleComplete("x") || s.staleComplete("") {
		t.Error("with no turn running nothing is stale")
	}
	s.TurnID = "B"
	if s.staleComplete("") || s.staleComplete("B") || !s.staleComplete("A") {
		t.Error("only a different non-empty id is stale")
	}
}
