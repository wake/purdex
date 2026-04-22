package arbitrator

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/wake/purdex/internal/store"
)

// Defaults for TraceWriter tunables (plan D10.2).
const (
	defaultTraceWriterCap        = 4096
	defaultTraceWriterFlushEvery = 100 * time.Millisecond
)

// StepsAppender is the minimal store-side interface the TraceWriter needs.
// The real implementation is *store.TraceStore.AppendSteps; unit tests supply
// an in-memory fake.
type StepsAppender interface {
	AppendSteps(steps []store.TraceStep) error
}

// TraceWriterOptions configures a TraceWriter. Zero values fall back to the
// package defaults (Cap=4096, FlushEvery=100ms, Now=time.Now).
type TraceWriterOptions struct {
	Cap        int
	FlushEvery time.Duration
	Store      StepsAppender
	Now        func() time.Time
}

// TraceWriter is the Arbitrator's trace sink. Submit is safe to call from any
// goroutine; a single background goroutine (Start) drains the buffer onto the
// backing StepsAppender on a periodic tick.
//
// Admission uses a priority ring: when the buffer is full, Submit finds the
// slot with the strictly worse DropPriority than the incoming record and
// overwrites it; if no such slot exists, the incoming record is dropped.
// Either path increments the lights_trace_dropped counter with tag
// priority=<dp of the dropped record>.
type TraceWriter struct {
	mu         sync.Mutex
	buf        []TraceRecord
	cap        int
	flushEvery time.Duration
	store      StepsAppender
	now        func() time.Time

	startOnce sync.Once
	done      chan struct{} // closed when the run loop exits
	runCtx    context.Context
}

// NewTraceWriter constructs a TraceWriter with defaults filled in for any
// unset option.
func NewTraceWriter(opts TraceWriterOptions) *TraceWriter {
	if opts.Cap <= 0 {
		opts.Cap = defaultTraceWriterCap
	}
	if opts.FlushEvery <= 0 {
		opts.FlushEvery = defaultTraceWriterFlushEvery
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &TraceWriter{
		buf:        make([]TraceRecord, 0, opts.Cap),
		cap:        opts.Cap,
		flushEvery: opts.FlushEvery,
		store:      opts.Store,
		now:        opts.Now,
		done:       make(chan struct{}),
	}
}

// Submit offers r to the buffer. Never blocks. Drops on full per the priority
// ring rules above. Concurrent callers race only on the mutex; drop decisions
// are serialized and deterministic.
func (w *TraceWriter) Submit(r TraceRecord) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.buf) < w.cap {
		w.buf = append(w.buf, r)
		return
	}

	// Buffer full — scan for the worst (highest DropPriority) slot that is
	// strictly worse than the incoming record.
	worstIdx := -1
	worstPri := r.DropPriority
	for i := range w.buf {
		if w.buf[i].DropPriority > worstPri {
			worstPri = w.buf[i].DropPriority
			worstIdx = i
		}
	}
	if worstIdx >= 0 {
		// Evict the worst slot; incoming takes its place.
		evicted := w.buf[worstIdx]
		w.buf[worstIdx] = r
		Inc("lights_trace_dropped", "priority="+strconv.Itoa(evicted.DropPriority))
		return
	}

	// Nothing in the buffer is strictly lower priority than the incoming r —
	// drop r itself.
	Inc("lights_trace_dropped", "priority="+strconv.Itoa(r.DropPriority))
}

// Start launches the flush loop. Idempotent — calling Start twice is a no-op.
// The supplied ctx governs shutdown; see Shutdown.
func (w *TraceWriter) Start(ctx context.Context) {
	w.startOnce.Do(func() {
		w.runCtx = ctx
		go w.run(ctx)
	})
}

func (w *TraceWriter) run(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(w.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.flush()
		case <-ctx.Done():
			// Final flush attempt on graceful exit. Shutdown may supply a
			// separate ctx to decide whether to skip it.
			w.flush()
			return
		}
	}
}

// Shutdown waits for the run goroutine to exit. If the Start ctx is still
// live, Shutdown cancels nothing itself — callers must cancel the ctx passed
// to Start. If the supplied shutdownCtx expires first, Shutdown returns
// ctx.Err() without waiting further (best-effort; data may remain buffered).
func (w *TraceWriter) Shutdown(ctx context.Context) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			// Caller passed an already-cancelled ctx — do not block, do not
			// flush. Contract: "ctx cancelled → exit without flush".
			return nil
		}
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// flush snapshots the current buffer and attempts to persist it via the
// backing store. On success the flushed prefix is removed from the buffer,
// preserving any records that arrived concurrently while AppendSteps was
// running. On store error the buffer is left intact so the next tick can
// retry — dropping the batch would silently lose committed-phase traces.
func (w *TraceWriter) flush() {
	w.mu.Lock()
	if len(w.buf) == 0 {
		w.mu.Unlock()
		return
	}
	// Snapshot the current head of the buffer. Do NOT clear w.buf yet:
	// AppendSteps may fail, in which case the batch must remain available
	// for the next flush attempt.
	batch := make([]TraceRecord, len(w.buf))
	copy(batch, w.buf)
	flushed := len(batch)
	w.mu.Unlock()

	if w.store == nil {
		// No store — drop the snapshot but still clear the head so
		// producer admission can keep inserting.
		w.mu.Lock()
		if len(w.buf) >= flushed {
			w.buf = w.buf[flushed:]
		} else {
			w.buf = w.buf[:0]
		}
		w.mu.Unlock()
		return
	}
	steps := toStoreSteps(batch, w.now)
	if err := w.store.AppendSteps(steps); err != nil {
		// Retain the buffer for the next tick. Records that arrived during
		// AppendSteps are appended to the tail of w.buf and remain, so the
		// next flush snapshots them together with the retried head.
		log.Printf("[arbitrator][trace_writer] AppendSteps failed — retaining %d records for next flush: %v", flushed, err)
		return
	}
	// Success — remove only the records we actually persisted. Records
	// appended during AppendSteps sit at w.buf[flushed:] and must survive.
	w.mu.Lock()
	if len(w.buf) >= flushed {
		w.buf = w.buf[flushed:]
	} else {
		// Defensive: buf cannot shrink below `flushed` in normal operation
		// (no other flusher is concurrent), but guard against pathological
		// mutations just in case.
		w.buf = w.buf[:0]
	}
	w.mu.Unlock()
}

// sizeForTesting returns the current buffer length. Test-only; holds mu.
func (w *TraceWriter) sizeForTesting() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.buf)
}

// snapshotForTesting returns a copy of the buffer. Test-only.
func (w *TraceWriter) snapshotForTesting() []TraceRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]TraceRecord, len(w.buf))
	copy(out, w.buf)
	return out
}

// toStoreSteps converts a batch of TraceRecord envelopes into store-level
// TraceStep rows. Zero times are normalized to the supplied now() so every
// step satisfies validateLightsRow's NOT-NULL requirements.
//
// Records without a SpanID are skipped with a metric: the apply pipeline
// is expected to back-fill SpanID with a UUID for every emitted record,
// and a missing id here is a contract violation. Skipping (rather than
// synthesizing a batch-local id) avoids non-idempotent step_id values
// that would defeat the INSERT OR IGNORE retry path.
func toStoreSteps(records []TraceRecord, now func() time.Time) []store.TraceStep {
	out := make([]store.TraceStep, 0, len(records))
	for _, r := range records {
		if r.SpanID == "" {
			log.Printf("[arbitrator][trace_writer] skipping record with empty SpanID — apply pipeline must back-fill: trace_id=%q action=%q", r.TraceID, r.Action)
			Inc("lights_trace_dropped", "reason=missing_span_id")
			continue
		}
		started := r.StartedAt
		if started.IsZero() {
			started = now()
		}
		ended := r.EndedAt
		if ended.IsZero() {
			ended = started
		}

		chainID := r.TraceID
		stepID := r.SpanID

		step := store.TraceStep{
			StepID:             stepID,
			ChainID:            chainID,
			ParentStepID:       r.ParentSpanID,
			Seq:                int(r.Seq),
			Kind:               string(r.SourceKind),
			TmuxSession:        "",
			PaneID:             "",
			AgentType:          "",
			FrameID:            "",
			ParentFrameID:      "",
			EventName:          r.Action,
			Decision:           "",
			Reason:             r.ReasonText,
			CreatedAt:          started.UnixNano(),
			SourceKind:         string(r.SourceKind),
			Action:             r.Action,
			ReasonCode:         r.ReasonCode,
			Outcome:            r.Outcome,
			ScenarioKey:        "",
			ObservedGeneration: r.ObservedGeneration,
			Phase:              string(r.Phase),
			Status:             r.Status,
			TraceID:            r.TraceID,
			ReasonText:         r.ReasonText,
			StartedAt:          started.UnixNano(),
			EndedAt:            ended.UnixNano(),
			OTelKind:           "internal",
		}

		if len(r.DecisionPorts) > 0 {
			if raw, err := json.Marshal(r.DecisionPorts); err == nil {
				step.DecisionPorts = raw
			}
		}
		if len(r.Evidence) > 0 {
			if raw, err := json.Marshal(r.Evidence); err == nil {
				step.EvidenceRefs = raw
			}
		}

		out = append(out, step)
	}
	return out
}
