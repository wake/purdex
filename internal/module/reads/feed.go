package reads

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/wake/purdex/internal/reads"
)

// readFeed delivers accepted reads to in-process subscribers (BG-4b's push side). Each subscriber has a worker goroutine and a
// coalescing queue: a read for a code that is already pending replaces the pending cursor in place (the order is that in
// which each code first became pending), so a subscriber that falls behind still ends with the latest cursor of every code
// and a request never waits for it. Nothing pending is ever dropped: the codes it can hold are those the daemon accepted a
// read for, i.e. sessions of a known list or sessions it has seen, so the queue is bounded by the sessions there are.
type readFeed struct {
	mu   sync.Mutex
	subs map[int]*feedSub
	next int
	wg   sync.WaitGroup
}

type feedSub struct {
	mu      sync.Mutex
	pending map[string]reads.Cursor
	order   []string
	wake    chan struct{}
	done    chan struct{}
	once    sync.Once
}

func (s *feedSub) close() { s.once.Do(func() { close(s.done) }) }

func (s *feedSub) closed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *feedSub) push(rd reads.Read) {
	s.mu.Lock()
	if _, ok := s.pending[rd.Code]; ok {
		s.pending[rd.Code] = rd.Through
	} else {
		s.pending[rd.Code] = rd.Through
		s.order = append(s.order, rd.Code)
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default: // a wake-up is already waiting
	}
}

// take removes the oldest pending read.
func (s *feedSub) take() (reads.Read, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.order) == 0 {
		return reads.Read{}, false
	}
	code := s.order[0]
	s.order = s.order[1:]
	cur := s.pending[code]
	delete(s.pending, code)
	return reads.Read{Code: code, Through: cur}, true
}

func newReadFeed() *readFeed { return &readFeed{subs: map[int]*feedSub{}} }

func (f *readFeed) SubscribeReads(fn func(reads.Read)) func() { return f.subscribe(fn) }

func (f *readFeed) subscribe(fn func(reads.Read)) func() {
	s := &feedSub{pending: map[string]reads.Cursor{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
	f.mu.Lock()
	id := f.next
	f.next++
	f.subs[id] = s
	f.wg.Add(1)
	f.mu.Unlock()
	go func() {
		defer f.wg.Done()
		for {
			select {
			case <-s.wake:
			case <-s.done:
				return
			}
			for {
				if s.closed() { // after unsubscribe nothing queued is delivered; only a callback already running finishes
					return
				}
				rd, ok := s.take()
				if !ok {
					break
				}
				fn(rd)
			}
		}
	}()
	return func() {
		f.mu.Lock()
		delete(f.subs, id)
		f.mu.Unlock()
		s.close()
	}
}

func (f *readFeed) publish(rd reads.Read) {
	f.mu.Lock()
	subs := make([]*feedSub, 0, len(f.subs))
	for _, s := range f.subs {
		subs = append(subs, s)
	}
	f.mu.Unlock()
	for _, s := range subs {
		s.push(rd)
	}
}

// stop ends every subscription and waits for the workers to return (or for ctx to end, which is the shutdown budget), so
// nothing is delivered after it returns nil; when ctx ends first it says so. It can be called again. A nil ctx waits for
// as long as it takes.
func (f *readFeed) stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	f.mu.Lock()
	subs := f.subs
	f.subs = map[int]*feedSub{}
	f.mu.Unlock()
	for _, s := range subs {
		s.close()
	}
	done := make(chan struct{})
	go func() { f.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		log.Printf("[reads] a read subscriber had not returned when Stop's budget ended")
		return fmt.Errorf("reads: a feed subscriber had not returned: %w", ctx.Err())
	}
}
