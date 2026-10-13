package reads

import (
	"log"
	"sync"
	"time"

	"github.com/wake/purdex/internal/reads"
)

// readFeed delivers accepted reads to in-process subscribers (BG-4b's push side). Each subscriber has a worker goroutine and a
// coalescing queue: a read for a code that is already pending replaces the pending cursor in place (the order is that in
// which each code first became pending), so a subscriber that falls behind still ends with the latest cursor of every code
// and a request never waits for it. Only more than maxPending distinct codes at once would lose the oldest pending one.
type readFeed struct {
	mu   sync.Mutex
	subs map[int]*feedSub
	next int
	wg   sync.WaitGroup
}

// maxPending bounds one subscriber's queue: lastRead holds at most 4096 codes, so there is rarely more to wait for.
const maxPending = reads.DefaultMax

// stopWait is how long stop waits for the workers before it gives up and says so.
const stopWait = 2 * time.Second

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
		if len(s.order) >= maxPending {
			log.Printf("[reads] a read subscriber has %d codes pending: dropping %s", maxPending, s.order[0])
			delete(s.pending, s.order[0])
			s.order = s.order[1:]
		}
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

// stop ends every subscription and waits (briefly) for the workers, so nothing is delivered after it returns. It can be
// called again.
func (f *readFeed) stop() {
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
	case <-time.After(stopWait):
		log.Printf("[reads] a read subscriber did not return within %v of Stop", stopWait)
	}
}
