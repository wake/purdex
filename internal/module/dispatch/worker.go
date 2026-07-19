package dispatch

import (
	"context"
	"errors"
	"log"
	"time"
)

// defaultPollInterval is how often the consumer worker polls Ploom for pending
// dispatches when no interval is configured.
const defaultPollInterval = 5 * time.Second

// PloomClient is the subset of the Ploom API the consumer worker drives. The
// concrete *Client satisfies it; tests inject a fake.
type PloomClient interface {
	PollPending(ctx context.Context) ([]PendingDispatch, error)
	Claim(ctx context.Context, dispatchID string) (ClaimResult, error)
	Fetch(ctx context.Context, dispatchID string) (DispatchDetail, error)
}

// ClaimedDispatch bundles everything the worker learns for one dispatch it
// successfully claimed and fetched. It is the unit handed to the FetchSink.
type ClaimedDispatch struct {
	Pending PendingDispatch
	Claim   ClaimResult
	Detail  DispatchDetail
}

// FetchSink receives each dispatch the worker has claimed and fully fetched.
//
// P.3 stops at fetch: this is the injectable seam where P.7 wires the rest of
// the consume loop (create execution row → admission → launch → report). In P.3
// it is a no-op/logging sink so the poll→claim→fetch pipeline can be exercised
// end-to-end against a fake Ploom without pulling in execution/admission/launch.
type FetchSink func(ctx context.Context, claimed ClaimedDispatch)

// Worker polls Ploom for pending dispatches and, for each, claims then fetches
// the full detail, handing the result to its FetchSink.
type Worker struct {
	client   PloomClient
	sink     FetchSink
	interval time.Duration
	logf     func(format string, args ...any)
}

// WorkerOption customises a Worker.
type WorkerOption func(*Worker)

// WithSink sets the FetchSink invoked for each claimed+fetched dispatch.
func WithSink(sink FetchSink) WorkerOption {
	return func(w *Worker) { w.sink = sink }
}

// WithInterval sets the poll interval used by Run.
func WithInterval(d time.Duration) WorkerOption {
	return func(w *Worker) {
		if d > 0 {
			w.interval = d
		}
	}
}

// WithLogf overrides the log function (defaults to the standard logger).
func WithLogf(logf func(format string, args ...any)) WorkerOption {
	return func(w *Worker) {
		if logf != nil {
			w.logf = logf
		}
	}
}

// NewWorker builds a consumer worker over the given client.
func NewWorker(client PloomClient, opts ...WorkerOption) *Worker {
	w := &Worker{
		client:   client,
		interval: defaultPollInterval,
		logf:     log.Printf,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Run polls on a ticker until ctx is cancelled. It runs one cycle immediately,
// then every interval. Per-cycle errors are logged, never fatal.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.RunOnce(ctx); err != nil {
			w.logf("[dispatch] poll cycle: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce executes a single poll→claim→fetch cycle. It processes every pending
// dispatch independently; a per-dispatch failure does not abort the others. The
// returned error is the join of all per-dispatch failures worth surfacing
// (skips — already_claimed / dispatch_not_found — are not errors).
func (w *Worker) RunOnce(ctx context.Context) error {
	pendings, err := w.client.PollPending(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range pendings {
		if err := w.processPending(ctx, p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// processPending claims one dispatch and, on success, fetches its full detail
// and hands it to the sink. Claim races (already_claimed) and missing/foreign
// dispatches (dispatch_not_found) are benign skips returning nil.
func (w *Worker) processPending(ctx context.Context, p PendingDispatch) error {
	claim, err := w.client.Claim(ctx, p.DispatchID)
	if err != nil {
		switch {
		case errors.Is(err, ErrAlreadyClaimed):
			// Another daemon won the race — skip, do not retry.
			return nil
		case errors.Is(err, ErrDispatchNotFound):
			// Gone or not ours (fail-closed) — skip.
			return nil
		default:
			// ErrSchemaIncompatible / ErrUnauthorized / 5xx / transport — surface.
			return err
		}
	}

	detail, err := w.client.Fetch(ctx, p.DispatchID)
	if err != nil {
		return err
	}

	if w.sink != nil {
		w.sink(ctx, ClaimedDispatch{Pending: p, Claim: claim, Detail: detail})
	}
	return nil
}
