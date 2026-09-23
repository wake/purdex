package session

import "context"

// slot is a one-holder lock whose acquisition can be abandoned: a caller
// queued behind a holder stuck in a slow tmux read gives up when its own
// context ends, instead of waiting the holder out (#1293 §3.2). It replaces a
// sync.Mutex wherever the critical section is a bounded session-list read.
//
// The zero value is unusable; build one with newSlot.
type slot chan struct{}

func newSlot() slot { return make(slot, 1) }

// acquire takes the slot, or returns ctx.Err() once ctx ends while waiting.
// A context that has already ended never takes the slot, even a free one, so
// the outcome does not depend on select's random choice.
func (s slot) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release frees a slot taken by acquire.
func (s slot) release() { <-s }
