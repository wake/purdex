package session

import (
	"context"
	"runtime"
	"testing"
)

// slotFree reports whether nobody holds s, leaving it free either way.
func slotFree(s slot) bool {
	select {
	case s <- struct{}{}:
		<-s
		return true
	default:
		return false
	}
}

// With the slot free and the context already ended, both select cases are
// ready and select picks one at random; repeated, a missing check after the
// send shows up at once (#1293 R1).
func TestSlot_EndedContextNeverTakesAFreeSlot(t *testing.T) {
	s := newSlot()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 1000; i++ {
		if err := s.acquire(ctx); err == nil {
			t.Fatalf("iteration %d: acquire with an ended context returned nil", i)
		}
		if !slotFree(s) {
			t.Fatalf("iteration %d: an ended context left the slot held", i)
		}
	}
}

// Cancellation and release interleaved: the context always ends before the
// holder releases, so whatever the scheduling (the waiter still on its way to
// select, where both cases are then ready, or parked in it), the waiter must
// come back with an error and must not keep the slot (#1293 R1).
func TestSlot_CancelledWaiterNeverHoldsTheSlot(t *testing.T) {
	s := newSlot()
	for i := 0; i < 5000; i++ {
		if err := s.acquire(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- s.acquire(ctx) }()
		runtime.Gosched()
		cancel()
		s.release()
		if err := <-result; err == nil {
			t.Fatalf("iteration %d: a waiter whose context ended before the slot freed took it", i)
		}
		if !slotFree(s) {
			t.Fatalf("iteration %d: a cancelled waiter left the slot held", i)
		}
	}
}
