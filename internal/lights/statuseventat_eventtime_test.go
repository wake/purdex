package lights

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

// StatusEventAt is the time a light event happened (the event's own at, the
// mod's Date.now()), not the time the daemon received it: the mod's events
// reach the daemon a second or more late, and a hook that arrived in between
// must not be mistaken for older than them. The mod reaches the daemon only
// through a Unix socket on the same host, so both stamps come off one wall
// clock.

func turnStartAt(at time.Time) modevents.Event {
	ev := e(modevents.TypeTurnStart, `{"turn_id":"t1"}`)
	if !at.IsZero() {
		ev.At = at.UnixMilli()
	}
	return ev
}

// TestStatusEventAt_UsesEventTime: an event received late reports when it
// happened.
func TestStatusEventAt_UsesEventTime(t *testing.T) {
	s := NewStreamState("s")
	happened := t0.Add(12750 * time.Millisecond)
	received := t0.Add(13950 * time.Millisecond)
	s.Apply(turnStartAt(happened), received)
	if !s.StatusEventAt.Equal(happened) {
		t.Fatalf("StatusEventAt = %v, want the event time %v (received %v)", s.StatusEventAt, happened, received)
	}
	if !s.LastEvent.Equal(received) {
		t.Fatalf("LastEvent = %v, want the receive time %v", s.LastEvent, received)
	}
}

// TestStatusEventAt_ClampedToReceiveTime: an event stamped in the future (a
// skewed or broken mod clock) counts as happening now, or a hook edge could
// never beat it again.
func TestStatusEventAt_ClampedToReceiveTime(t *testing.T) {
	s := NewStreamState("s")
	received := t0.Add(5 * time.Second)
	s.Apply(turnStartAt(received.Add(time.Hour)), received)
	if !s.StatusEventAt.Equal(received) {
		t.Fatalf("StatusEventAt = %v, want it clamped to %v", s.StatusEventAt, received)
	}
}

// TestStatusEventAt_MissingAtFallsBackToReceiveTime: an event without a time
// counts as happening on arrival, or an edge would always beat it.
func TestStatusEventAt_MissingAtFallsBackToReceiveTime(t *testing.T) {
	for _, at := range []int64{0, -5} {
		s := NewStreamState("s")
		ev := turnStartAt(time.Time{})
		ev.At = at
		received := t0.Add(5 * time.Second)
		s.Apply(ev, received)
		if !s.StatusEventAt.Equal(received) {
			t.Fatalf("at=%d: StatusEventAt = %v, want the receive time %v", at, s.StatusEventAt, received)
		}
	}
}

// TestStatusEventAt_IsMonotonic: an event that arrives out of order with an
// older at never moves it back.
func TestStatusEventAt_IsMonotonic(t *testing.T) {
	s := NewStreamState("s")
	s.Apply(turnStartAt(t0.Add(10*time.Second)), t0.Add(11*time.Second))
	s.Apply(e(modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"answer"}`), t0.Add(12*time.Second)) // at=0: the receive time
	if want := t0.Add(12 * time.Second); !s.StatusEventAt.Equal(want) {
		t.Fatalf("StatusEventAt = %v, want %v", s.StatusEventAt, want)
	}
	older := turnStartAt(t0.Add(3 * time.Second))
	s.Apply(older, t0.Add(13*time.Second))
	if want := t0.Add(12 * time.Second); !s.StatusEventAt.Equal(want) {
		t.Fatalf("an older event moved StatusEventAt to %v, want it kept at %v", s.StatusEventAt, want)
	}
}
