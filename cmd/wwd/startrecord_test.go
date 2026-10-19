package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// R2: the start record is the crash-loop rule's only input; one transient failure of the write must not lose it for good.
func TestRecordStartRetriesAfterATransientFailure(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{})
	write := func() error {
		if calls.Add(1) < 3 {
			return errors.New("database is locked")
		}
		close(done)
		return nil
	}
	var logged atomic.Int32
	recordStart(context.Background(), write, []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}, func(string, ...any) { logged.Add(1) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("never written after %d tries", calls.Load())
	}
	if calls.Load() != 3 || logged.Load() == 0 {
		t.Fatalf("calls %d logged %d", calls.Load(), logged.Load())
	}
}

// A write that works the first time is made once, in the caller (start-up does not wait for retries).
func TestRecordStartWritesOnceWhenItWorks(t *testing.T) {
	var calls atomic.Int32
	recordStart(context.Background(), func() error { calls.Add(1); return nil }, []time.Duration{time.Millisecond}, func(string, ...any) {})
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("calls %d", calls.Load())
	}
}

// The retries are bounded and end with the process.
func TestRecordStartGivesUpAfterTheDelaysAndStopsOnCancel(t *testing.T) {
	var calls atomic.Int32
	fail := func() error { calls.Add(1); return errors.New("disk full") }
	recordStart(context.Background(), fail, []time.Duration{time.Millisecond, time.Millisecond}, func(string, ...any) {})
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 3 { // the first try and one per delay
		t.Fatalf("calls %d want 3", calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	var n atomic.Int32
	recordStart(ctx, func() error { n.Add(1); return errors.New("x") }, []time.Duration{time.Hour}, func(string, ...any) {})
	cancel()
	time.Sleep(20 * time.Millisecond)
	if n.Load() != 1 {
		t.Fatalf("a cancelled context must stop the wait: %d", n.Load())
	}
}
