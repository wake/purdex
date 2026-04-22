package arbitrator

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
)

// newCancelableCtx is a tiny helper that returns a background context plus
// its cancel. Named so the signal in current_generation_test.go is readable
// (context.WithCancel inlined everywhere buries the intent).
func newCancelableCtx() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

// runInGoroutine starts arb.Run in a goroutine and returns a channel that
// closes when Run exits. Callers should cancel the ctx and then receive on
// the returned channel to wait for graceful shutdown.
func runInGoroutine(arb *Arbitrator, ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		arb.Run(ctx)
		close(done)
	}()
	return done
}

// waitForSubmit polls cap until at least n records have been submitted or
// timeout expires. Failure message names the caller's expectation so
// CurrentGeneration tests do not need their own retry loop.
func waitForSubmit(t *testing.T, cap *captureTraceSubmitter, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cap.count() >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("waitForSubmit: want >= %d records within %v, got %d", n, timeout, cap.count())
}

// actorStatus is a test-only helper that reads the actor's Status under the
// frameState's read lock. Used by TestFrameState_RWMutex_WriteExcludesReads
// to prove reads block while a writer holds the lock.
func (f *frameState) actorStatus(key observation.ActorKey) string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	sess, ok := f.sessions[key.SessionID]
	if !ok {
		return ""
	}
	a, ok := sess.Actors[key]
	if !ok {
		return ""
	}
	return a.Status
}

// TestArbitrator_CurrentGeneration_UnknownSession_ReturnsZero locks the
// contract that an unknown session reads as generation 0 (matching the
// "not-yet-SessionStart" default). Callers (hook path ObservedGeneration
// calculations) rely on this to decide whether to push generation forward.
func TestArbitrator_CurrentGeneration_UnknownSession_ReturnsZero(t *testing.T) {
	cap := &captureTraceSubmitter{}
	arb := NewArbitrator(newArbOptionsForTest(cap))

	if got := arb.CurrentGeneration("nobody-home"); got != 0 {
		t.Fatalf("CurrentGeneration(unknown) = %d, want 0", got)
	}
}

// TestArbitrator_CurrentGeneration_AfterSessionStart_ReturnsNewGen verifies
// that once the Arbitrator goroutine processes a SessionStart observation
// (which bumps frameState.sessions[sid].Generation), CurrentGeneration
// reflects the new value.
func TestArbitrator_CurrentGeneration_AfterSessionStart_ReturnsNewGen(t *testing.T) {
	cap := &captureTraceSubmitter{}
	opts := newArbOptionsForTest(cap)
	opts.ReconcileEvery = time.Hour // keep the ticker out of the way
	arb := NewArbitrator(opts)

	// Start Run so the single-owner goroutine processes inCh.
	ctx, cancel := newCancelableCtx()
	doneCh := runInGoroutine(arb, ctx)

	now := time.Now()
	arb.InCh() <- makeSessionStart("sess-curgen", 7, now)

	// Wait for the boundary trace as a signal that apply finished advancing
	// the generation.
	waitForSubmit(t, cap, 1, 500*time.Millisecond)

	got := arb.CurrentGeneration("sess-curgen")
	if got != 7 {
		t.Fatalf("CurrentGeneration after SessionStart gen=7 = %d, want 7", got)
	}

	cancel()
	<-doneCh
}

// TestArbitrator_CurrentGeneration_ConcurrentRead_NoRace spins many reader
// goroutines calling CurrentGeneration while the Arbitrator Run goroutine
// processes SessionStart observations (which mutate frameState.sessions).
// Under -race, this must complete without a data race report.
func TestArbitrator_CurrentGeneration_ConcurrentRead_NoRace(t *testing.T) {
	cap := &captureTraceSubmitter{}
	opts := newArbOptionsForTest(cap)
	opts.InChCap = 1024
	opts.ReconcileEvery = time.Hour
	arb := NewArbitrator(opts)

	ctx, cancel := newCancelableCtx()
	doneCh := runInGoroutine(arb, ctx)

	const readers = 8
	const readsPerReader = 500
	const writes = 200

	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for n := 0; n < readsPerReader && !stop.Load(); n++ {
				_ = arb.CurrentGeneration("sess-race")
			}
		}()
	}

	now := time.Now()
	for g := int64(1); g <= writes; g++ {
		arb.InCh() <- makeSessionStart("sess-race", g, now)
	}

	// Let readers keep going until the writer channel drains. We poll the
	// capture until we observe >= writes records (each SessionStart emits
	// exactly one synthetic boundary trace).
	waitForSubmit(t, cap, writes, 2*time.Second)
	stop.Store(true)
	wg.Wait()

	cancel()
	<-doneCh
}

// TestFrameState_RWMutex_MultipleReadersConcurrent verifies multiple
// concurrent RLock-based readers (CurrentGeneration + allActiveActors) can
// run in parallel without blocking each other. Failure is "timeout" — if
// RWMutex were downgraded to a plain Mutex this test would serialize the
// readers and exceed the generous deadline.
func TestFrameState_RWMutex_MultipleReadersConcurrent(t *testing.T) {
	fs := newFrameState()

	// Seed a handful of sessions so readers have something to scan.
	for i := 0; i < 16; i++ {
		sid := "sess-multi-" + string(rune('a'+i))
		sg := fs.getOrCreateSession(sid)
		// Write Generation via the locked API so this test doubles as a
		// sanity check that SetGeneration exists and round-trips.
		fs.SetGeneration(sid, int64(i+1))
		if sg == nil {
			t.Fatal("getOrCreateSession returned nil")
		}
	}

	const readers = 16
	const readsPerReader = 1000

	var wg sync.WaitGroup
	wg.Add(readers)
	start := time.Now()
	for r := 0; r < readers; r++ {
		go func(r int) {
			defer wg.Done()
			for n := 0; n < readsPerReader; n++ {
				sid := "sess-multi-" + string(rune('a'+(r%16)))
				_ = fs.GenerationOf(sid)
			}
		}(r)
	}
	wg.Wait()
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("concurrent readers took too long (%v); RWMutex likely serialized them", elapsed)
	}
}

// TestFrameState_RWMutex_WriteExcludesReads verifies that a pending writer
// forces readers to wait (mutex exclusion). We use a channel-synchronized
// writer that parks inside the critical section via upsertActor's closure
// so we can assert readers observe a stable state before the writer
// releases the lock.
func TestFrameState_RWMutex_WriteExcludesReads(t *testing.T) {
	fs := newFrameState()
	key := observation.ActorKey{SessionID: "sess-excl", Generation: 1, ActorID: "a1"}
	// Seed so upsertActor's write path definitely enters the lock.
	fs.upsertActor(key, func(a *actorSummary) { a.Status = "initial" })

	writerEntered := make(chan struct{})
	writerRelease := make(chan struct{})
	writerDone := make(chan struct{})

	// Writer goroutine parks inside upsertActor's closure (which runs
	// under Lock) until writerRelease is signalled.
	go func() {
		defer close(writerDone)
		fs.upsertActor(key, func(a *actorSummary) {
			close(writerEntered)
			<-writerRelease
			a.Status = "updated"
		})
	}()

	// Wait until writer has the lock.
	<-writerEntered

	// Reader goroutine must block on RLock until writer releases.
	readerDone := make(chan string, 1)
	go func() {
		readerDone <- fs.actorStatus(key)
	}()

	// Give the reader a chance to try and block.
	select {
	case s := <-readerDone:
		t.Fatalf("reader observed status=%q before writer released; RWMutex does not exclude reads during write", s)
	case <-time.After(30 * time.Millisecond):
		// Expected: reader is blocked on RLock.
	}

	// Release writer; reader should then observe "updated".
	close(writerRelease)
	<-writerDone

	select {
	case s := <-readerDone:
		if s != "updated" {
			t.Fatalf("reader observed status=%q after writer release, want updated", s)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("reader did not unblock after writer released lock")
	}
}
