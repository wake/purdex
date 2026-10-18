package queue

import "sync"

// sequencer hands events out in commit order. A ticket is taken inside the transaction, while the database's write lock is held, so
// ticket numbers follow commit order exactly; after its commit a writer waits for its turn, publishes, and passes the turn on. Without
// it, a transaction that commits second could publish first, and a subscriber would see an older state of a job after a newer one.
//
// Nothing is held while the transaction is open (a ticket is a number), so the sequencer never waits on the database. The one thing
// it forbids is a Publish that waits for a later ticket: a subscriber must not call a queue method that writes.
type sequencer struct {
	mu   sync.Mutex
	cond *sync.Cond
	next uint64 // the next ticket to hand out
	turn uint64 // the ticket allowed to publish now
}

func newSequencer() *sequencer {
	s := &sequencer{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// ticket returns the next number. Call it inside the transaction, after the writes and before COMMIT.
func (s *sequencer) ticket() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.next
	s.next++
	return t
}

// await blocks until it is ticket t's turn.
func (s *sequencer) await(t uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.turn != t {
		s.cond.Wait()
	}
}

// done passes the turn on. A ticket whose transaction failed after it was taken must still be awaited and done, or every later
// writer waits for it forever.
func (s *sequencer) done(t uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turn = t + 1
	s.cond.Broadcast()
}
