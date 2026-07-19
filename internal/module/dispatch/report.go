package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"
)

// This file drives the durable E4 report stream (spec §3.3):
//
//   - accepted-before-lifecycle ordering: accepted(seq=1) must be acked before
//     any running/completed/failed is sent; a 409 accepted_required makes the
//     sender (re)send accepted then retry.
//   - ack_seq cursor: a per-execution high-water mark of the seq Ploom has
//     projected; seq ≤ cursor is treated as already-projected (idempotent).
//   - retry/backoff: 5xx → exponential backoff (next_attempt_at); 401/403/schema
//     → permanent failure; stale_seq (200 + ack_seq) advances the cursor.
//   - durability cut: accepted's immutable metadata all live on the execution
//     row, so even if the in-flight accepted was lost before it hit the outbox,
//     it is reconstructed from the row on restart and replayed.

const (
	// reportSchemaVersion is the M0 wire schema_version stamped on every report.
	reportSchemaVersion = SchemaVersion
	// reportProvider is the M0 fixed provider (neutral seam; codex → M1).
	reportProvider = "claude"

	defaultReportBaseBackoff   = 1 * time.Second
	defaultReportMaxBackoff    = 5 * time.Minute
	defaultReportFlushInterval = 3 * time.Second
)

// AcceptedRow is the durable subset of an execution row needed to reconstruct
// the accepted(seq=1) report — the §3.3 durability cut. Every field is a
// persistent execution-row column, so accepted can always be rebuilt after a
// restart even if the in-flight accepted never reached the outbox.
type AcceptedRow struct {
	ExecutionID             string
	DispatchID              string
	RepoLocation            string // canonical local_dir (execution row stores the path)
	Provider                string
	AttemptNo               int
	EffectiveSandboxProfile string
	HeadAtStart             string
	DirtyAtStart            bool
	SessionCode             string // "" → JSON null
}

// ExecutionReader loads the durable facts for reconstructing an accepted report.
// The real execution store is adapted onto this in module wiring; tests inject a
// fake. Absence (found=false) means the row is gone and accepted cannot be
// rebuilt.
type ExecutionReader interface {
	LoadAcceptedRow(executionID string) (row AcceptedRow, found bool, err error)
}

// ReportPoster is the subset of *Client the sender drives (POST E4). Tests inject
// a fake Ploom.
type ReportPoster interface {
	Report(ctx context.Context, dispatchID string, payload []byte) (ReportResult, error)
}

// Artifact is one pointer-first artifact reference on a terminal report
// (m0-contract §5): meta carries only a summary, never an inlined blob.
type Artifact struct {
	Kind    string         `json:"kind"`
	Pointer string         `json:"pointer"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// ReportError is the error object on a failed report (m0-contract §2).
type ReportError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// BuildAcceptedPayload marshals the accepted(seq=1) E4 request from durable row
// facts. This is the single source for accepted so a reconstructed accepted is
// byte-identical to one enqueued at admission time (m0-contract §3, E4 accepted).
func BuildAcceptedPayload(row AcceptedRow) ([]byte, error) {
	provider := row.Provider
	if provider == "" {
		provider = reportProvider
	}
	attempt := row.AttemptNo
	if attempt == 0 {
		attempt = 1
	}
	var sessionCode *string
	if row.SessionCode != "" {
		sc := row.SessionCode
		sessionCode = &sc
	}
	payload := map[string]any{
		"schema_version":            reportSchemaVersion,
		"dispatch_id":               row.DispatchID,
		"execution_id":              row.ExecutionID,
		"attempt_no":                attempt,
		"provider":                  provider,
		"status":                    "accepted",
		"seq":                       1,
		"repo_location":             map[string]any{"local_dir": row.RepoLocation},
		"effective_sandbox_profile": row.EffectiveSandboxProfile,
		"head_at_start":             row.HeadAtStart,
		"dirty_at_start":            row.DirtyAtStart,
		"session_code":              sessionCode, // nil → JSON null
	}
	return json.Marshal(payload)
}

// BuildRunningPayload marshals a running lifecycle report.
func BuildRunningPayload(executionID string, seq int) ([]byte, error) {
	return json.Marshal(map[string]any{
		"schema_version": reportSchemaVersion,
		"execution_id":   executionID,
		"status":         "running",
		"seq":            seq,
	})
}

// BuildTerminalPayload marshals a completed/failed lifecycle report. artifacts is
// optional (may be nil). errObj is required for failed and ignored otherwise.
func BuildTerminalPayload(executionID string, seq int, status string, artifacts []Artifact, errObj *ReportError) ([]byte, error) {
	payload := map[string]any{
		"schema_version": reportSchemaVersion,
		"execution_id":   executionID,
		"status":         status,
		"seq":            seq,
	}
	if len(artifacts) > 0 {
		payload["artifacts"] = artifacts
	}
	if status == "failed" && errObj != nil {
		payload["error"] = errObj
	}
	return json.Marshal(payload)
}

// Sender drains the durable outbox to Ploom, enforcing accepted-before-lifecycle
// ordering, the ack cursor, retry/backoff, and durability-cut reconstruction.
type Sender struct {
	outbox   *Outbox
	client   ReportPoster
	reader   ExecutionReader // may be nil: reconstruction unavailable, queued rows still replay
	now      func() int64
	backoff  func(attempts int) time.Duration
	logf     func(format string, args ...any)
	interval time.Duration
}

// SenderOption customises a Sender.
type SenderOption func(*Sender)

// WithExecutionReader sets the durability-cut source for reconstructing accepted.
func WithExecutionReader(r ExecutionReader) SenderOption {
	return func(s *Sender) { s.reader = r }
}

// WithNow overrides the clock (tests).
func WithNow(now func() int64) SenderOption {
	return func(s *Sender) {
		if now != nil {
			s.now = now
		}
	}
}

// WithBackoff overrides the backoff schedule (tests).
func WithBackoff(fn func(attempts int) time.Duration) SenderOption {
	return func(s *Sender) {
		if fn != nil {
			s.backoff = fn
		}
	}
}

// WithSenderLogf overrides the log function.
func WithSenderLogf(logf func(format string, args ...any)) SenderOption {
	return func(s *Sender) {
		if logf != nil {
			s.logf = logf
		}
	}
}

// WithFlushInterval sets how often Run flushes the outbox.
func WithFlushInterval(d time.Duration) SenderOption {
	return func(s *Sender) {
		if d > 0 {
			s.interval = d
		}
	}
}

// NewSender builds a report sender over the given outbox and Ploom client.
func NewSender(outbox *Outbox, client ReportPoster, opts ...SenderOption) *Sender {
	s := &Sender{
		outbox:   outbox,
		client:   client,
		now:      func() int64 { return time.Now().Unix() },
		backoff:  defaultReportBackoff,
		logf:     log.Printf,
		interval: defaultReportFlushInterval,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// defaultReportBackoff is exponential from a 1s base, capped at 5m.
func defaultReportBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := defaultReportBaseBackoff
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= defaultReportMaxBackoff {
			return defaultReportMaxBackoff
		}
	}
	return d
}

// Enqueue durably queues one already-built report payload for later send. P.6/P.7
// call this at admission (accepted) and lifecycle transitions; it is idempotent
// on (execution_id, seq).
func (s *Sender) Enqueue(executionID, dispatchID string, seq int, status string, payload []byte) error {
	_, err := s.outbox.Enqueue(OutboxRecord{
		ExecutionID: executionID,
		DispatchID:  dispatchID,
		Seq:         seq,
		Status:      status,
		Payload:     payload,
	})
	return err
}

// Run flushes the outbox immediately (startup replay) then every interval until
// ctx is cancelled.
func (s *Sender) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		if err := s.Flush(ctx); err != nil {
			s.logf("[dispatch] report flush: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Flush attempts one drain pass over every execution with due reports. A
// per-execution failure does not abort the others.
func (s *Sender) Flush(ctx context.Context) error {
	execIDs, err := s.outbox.DueExecutions(s.now())
	if err != nil {
		return err
	}
	var errs []error
	for _, execID := range execIDs {
		if err := s.flushExecution(ctx, execID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// flushExecution drains one execution's ordered report stream. It first ensures
// accepted(seq=1) is acked (ordering gate), then sends lifecycle reports in seq
// order, treating seq ≤ ack cursor as already projected.
func (s *Sender) flushExecution(ctx context.Context, execID string) error {
	ackSeq, err := s.outbox.Cursor(execID)
	if err != nil {
		return err
	}

	// Ordering gate: accepted must be acked before any lifecycle report.
	if ackSeq < 1 {
		if err := s.ensureAccepted(ctx, execID, false); err != nil {
			return err
		}
		if ackSeq, err = s.outbox.Cursor(execID); err != nil {
			return err
		}
		if ackSeq < 1 {
			// accepted still unacked (backoff / send failure) — hold lifecycle.
			return nil
		}
	}

	recs, err := s.outbox.UnackedRecords(execID)
	if err != nil {
		return err
	}
	now := s.now()
	for _, rec := range recs {
		if rec.Status == "accepted" {
			continue // handled by the ordering gate above
		}
		if rec.Seq <= ackSeq {
			// Already projected (stale) — mark acked, never re-send (idempotent).
			if err := s.outbox.MarkAcked(rec.ID); err != nil {
				return err
			}
			continue
		}
		if rec.NextAttemptAt > now {
			continue // in backoff
		}
		newAck, outcome := s.sendOne(ctx, rec)
		switch outcome {
		case sendOK:
			if newAck > ackSeq {
				ackSeq = newAck
			}
		case sendAcceptedRequired:
			// Ploom lost/never-acked accepted: force-resend it, then retry this
			// record on the next flush cycle.
			if err := s.ensureAccepted(ctx, execID, true); err != nil {
				return err
			}
			return nil
		case sendDefer:
			// backoff or permanent failure — stop this execution's ordered stream.
			return nil
		}
	}
	return nil
}

// ensureAccepted guarantees the accepted(seq=1) report exists and is sent. When
// missing from the outbox it is reconstructed from the execution row (durability
// cut). force re-sends even a locally-acked accepted (used on 409
// accepted_required, where Ploom says it never projected accepted).
func (s *Sender) ensureAccepted(ctx context.Context, execID string, force bool) error {
	rec, ok, err := s.outbox.RecordBySeq(execID, 1)
	if err != nil {
		return err
	}
	if !ok {
		rec, err = s.reconstructAccepted(execID)
		if err != nil {
			return err
		}
		if rec.ID == 0 {
			// no queued row and no reconstruction source — cannot proceed.
			return nil
		}
	}
	if rec.Permanent {
		return nil
	}
	if rec.Acked && !force {
		return nil
	}
	if !force && rec.NextAttemptAt > s.now() {
		return nil // in backoff
	}
	s.sendOne(ctx, rec)
	return nil
}

// reconstructAccepted rebuilds the accepted report from the durable execution
// row and enqueues it, then returns the queued record. If no reader is wired or
// the row is gone it returns a zero-ID record (nothing to send).
func (s *Sender) reconstructAccepted(execID string) (OutboxRecord, error) {
	if s.reader == nil {
		return OutboxRecord{}, nil
	}
	row, found, err := s.reader.LoadAcceptedRow(execID)
	if err != nil {
		return OutboxRecord{}, err
	}
	if !found {
		s.logf("[dispatch] cannot reconstruct accepted: execution row %s gone", execID)
		return OutboxRecord{}, nil
	}
	payload, err := BuildAcceptedPayload(row)
	if err != nil {
		return OutboxRecord{}, err
	}
	if _, err := s.outbox.Enqueue(OutboxRecord{
		ExecutionID: row.ExecutionID,
		DispatchID:  row.DispatchID,
		Seq:         1,
		Status:      "accepted",
		Payload:     payload,
	}); err != nil {
		return OutboxRecord{}, err
	}
	rec, _, err := s.outbox.RecordBySeq(execID, 1)
	return rec, err
}

// sendOutcome classifies one report POST result for the flush driver.
type sendOutcome int

const (
	sendOK               sendOutcome = iota // acked/projected; cursor advanced
	sendAcceptedRequired                    // 409: caller must (re)send accepted
	sendDefer                               // backoff or permanent — stop for now
)

// sendOne POSTs a single report and records the outcome durably. On success it
// advances the ack cursor and marks the record acked when seq ≤ ack_seq. A 409
// accepted_required is surfaced to the caller; 5xx/transport → backoff attempt;
// 401/403/schema → permanent.
func (s *Sender) sendOne(ctx context.Context, rec OutboxRecord) (ackSeq int, outcome sendOutcome) {
	res, err := s.client.Report(ctx, rec.DispatchID, rec.Payload)
	if err == nil {
		if aerr := s.outbox.AdvanceCursor(rec.ExecutionID, res.AckSeq); aerr != nil {
			s.logf("[dispatch] advance cursor execution=%s: %v", rec.ExecutionID, aerr)
		}
		if rec.Seq <= res.AckSeq {
			if merr := s.outbox.MarkAcked(rec.ID); merr != nil {
				s.logf("[dispatch] mark acked id=%d: %v", rec.ID, merr)
			}
		}
		return res.AckSeq, sendOK
	}

	switch {
	case errors.Is(err, ErrAcceptedRequired):
		return 0, sendAcceptedRequired
	case errors.Is(err, ErrUnauthorized), errors.Is(err, ErrSchemaIncompatible):
		if merr := s.outbox.MarkPermanent(rec.ID, err.Error()); merr != nil {
			s.logf("[dispatch] mark permanent id=%d: %v", rec.ID, merr)
		}
		s.logf("[dispatch] report permanent failure execution=%s seq=%d: %v", rec.ExecutionID, rec.Seq, err)
		return 0, sendDefer
	default:
		// 5xx / transport / unclassified → retry with backoff.
		attempts := rec.Attempts + 1
		next := s.now() + int64(s.backoff(attempts)/time.Second)
		if merr := s.outbox.MarkAttempt(rec.ID, next, err.Error()); merr != nil {
			s.logf("[dispatch] mark attempt id=%d: %v", rec.ID, merr)
		}
		return 0, sendDefer
	}
}

// compile-time assertion the concrete client satisfies the sender's poster.
var _ ReportPoster = (*Client)(nil)
