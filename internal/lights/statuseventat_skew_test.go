package lights

import (
	"testing"
	"time"
)

// An event's at that is far from the receive time cannot be the time the event
// happened: a Unix second sent as milliseconds, a version mismatch, a mod clock
// gone wrong. Such a value is not trusted; the event counts as happening on
// arrival, and the stream counts the refusal in AtRejected.

func applyTurnStartWithAt(t *testing.T, at int64, received time.Time) *StreamState {
	t.Helper()
	s := NewStreamState("s")
	ev := turnStartAt(time.Time{})
	ev.At = at
	s.Apply(ev, received)
	return s
}

// TestStatusEventAt_UnixSecondsFallsBackToReceiveTime: 1791409762 is a Unix
// second; read as milliseconds it is January 1970.
func TestStatusEventAt_UnixSecondsFallsBackToReceiveTime(t *testing.T) {
	received := time.UnixMilli(1791409762960)
	s := applyTurnStartWithAt(t, 1791409762, received)
	if !s.StatusEventAt.Equal(received) {
		t.Fatalf("StatusEventAt = %v, want the receive time %v", s.StatusEventAt, received)
	}
	if s.AtRejected != 1 {
		t.Fatalf("AtRejected = %d, want 1", s.AtRejected)
	}
}

// TestStatusEventAt_FarPastFallsBackToReceiveTime: older than the skew window.
func TestStatusEventAt_FarPastFallsBackToReceiveTime(t *testing.T) {
	received := t0.Add(10 * time.Minute)
	s := applyTurnStartWithAt(t, received.Add(-atSkewWindow-time.Second).UnixMilli(), received)
	if !s.StatusEventAt.Equal(received) {
		t.Fatalf("StatusEventAt = %v, want the receive time %v", s.StatusEventAt, received)
	}
	if s.AtRejected != 1 {
		t.Fatalf("AtRejected = %d, want 1", s.AtRejected)
	}
}

// TestStatusEventAt_FarFutureFallsBackToReceiveTime: later than the skew window.
func TestStatusEventAt_FarFutureFallsBackToReceiveTime(t *testing.T) {
	received := t0.Add(10 * time.Minute)
	s := applyTurnStartWithAt(t, received.Add(atSkewWindow+time.Second).UnixMilli(), received)
	if !s.StatusEventAt.Equal(received) {
		t.Fatalf("StatusEventAt = %v, want the receive time %v", s.StatusEventAt, received)
	}
	if s.AtRejected != 1 {
		t.Fatalf("AtRejected = %d, want 1", s.AtRejected)
	}
}

// TestStatusEventAt_InsideTheWindowIsTrusted: the window edge keeps an honest
// late event, and a missing at is not a rejection (nothing was sent).
func TestStatusEventAt_InsideTheWindowIsTrusted(t *testing.T) {
	received := t0.Add(10 * time.Minute)
	old := received.Add(-atSkewWindow)
	s := applyTurnStartWithAt(t, old.UnixMilli(), received)
	if !s.StatusEventAt.Equal(old) || s.AtRejected != 0 {
		t.Fatalf("StatusEventAt = %v AtRejected = %d, want %v 0", s.StatusEventAt, s.AtRejected, old)
	}
	s = applyTurnStartWithAt(t, 0, received)
	if !s.StatusEventAt.Equal(received) || s.AtRejected != 0 {
		t.Fatalf("a missing at: StatusEventAt = %v AtRejected = %d, want %v 0", s.StatusEventAt, s.AtRejected, received)
	}
}
