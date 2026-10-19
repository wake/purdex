package teammod

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The per-approval lock of the lead name (name pool v2 spec §5.3): decides, the sweeper's expiry close and a person's approve of
// one approval run one at a time; two different approvals never wait for each other, and an entry is gone once nobody holds or
// waits for it.

// Mutation gate: one lock for every key → the "different keys" case is red; no lock at all → the "same key" case is red.
func TestKeyedMutex_SameKeySerialisesDifferentKeysDoNot(t *testing.T) {
	var k keyedMutex
	var inside, overlap atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := k.lock("a")
			if inside.Add(1) > 1 {
				overlap.Add(1)
			}
			time.Sleep(2 * time.Millisecond)
			inside.Add(-1)
			unlock()
		}()
	}
	wg.Wait()
	if overlap.Load() != 0 {
		t.Fatalf("%d holders of one key overlapped", overlap.Load())
	}

	holdA := k.lock("a")
	done := make(chan struct{})
	go func() {
		unlock := k.lock("b") // another key: must not wait for a
		unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a lock on key b waited for key a")
	}
	holdA()
}

func TestKeyedMutex_AnEntryIsFreedWhenNobodyHoldsOrWaits(t *testing.T) {
	var k keyedMutex
	u1 := k.lock("a")
	waiter := make(chan func())
	go func() { waiter <- k.lock("a") }()
	time.Sleep(20 * time.Millisecond) // the second caller is waiting now
	u1()
	if k.size() != 1 {
		t.Fatalf("size = %d with a waiter that took over, want 1", k.size())
	}
	(<-waiter)()
	if k.size() != 0 {
		t.Fatalf("size = %d after the last unlock, want 0 (the table must not grow with every approval)", k.size())
	}
}
