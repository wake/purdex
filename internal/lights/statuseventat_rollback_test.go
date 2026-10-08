package lights

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

// StatusEventAt is a high-water mark, and one that sits in the future (the
// wall clock was set back by NTP or a wake from sleep) would beat every later
// event, and every hook edge would lose to it. Apply notices the mark ahead of
// the receive time and starts it over.

// TestStatusEventAt_ResetsAfterClockRollback: an event at 12:00:09 received at
// 12:00:10; the clock goes back and a turn.complete that happened at 12:00:02 is
// received at 12:00:03. The mark follows it instead of staying at 12:00:09.
func TestStatusEventAt_ResetsAfterClockRollback(t *testing.T) {
	s := NewStreamState("s")
	s.Apply(turnStartAt(t0.Add(9*time.Second)), t0.Add(10*time.Second))
	if want := t0.Add(9 * time.Second); !s.StatusEventAt.Equal(want) {
		t.Fatalf("before the rollback: StatusEventAt = %v, want %v", s.StatusEventAt, want)
	}

	complete := e(modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`)
	complete.At = t0.Add(2 * time.Second).UnixMilli()
	s.Apply(complete, t0.Add(3*time.Second))
	if want := t0.Add(2 * time.Second); !s.StatusEventAt.Equal(want) {
		t.Fatalf("after the rollback: StatusEventAt = %v, want %v", s.StatusEventAt, want)
	}
}

// TestStatusEventAt_ResetsOnAnyEventAfterRollback: an event that does not move
// the light (a heartbeat) also clears a mark that is ahead of the clock, so the
// next hook edge is not held down until the next light event.
func TestStatusEventAt_ResetsOnAnyEventAfterRollback(t *testing.T) {
	s := NewStreamState("s")
	s.Apply(turnStartAt(t0.Add(9*time.Second)), t0.Add(10*time.Second))
	s.Apply(e(modevents.TypeHeartbeat, `{"turn_id":"t1"}`), t0.Add(2*time.Second))
	if !s.StatusEventAt.IsZero() {
		t.Fatalf("StatusEventAt = %v, want it reset", s.StatusEventAt)
	}
}
