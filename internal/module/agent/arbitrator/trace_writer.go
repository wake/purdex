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

// flush drains the current buffer onto the store in one batch. Store errors
// are logged; the buffer is drained either way (retrying would pile up more
// drops on the next tick).
func (w *TraceWriter) flush() {
	w.mu.Lock()
	if len(w.buf) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.buf
	w.buf = make([]TraceRecord, 0, w.cap)
	w.mu.Unlock()

	if w.store == nil {
		return
	}
	steps := toStoreSteps(batch, w.now)
	if err := w.store.AppendSteps(steps); err != nil {
		log.Printf("[arbitrator][trace_writer] AppendSteps failed: %v", err)
	}
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
func toStoreSteps(records []TraceRecord, now func() time.Time) []store.TraceStep {
	out := make([]store.TraceStep, 0, len(records))
	for i, r := range records {
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
		if stepID == "" {
			// SpanID is an optional PR-1b-1b envelope field; fall back to a
			// deterministic synthetic id derived from trace+seq+index so
			// AppendSteps retries collapse via INSERT OR IGNORE.
			stepID = chainID + "/" + strconv.FormatInt(r.Seq, 10) + "/" + strconv.Itoa(i)
		}

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
