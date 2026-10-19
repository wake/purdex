package main

import "context"

// qualifyTrigger turns the facts cadence into ticks of the qualification coordinator without letting the tick run in the facts callback:
// a tick prepares a snapshot (git) and reads and writes the queue, and the callback runs under the daemon's own locks. Notify only leaves a
// mark (capacity one, so any number of records while a tick runs ask for one more tick); Run takes the marks on a goroutine of its own, one
// tick at a time.
type qualifyTrigger struct{ ch chan struct{} }

func newQualifyTrigger() *qualifyTrigger { return &qualifyTrigger{ch: make(chan struct{}, 1)} }

// Notify asks for a tick. It never waits.
func (t *qualifyTrigger) Notify() {
	select {
	case t.ch <- struct{}{}:
	default:
	}
}

// Run calls tick once for each mark taken, until ctx ends.
func (t *qualifyTrigger) Run(ctx context.Context, tick func(context.Context)) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.ch:
			tick(ctx)
		}
	}
}
