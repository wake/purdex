package arbitrator

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
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

var makeRecordCounter int64

func makeRecord(source observation.SourceKind, phase observation.ObsPhase) TraceRecord {
	t0 := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	// Atomic increment so concurrent tests (e.g. Submit_Concurrent_Safe)
	// generate unique SpanIDs without a data race.
	n := atomic.AddInt64(&makeRecordCounter, 1)
	// Stable synthetic SpanID so toStoreSteps keeps the record (C8: empty
	// SpanID is now a contract violation that triggers a skip + metric).
	spanID := "test-span-" + strconv.FormatInt(n, 10)
	return TraceRecord{
		TraceID:      "trace-1",
		SpanID:       spanID,
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

// ----- C4: flush retains buffer on store error -----------------------------

// TestTraceWriter_Flush_StoreError_RetainsBuffer verifies that a failed
// AppendSteps call leaves the buffer intact so the next flush tick retries
// the same records. Dropping the batch on error would silently lose
// committed traces.
func TestTraceWriter_Flush_StoreError_RetainsBuffer(t *testing.T) {
	store := &fakeStepsAppender{err: errors.New("db down")}
	w := newTestTraceWriter(store, 100, time.Hour)

	// Populate the buffer with 3 records.
	for i := 0; i < 3; i++ {
		w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
	}
	if got := w.sizeForTesting(); got != 3 {
		t.Fatalf("pre-flush buffer size = %d, want 3", got)
	}

	// Manually invoke flush; the store will return an error.
	w.flush()

	// Buffer must still hold 3 records (no silent loss).
	if got := w.sizeForTesting(); got != 3 {
		t.Fatalf("post-error buffer size = %d, want 3 (records must be retained for retry)", got)
	}

	// Remove the error so the retry succeeds.
	store.mu.Lock()
	store.err = nil
	store.mu.Unlock()

	w.flush()

	// Second flush drains everything.
	if got := w.sizeForTesting(); got != 0 {
		t.Errorf("post-retry buffer size = %d, want 0 (retry must drain retained records)", got)
	}
	if got := store.totalSteps(); got != 3 {
		t.Errorf("retry delivered %d steps, want 3", got)
	}
}

// TestTraceWriter_Flush_Success_ClearsFlushedOnly_PreservesLateArrivals
// verifies that records added concurrently while AppendSteps runs survive
// the flush. flush snapshots only the prefix it persists; the tail accepted
// during the store call must still be in the buffer afterwards.
func TestTraceWriter_Flush_Success_ClearsFlushedOnly_PreservesLateArrivals(t *testing.T) {
	slow := newSlowFakeAppender()
	w := newTestTraceWriter(slow, 100, time.Hour)

	// Head: 2 records.
	w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
	w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))

	flushDone := make(chan struct{})
	go func() {
		w.flush()
		close(flushDone)
	}()

	// Wait for AppendSteps to start, then Submit 1 more record before it
	// returns.
	<-slow.started
	w.Submit(makeRecord(observation.SourceHook, observation.PhaseCommitted))
	close(slow.release)
	<-flushDone

	// After flush: the 2 head records are persisted, the 1 late record
	// remains in the buffer.
	if got := w.sizeForTesting(); got != 1 {
		t.Fatalf("post-flush buffer size = %d, want 1 (late arrival must be preserved)", got)
	}
	if got := slow.totalSteps(); got != 2 {
		t.Errorf("persisted steps = %d, want 2 (only the snapshotted head)", got)
	}
}

// slowFakeAppender blocks the first AppendSteps call until release is
// closed, letting tests interleave a Submit during the store call.
type slowFakeAppender struct {
	mu          sync.Mutex
	batches     [][]store.TraceStep
	started     chan struct{}
	release     chan struct{}
	firstCalled bool
}

func newSlowFakeAppender() *slowFakeAppender {
	return &slowFakeAppender{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (f *slowFakeAppender) AppendSteps(steps []store.TraceStep) error {
	f.mu.Lock()
	firstCall := !f.firstCalled
	f.firstCalled = true
	f.mu.Unlock()

	if firstCall {
		close(f.started)
		<-f.release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	clone := make([]store.TraceStep, len(steps))
	copy(clone, steps)
	f.batches = append(f.batches, clone)
	return nil
}

func (f *slowFakeAppender) totalSteps() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.batches {
		n += len(b)
	}
	return n
}

// ----- C8 defensive: toStoreSteps skips records with empty SpanID ---------

// TestToStoreSteps_EmptySpanID_SkipsRecord_LogsMetric is a defensive contract
// check: apply.go is expected to back-fill every record's SpanID with a
// UUID, so an empty SpanID reaching toStoreSteps is a bug. Rather than
// fabricate a non-idempotent step_id (trace+seq+batch-index would vary
// across retries), we drop the record and bump a metric.
func TestToStoreSteps_EmptySpanID_SkipsRecord_LogsMetric(t *testing.T) {
	ResetForTesting()
	now := func() time.Time { return time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC) }
	records := []TraceRecord{
		{
			TraceID: "trace-1", SpanID: "", // empty — contract violation
			SessionID: "sess-1", SourceKind: observation.SourceHook,
			Action: "PostToolUse", Phase: observation.PhaseCommitted,
			Status: "success", Outcome: "skipped", Seq: 1,
			StartedAt: now(), EndedAt: now(),
		},
		{
			TraceID: "trace-2", SpanID: "valid-span",
			SessionID: "sess-2", SourceKind: observation.SourceHook,
			Action: "PostToolUse", Phase: observation.PhaseCommitted,
			Status: "success", Outcome: "skipped", Seq: 2,
			StartedAt: now(), EndedAt: now(),
		},
	}

	steps := toStoreSteps(records, now)
	if len(steps) != 1 {
		t.Fatalf("toStoreSteps out = %d, want 1 (empty SpanID must be skipped)", len(steps))
	}
	if steps[0].StepID != "valid-span" {
		t.Errorf("kept step StepID = %q, want valid-span", steps[0].StepID)
	}
	if got := Value("lights_trace_dropped", "reason=missing_span_id"); got != 1 {
		t.Errorf("missing_span_id counter = %d, want 1", got)
	}
}
