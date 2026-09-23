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
// A caller whose context has ended never comes back holding the slot: when
// the slot frees as ctx ends (or ctx has already ended and the slot is free),
// both select cases are ready and select may pick the send, so a successful
// send is followed by a ctx check that hands the slot straight back.
func (s slot) acquire(ctx context.Context) error {
	select {
	case s <- struct{}{}:
		if err := ctx.Err(); err != nil {
			s.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release frees a slot taken by acquire.
func (s slot) release() { <-s }
