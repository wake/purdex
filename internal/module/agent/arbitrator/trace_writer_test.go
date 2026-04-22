package arbitrator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent/observation"
	"github.com/wake/purdex/internal/store"
)

// fakeStepsAppender captures AppendSteps calls so trace-writer tests can
// assert on batch shape and ordering without a real SQLite store.
type fakeStepsAppender struct {
	mu      sync.Mutex
	batches [][]store.TraceStep
	err     error
}

func (f *fakeStepsAppender) AppendSteps(steps []store.TraceStep) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	clone := make([]store.TraceStep, len(steps))
	copy(clone, steps)
	f.batches = append(f.batches, clone)
	return nil
}

func (f *fakeStepsAppender) totalSteps() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.batches {
		n += len(b)
	}
	return n
}

func (f *fakeStepsAppender) batchCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.batches)
}

func makeRecord(source observation.SourceKind, phase observation.ObsPhase) TraceRecord {
	t0 := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	return TraceRecord{
		TraceID:      "trace-1",
		SessionID:    "sess-1",
		SourceKind:   source,
		Action:       "decision:ok",
		Phase:        phase,
		Status:       "success",
		Outcome:      "emitted",
		StartedAt:    t0,
		EndedAt:      t0,
		DropPriority: dropPriority(source, phase),
	}
}

func newTestTraceWriter(store StepsAppender, cap int, flushEvery time.Duration) *TraceWriter {
	return NewTraceWriter(TraceWriterOptions{
		Cap:        cap,
		FlushEvery: flushEvery,
		Store:      store,
		Now:        func() time.Time { return time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC) },
	})
}

func TestTraceWriter_Submit_UnderCap_Buffers(t *testing.T) {
	store := &fakeStepsAppender{}
	w := newTestTraceWriter(store, 10, time.Hour)
	for i := 0; i < 5; i++ {
		w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
	}
	if got := w.sizeForTesting(); got != 5 {
		t.Fatalf("buffer size = %d, want 5", got)
	}
	if n := store.batchCount(); n != 0 {
		t.Fatalf("batches flushed prematurely = %d", n)
	}
}

func TestTraceWriter_FlushTick_DrainsBuffer(t *testing.T) {
	store := &fakeStepsAppender{}
	w := newTestTraceWriter(store, 100, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	for i := 0; i < 3; i++ {
		w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.totalSteps() >= 3 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := store.totalSteps(); got != 3 {
		t.Fatalf("flushed steps = %d, want 3", got)
	}
	if got := w.sizeForTesting(); got != 0 {
		t.Fatalf("buffer not drained: size=%d", got)
	}
}

func TestTraceWriter_Submit_BufferFull_EvictsLowestPriority(t *testing.T) {
	ResetForTesting()
	store := &fakeStepsAppender{}
	w := newTestTraceWriter(store, 3, time.Hour)
	// Fill with worst-priority records: sweep+proposed → dp=4.
	for i := 0; i < 3; i++ {
		w.Submit(makeRecord(observation.SourceSweep, observation.PhaseProposed))
	}
	// Incoming hook+committed (dp=0) must evict one of the dp=4 slots.
	w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
	if got := w.sizeForTesting(); got != 3 {
		t.Fatalf("size after eviction = %d, want 3 (cap)", got)
	}
	// Evicted record carried dp=4 → metric tag priority=4 incremented.
	if Value("lights_trace_dropped", "priority=4") == 0 {
		t.Fatalf("expected lights_trace_dropped[priority=4] to increment")
	}
	// Verify buffer now contains one dp=0 record.
	buf := w.snapshotForTesting()
	var foundCommitted bool
	for _, r := range buf {
		if r.DropPriority == 0 {
			foundCommitted = true
		}
	}
	if !foundCommitted {
		t.Fatal("incoming dp=0 record not found in buffer after eviction")
	}
}

func TestTraceWriter_Submit_BufferFull_NoLowerPriority_DropsIncoming(t *testing.T) {
	ResetForTesting()
	store := &fakeStepsAppender{}
	w := newTestTraceWriter(store, 3, time.Hour)
	// Fill with highest-priority records (dp=0).
	for i := 0; i < 3; i++ {
		w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
	}
	// Incoming sweep+proposed (dp=4) must be dropped — nothing in buffer is
	// lower priority.
	w.Submit(makeRecord(observation.SourceSweep, observation.PhaseProposed))
	if got := w.sizeForTesting(); got != 3 {
		t.Fatalf("size after drop = %d, want 3", got)
	}
	if Value("lights_trace_dropped", "priority=4") == 0 {
		t.Fatalf("expected incoming dp=4 to be counted as dropped")
	}
}

func TestTraceWriter_Submit_Concurrent_Safe(t *testing.T) {
	store := &fakeStepsAppender{}
	w := newTestTraceWriter(store, 1000, time.Hour)
	const goroutines = 100
	const perG = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
			}
		}()
	}
	wg.Wait()
	if got := w.sizeForTesting(); got != 1000 {
		t.Fatalf("size = %d, want cap 1000 (all should fit since cap matches total capacity)", got)
	}
}

func TestTraceWriter_Shutdown_FlushesRemaining(t *testing.T) {
	store := &fakeStepsAppender{}
	w := newTestTraceWriter(store, 100, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)
	for i := 0; i < 7; i++ {
		w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer shutdownCancel()
	if err := w.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := store.totalSteps(); got != 7 {
		t.Fatalf("flushed = %d, want 7", got)
	}
}

func TestTraceWriter_Shutdown_ContextCancelled_ExitsWithoutFlush(t *testing.T) {
	store := &fakeStepsAppender{}
	w := newTestTraceWriter(store, 100, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)
	for i := 0; i < 3; i++ {
		w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
	}
	cancel()
	// Supply an already-cancelled ctx to Shutdown; writer skips the final
	// flush attempt and returns promptly.
	deadCtx, deadCancel := context.WithCancel(context.Background())
	deadCancel()
	if err := w.Shutdown(deadCtx); err != nil {
		t.Fatalf("Shutdown with dead ctx: %v", err)
	}
	// Do not assert totalSteps — the test contract is "does not panic and
	// returns quickly"; the loop goroutine may have flushed before ctx.Done
	// raced it, which is fine.
}

func TestTraceWriter_StoreWriteFailure_LoggedNotPanic(t *testing.T) {
	store := &fakeStepsAppender{err: errors.New("db down")}
	w := newTestTraceWriter(store, 100, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	for i := 0; i < 3; i++ {
		w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
	}
	// Give the flush tick a chance to fire; writer must survive the error
	// without panicking. The follow-up Submit must also work.
	time.Sleep(30 * time.Millisecond)
	w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
	cancel()
}

func TestTraceWriter_MetricCounterPerPriority(t *testing.T) {
	ResetForTesting()
	store := &fakeStepsAppender{}
	w := newTestTraceWriter(store, 1, time.Hour)
	// Start at dp=4 (sweep+proposed).
	w.Submit(makeRecord(observation.SourceSweep, observation.PhaseProposed))
	// Incoming dp=0 evicts the dp=4 → priority=4 tag +1.
	w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
	// Buffer now holds dp=0. Incoming dp=4 cannot evict → priority=4 tag +1.
	w.Submit(makeRecord(observation.SourceSweep, observation.PhaseProposed))
	if got := Value("lights_trace_dropped", "priority=4"); got != 2 {
		t.Fatalf("priority=4 counter = %d, want 2", got)
	}
	if got := Value("lights_trace_dropped", "priority=0"); got != 0 {
		t.Fatalf("priority=0 counter = %d, want 0", got)
	}
}

// TestTraceWriter_Counter_Reset confirms the metrics counter pathway doesn't
// leak between tests (defensive; all trace-writer tests call ResetForTesting).
func TestTraceWriter_Counter_Reset(t *testing.T) {
	ResetForTesting()
	if got := Value("lights_trace_dropped", "priority=0"); got != 0 {
		t.Fatalf("stale counter: %d", got)
	}
	if got := Value("lights_trace_dropped", "priority=4"); got != 0 {
		t.Fatalf("stale counter: %d", got)
	}
}
