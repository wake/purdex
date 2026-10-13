package reads

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/wake/purdex/internal/reads"
)

// readFeed delivers accepted reads to in-process subscribers (BG-4b's push side). Each subscriber has a worker goroutine and a
// coalescing queue: a read for a code that is already pending replaces the pending cursor in place (the order is that in
// which each code first became pending), so a subscriber that falls behind still ends with the latest cursor of every code
// and a request never waits for it. The queue is bounded (max codes); past the bound it is rebuilt from lastRead, which is
// the bounded, authoritative state of every read, so a subscriber that was far behind still ends with the latest cursor of
// every code lastRead holds, and nothing grows without limit.
type readFeed struct {
	mu       sync.Mutex
	subs     map[int]*feedSub
	next     int
	wg       sync.WaitGroup
	snapshot func() []reads.Read // lastRead, oldest update first; nil = drop the oldest pending code instead
	max      int                 // the bound of one subscriber's queue
	wait     time.Duration       // the longest stop waits for the workers, whatever its caller's deadline
}

// defaultWait is how long stop waits for a delivery at most: short, so one stuck callback cannot use up the shutdown budget
// that the modules stopped after this one share.
const defaultWait = time.Second

type feedSub struct {
	feed    *readFeed
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
		if len(s.order) >= s.feed.max {
			s.rebuild(rd)
		} else {
			s.pending[rd.Code] = rd.Through
			s.order = append(s.order, rd.Code)
		}
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default: // a wake-up is already waiting
	}
}

// rebuild replaces the queue by lastRead (which already holds rd, since the module advances it before it publishes), because
// there are more distinct codes pending than the bound. Without a snapshot the oldest pending code is dropped instead.
// Under s.mu.
func (s *feedSub) rebuild(rd reads.Read) {
	if s.feed.snapshot == nil {
		log.Printf("[reads] a read subscriber has %d codes pending: dropping %s", s.feed.max, s.order[0])
		delete(s.pending, s.order[0])
		s.order = s.order[1:]
		s.pending[rd.Code] = rd.Through
		s.order = append(s.order, rd.Code)
		return
	}
	s.pending, s.order = map[string]reads.Cursor{}, nil
	for _, held := range s.feed.snapshot() {
		s.pending[held.Code] = held.Through
		s.order = append(s.order, held.Code)
	}
	if _, ok := s.pending[rd.Code]; !ok { // evicted from lastRead already: still the newest thing there is to say
		s.pending[rd.Code] = rd.Through
		s.order = append(s.order, rd.Code)
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

func newReadFeed(snapshot func() []reads.Read) *readFeed {
	return &readFeed{subs: map[int]*feedSub{}, snapshot: snapshot, max: reads.DefaultMax, wait: defaultWait}
}

func (f *readFeed) SubscribeReads(fn func(reads.Read)) func() { return f.subscribe(fn) }

func (f *readFeed) subscribe(fn func(reads.Read)) func() {
	s := &feedSub{feed: f, pending: map[string]reads.Cursor{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
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

// stop ends every subscription and waits for the workers to return, so nothing is delivered after it returns nil. It waits
// at most f.wait and no longer than ctx (the shutdown budget, shared with the modules stopped after this one); when the
// workers have not returned by then it says so. It can be called again.
func (f *readFeed) stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, f.wait)
	defer cancel()
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
