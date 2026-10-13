package reads

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/reads"
)

// Re-review of the second round: the two requirements pull against each other and are met together — the queue is bounded
// AND nothing is lost, because past its bound the queue is rebuilt from lastRead (the bounded, authoritative state); and
// Stop waits for a delivery, but never longer than a short local limit, so one stuck callback cannot eat the daemon's whole
// shutdown budget.

// A subscriber that is stuck while far more distinct codes than the bound are read: the pending queue never exceeds its
// bound, and once the subscriber is back it ends with the latest cursor of every code lastRead still holds. Mutation gate:
// an unbounded queue / dropping the oldest instead of rebuilding from lastRead → red.
func TestFeed_PastItsBoundTheQueueIsRebuiltFromLastRead(t *testing.T) {
	store := reads.NewStore(100) // the module's real bound is 4096, for both lastRead and the queue
	f := newReadFeed(store.Snapshot)
	f.max = 100
	defer f.stop(nil)
	release := make(chan struct{})
	var mu sync.Mutex
	seen := map[string]uint64{}
	var first atomic.Bool
	f.subscribe(func(r reads.Read) {
		if first.CompareAndSwap(false, true) {
			<-release
		}
		mu.Lock()
		seen[r.Code] = r.Through.Seq
		mu.Unlock()
	})
	const n = 500
	maxPending := 0
	for i := 0; i < n; i++ {
		rd := reads.Read{Code: fmt.Sprintf("c%04d", i), Through: reads.Cursor{Epoch: "e", Seq: uint64(i + 1)}}
		store.Advance(rd.Code, rd.Through) // the module advances lastRead first, then publishes
		f.publish(rd)
		f.mu.Lock()
		for _, s := range f.subs {
			s.mu.Lock()
			if l := len(s.order); l > maxPending {
				maxPending = l
			}
			s.mu.Unlock()
		}
		f.mu.Unlock()
	}
	if maxPending > 101 { // the bound, plus the read that triggered a rebuild
		t.Fatalf("the pending queue reached %d, its bound is 100 (+1)", maxPending)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		missing := 0
		for _, held := range store.Snapshot() {
			if seen[held.Code] != held.Through.Seq {
				missing++
			}
		}
		mu.Unlock()
		if missing == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d codes that lastRead holds never reached the subscriber", missing)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// "The oldest pending" is the wrong victim: a code that first became pending long ago but was updated just now (coalesced in
// place) is the newest thing lastRead holds. On overflow it must survive. Mutation gate: drop the first-pending code → red.
func TestFeed_AnOverflowKeepsARecentlyUpdatedCode(t *testing.T) {
	store := reads.NewStore(100)
	f := newReadFeed(store.Snapshot)
	f.max = 100
	defer f.stop(nil)
	release := make(chan struct{})
	var mu sync.Mutex
	seen := map[string]uint64{}
	var first atomic.Bool
	f.subscribe(func(r reads.Read) {
		if first.CompareAndSwap(false, true) {
			<-release
		}
		mu.Lock()
		seen[r.Code] = r.Through.Seq
		mu.Unlock()
	})
	pub := func(code string, seq uint64) {
		rd := reads.Read{Code: code, Through: reads.Cursor{Epoch: "e", Seq: seq}}
		store.Advance(rd.Code, rd.Through)
		f.publish(rd)
	}
	pub("blocker", 1) // the subscriber is stuck inside this one
	time.Sleep(30 * time.Millisecond)
	pub("X", 1) // first pending
	for i := 0; i < 99; i++ {
		pub(fmt.Sprintf("c%02d", i), 1) // the queue is now at its bound of 100
	}
	pub("X", 2)    // coalesced in place: X is also the most recently updated code in lastRead
	pub("Y", 1)    // one more distinct code: overflow
	close(release) // (blocker + 100 pending + Y)
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		got := seen["X"]
		mu.Unlock()
		if got == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("X, updated to seq 2 and still held by lastRead, never reached the subscriber (saw %d)", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// One callback that never returns costs Stop at most its own short wait, not the caller's whole context, and Stop says so.
// Mutation gate: wait for the caller's context only → red.
func TestFeed_StopWaitsOnlyAShortWhileForAStuckCallback(t *testing.T) {
	f := newReadFeed(nil)
	f.wait = 50 * time.Millisecond
	release, entered := make(chan struct{}), make(chan struct{})
	defer close(release)
	f.subscribe(func(reads.Read) { close(entered); <-release })
	f.publish(reads.Read{Code: "a", Through: reads.Cursor{Epoch: "e", Seq: 1}})
	<-entered
	start := time.Now()
	err := f.stop(nil) // no deadline of the caller's own
	if err == nil {
		t.Fatal("stop returned nil while a delivery was stuck")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("stop waited %v for one stuck callback", d)
	}
}
