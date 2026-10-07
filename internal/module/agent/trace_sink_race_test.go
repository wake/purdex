package agent

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/store"
)

func listAllChains(t *testing.T, traces *store.TraceStore) []store.TraceChain {
	t.Helper()
	page, err := traces.ListChains(store.TraceListFilter{Limit: 100000})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	return page.Chains
}

// T2: deterministic interleave. Enqueue is parked between the closed check
// and the send; Close must block (RLock) until Enqueue finishes, and the
// record must be persisted.
func TestHookTraceSink_CloseBlocksUntilInFlightEnqueueFinishes(t *testing.T) {
	sink, traces := newCloseTestSink(t)

	reached := make(chan struct{})
	release := make(chan struct{})
	enqueueAfterCheckHook = func() {
		close(reached)
		<-release
	}
	t.Cleanup(func() { enqueueAfterCheckHook = nil })

	enqueueDone := make(chan struct{})
	go func() {
		defer close(enqueueDone)
		sink.Enqueue(closeTestRecord("inflight"))
	}()
	<-reached

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		sink.Close()
	}()

	// Negative wait: with the RLock in place Close cannot finish before
	// release; without it Close completes immediately and the send panics.
	select {
	case <-closeDone:
		close(release)
		<-enqueueDone
		t.Fatal("Close returned while an Enqueue was parked between check and send")
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	<-enqueueDone
	<-closeDone

	chains := listAllChains(t, traces)
	if len(chains) != 1 || chains[0].ChainID != "inflight" {
		t.Fatalf("persisted chains = %+v, want exactly [inflight]", chains)
	}
	if got := sink.dropped.Load(); got != 0 {
		t.Fatalf("dropped = %d, want 0", got)
	}
}

// T3: concurrent producers racing Close (run with -race). Total sends stay
// below the queue capacity so queue-full drops cannot occur and accounting
// is exact: stored + dropped == sent.
func TestHookTraceSink_ConcurrentEnqueueAndClose(t *testing.T) {
	sink, traces := newCloseTestSink(t)
	captureLog(t)

	const producers = 4
	const perProducer = 60
	var wg sync.WaitGroup
	start := make(chan struct{})
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			<-start
			for i := 0; i < perProducer; i++ {
				sink.Enqueue(closeTestRecord(fmt.Sprintf("p%d-%d", p, i)))
			}
		}(p)
	}
	close(start)
	sink.Close()
	wg.Wait()

	stored := int64(len(listAllChains(t, traces)))
	dropped := sink.dropped.Load()
	if stored+dropped != producers*perProducer {
		t.Fatalf("stored(%d)+dropped(%d) != sent(%d)", stored, dropped, producers*perProducer)
	}
}

// T7: FlushForTest racing Enqueue must not misuse the WaitGroup (-race).
func TestHookTraceSink_FlushForTestConcurrentWithEnqueue(t *testing.T) {
	sink, _ := newCloseTestSink(t)
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			sink.Enqueue(closeTestRecord(fmt.Sprintf("f-%d", i)))
		}
	}()
	for i := 0; i < 200; i++ {
		sink.FlushForTest()
	}
	stop.Store(true)
	wg.Wait()
	sink.FlushForTest()
}
