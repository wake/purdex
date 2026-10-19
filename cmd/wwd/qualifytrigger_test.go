package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Notify never waits: the facts callback calls it under the daemon's own locks, so a tick that is still running must not hold it.
func TestNotifyNeverBlocksAndCoalesces(t *testing.T) {
	tr := newQualifyTrigger()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			tr.Notify()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Notify blocked with nobody running the ticks")
	}
	assert.Len(t, tr.ch, 1, "a thousand records ask for one tick")
}

// A tick runs on the trigger's own goroutine after a Notify, one at a time; what arrives while one runs is one more tick, not many.
func TestRunTicksOnNotifyOneAtATimeAndStopsWithItsContext(t *testing.T) {
	tr := newQualifyTrigger()
	var running, overlap, ticks atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		tr.Run(ctx, func(context.Context) {
			if running.Add(1) > 1 {
				overlap.Add(1)
			}
			ticks.Add(1)
			started <- struct{}{}
			<-release
			running.Add(-1)
		})
		close(stopped)
	}()
	tr.Notify()
	<-started
	for i := 0; i < 50; i++ { // while the first tick runs
		tr.Notify()
	}
	release <- struct{}{} // first tick ends
	<-started             // the one tick the 50 records asked for
	release <- struct{}{}
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop with its context")
	}
	require.EqualValues(t, 2, ticks.Load())
	assert.EqualValues(t, 0, overlap.Load())
}

// Without a Notify nothing ticks.
func TestRunDoesNotTickByItself(t *testing.T) {
	tr := newQualifyTrigger()
	var ticks atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	tr.Run(ctx, func(context.Context) { ticks.Add(1) })
	assert.EqualValues(t, 0, ticks.Load())
}
