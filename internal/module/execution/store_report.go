package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// This file holds the composite, transactional store operations: every state
// transition that implies an E4 report commits the row change AND the queued
// report in ONE transaction (F1).
//
// Why it must be atomic: the execution row is the SOT for what still needs
// attention (ListLive drives the reconcile sweep) while the outbox is the only
// place a report payload survives. Split across two commits, a failure in between
// leaves an unrecoverable state — most sharply for terminal: the row goes
// terminal (so ListLive/reconcile never look at it again) while the terminal
// payload, which carries the error and artifacts, is gone. Ploom then waits on a
// report that nothing can ever produce.
//
// The rule this file enforces: if the report cannot be queued, the transition
// does not happen. The row stays live and the next reconcile pass retries it.

// ReportEnvelope is the durable content of one E4 report: the per-execution seq,
// the lifecycle status it carries, and the ready-to-POST payload. The wire format
// is owned by the dispatch module; execution only persists the bytes.
type ReportEnvelope struct {
	Seq     int
	Status  string
	Payload []byte
}

// ReportBuilder renders the report implied by a state transition, from the row as
// it looks AFTER the transition (so a report can never describe a state the row
// does not have).
//
// It runs INSIDE the transaction that commits the transition and must therefore
// be pure — marshalling only, never a database call (which would deadlock on the
// held write lock) and never a slow syscall (which would hold the lock open).
// Expensive inputs such as diff capture must be computed before the call and
// closed over. Returning an error aborts the whole transaction.
type ReportBuilder func(*Execution) (ReportEnvelope, error)

// UpsertByDispatchWithReport is UpsertByDispatch plus the accepted report: the
// row insert and its accepted(seq=1) enqueue commit together, so a row can never
// exist without a recoverable accepted (a row with no report would occupy its
// repo — single-live is status-based — while Ploom heard nothing). build is only
// invoked when a row was actually created; an already-admitted dispatch is
// returned unchanged and re-enqueues nothing.
func (s *ExecutionStore) UpsertByDispatchWithReport(req NewExecution, build ReportBuilder) (*Execution, bool, error) {
	var (
		exec    *Execution
		created bool
	)
	err := s.inTx(func(ctx context.Context, conn *sql.Conn) error {
		var err error
		exec, created, err = upsertTx(ctx, conn, s.now(), req)
		if err != nil {
			return err
		}
		if !created {
			return nil
		}
		return emitReport(ctx, conn, s.now(), exec, build)
	})
	if err != nil {
		return nil, false, err
	}
	return exec, created, nil
}

// UpsertByDispatch creates a new execution for req.DispatchID, or returns the
// existing execution unchanged if the dispatch was already admitted. created is
// true only when a new row was inserted. The whole operation runs in a single
// BEGIN IMMEDIATE transaction so two concurrent claims of the same dispatch_id
// cannot both insert (contract §8; the UNIQUE dispatch_id index is the backstop).
// Production always goes through UpsertByDispatchWithReport — this report-less
// form exists for callers with nothing to report (tests, seeding).
func (s *ExecutionStore) UpsertByDispatch(req NewExecution) (*Execution, bool, error) {
	return s.UpsertByDispatchWithReport(req, nil)
}

// MarkLaunchedWithReport commits the successful-launch transition (spec §4.3)
// together with its running(seq=2) report: launch_state launching→launched, the
// derived session_code, and status accepted→running, all fenced on the prior
// state so a retry can never double-launch or revive a finished execution. It
// returns the committed row.
//
// Atomicity matters both ways here: if the running report cannot be queued the
// row stays accepted+launching, which is exactly the state the reconcile sweep
// knows how to finish — whereas a committed running with no report would look
// healthy forever while Ploom stayed at accepted.
func (s *ExecutionStore) MarkLaunchedWithReport(execID, sessionCode string, build ReportBuilder) (*Execution, error) {
	var exec *Execution
	err := s.inTx(func(ctx context.Context, conn *sql.Conn) error {
		var exists int
		err := conn.QueryRowContext(ctx,
			`SELECT 1 FROM executions WHERE execution_id = ?`, execID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		var code any
		if sessionCode != "" {
			code = sessionCode
		}
		res, err := conn.ExecContext(ctx, `
			UPDATE executions
			   SET launch_state = ?, status = ?, session_code = ?, updated_at = ?
			 WHERE execution_id = ? AND launch_state = ? AND status = ?`,
			string(LaunchLaunched), string(StatusRunning), code, s.now(),
			execID, string(LaunchLaunching), string(StatusAccepted),
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: mark launched requires launching+accepted", ErrIllegalTransition)
		}
		if exec, err = loadByID(ctx, conn, execID); err != nil {
			return err
		}
		return emitReport(ctx, conn, s.now(), exec, build)
	})
	if err != nil {
		return nil, err
	}
	return exec, nil
}

// MarkLaunched is the report-less form of MarkLaunchedWithReport (tests/seeding).
func (s *ExecutionStore) MarkLaunched(execID, sessionCode string) error {
	_, err := s.MarkLaunchedWithReport(execID, sessionCode, nil)
	return err
}

// MarkTerminalWithReport commits a terminal outcome (spec §5.3) together with its
// terminal report: status → completed/failed plus outcome_source, and the
// completed/failed report, in one transaction. The move is guarded by
// CanTransition, so a terminal row (no outgoing edge) yields ErrIllegalTransition
// — a double-fired terminal seam can neither re-report nor flip an already-decided
// outcome. to must be terminal.
//
// This is the pairing the whole file exists for: a terminal row is invisible to
// ListLive, so if its report were lost there would be no actor left to rebuild it
// (the payload carries the error object and artifact pointers, which no other
// durable record holds). Failing the enqueue therefore keeps the row live for the
// next reconcile pass.
func (s *ExecutionStore) MarkTerminalWithReport(execID string, to Status, source OutcomeSource, build ReportBuilder) error {
	if !to.IsTerminal() {
		return fmt.Errorf("%w: MarkTerminal target %q is not terminal", ErrIllegalTransition, to)
	}
	return s.inTx(func(ctx context.Context, conn *sql.Conn) error {
		if err := transitionTx(ctx, conn, s.now(), execID, to, &source); err != nil {
			return err
		}
		exec, err := loadByID(ctx, conn, execID)
		if err != nil {
			return err
		}
		return emitReport(ctx, conn, s.now(), exec, build)
	})
}

// MarkTerminal is the report-less form of MarkTerminalWithReport (tests/seeding).
func (s *ExecutionStore) MarkTerminal(execID string, to Status, source OutcomeSource) error {
	return s.MarkTerminalWithReport(execID, to, source, nil)
}

// UpdateStatusWithReport advances an execution to a new status and queues the
// report that transition implies, in one transaction. The pre-relay launch
// failure path uses it: status → failed plus the terminal failed report. Same
// invariant — a failed row is not live, so losing its report would wedge Ploom at
// accepted with nothing left to retry.
func (s *ExecutionStore) UpdateStatusWithReport(execID string, to Status, build ReportBuilder) error {
	return s.inTx(func(ctx context.Context, conn *sql.Conn) error {
		if err := transitionTx(ctx, conn, s.now(), execID, to, nil); err != nil {
			return err
		}
		exec, err := loadByID(ctx, conn, execID)
		if err != nil {
			return err
		}
		return emitReport(ctx, conn, s.now(), exec, build)
	})
}

// UpdateStatus advances an execution to a new status, enforcing the state machine
// (spec §5.1). Returns ErrNotFound if the id is unknown, or ErrIllegalTransition
// if the move is not a legal edge (e.g. terminal→running). Report-less form of
// UpdateStatusWithReport.
func (s *ExecutionStore) UpdateStatus(execID string, to Status) error {
	return s.UpdateStatusWithReport(execID, to, nil)
}

// inTx runs fn inside a single BEGIN IMMEDIATE transaction on a dedicated
// connection, committing on success and rolling back on any error (including a
// panic). Every composite operation in this file is built on it, which is what
// makes "row change + report" all-or-nothing.
func (s *ExecutionStore) inTx(fn func(ctx context.Context, conn *sql.Conn) error) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	if err := fn(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// emitReport renders and durably queues the report implied by a just-committed
// (but not yet visible) transition, inside the caller's transaction. A nil
// builder means "this caller has nothing to report" and is a no-op. Any error —
// from the builder or from the insert — propagates and aborts the transaction,
// taking the transition with it.
func emitReport(ctx context.Context, conn *sql.Conn, now int64, exec *Execution, build ReportBuilder) error {
	if build == nil {
		return nil
	}
	env, err := build(exec)
	if err != nil {
		return fmt.Errorf("build report for execution %s: %w", exec.ExecutionID, err)
	}
	if _, err := enqueueReport(ctx, conn, now, OutboxRecord{
		ExecutionID: exec.ExecutionID,
		DispatchID:  exec.DispatchID,
		Seq:         env.Seq,
		Status:      env.Status,
		Payload:     env.Payload,
	}); err != nil {
		return fmt.Errorf("enqueue report for execution %s: %w", exec.ExecutionID, err)
	}
	return nil
}

// transitionTx applies one fenced status change inside the caller's transaction.
// source, when non-nil, also records outcome_source (terminal moves). The legality
// check is CanTransition, so terminal rows have no outgoing edge.
func transitionTx(ctx context.Context, conn *sql.Conn, now int64, execID string, to Status, source *OutcomeSource) error {
	var cur string
	err := conn.QueryRowContext(ctx,
		`SELECT status FROM executions WHERE execution_id = ?`, execID).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !CanTransition(Status(cur), to) {
		return fmt.Errorf("%w: %s → %s", ErrIllegalTransition, cur, to)
	}
	if source != nil {
		_, err = conn.ExecContext(ctx,
			`UPDATE executions SET status = ?, outcome_source = ?, updated_at = ? WHERE execution_id = ?`,
			string(to), string(*source), now, execID)
		return err
	}
	_, err = conn.ExecContext(ctx,
		`UPDATE executions SET status = ?, updated_at = ? WHERE execution_id = ?`,
		string(to), now, execID)
	return err
}

// upsertTx inserts the execution row for req.DispatchID inside the caller's
// transaction, or loads the existing one when the dispatch was already admitted.
func upsertTx(ctx context.Context, conn *sql.Conn, now int64, req NewExecution) (*Execution, bool, error) {
	// Existing dispatch → return its execution unchanged (idempotent, no rebuild).
	existingID, err := scanExecutionID(conn.QueryRowContext(ctx,
		`SELECT execution_id FROM executions WHERE dispatch_id = ?`, req.DispatchID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if err == nil {
		exec, err := loadByID(ctx, conn, existingID)
		if err != nil {
			return nil, false, err
		}
		return exec, false, nil
	}

	// New dispatch → insert. The execution_id and launch_state may be supplied by
	// the caller (launch durable cut, so SessionName can be derived from the same
	// id before insert); otherwise they default.
	provider := req.Provider
	if provider == "" {
		provider = "claude"
	}
	execID := req.ExecutionID
	if execID == "" {
		execID = newExecutionID()
	}
	launchState := req.LaunchState
	if launchState == "" {
		launchState = LaunchNone
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO executions (
			execution_id, dispatch_id, repo_location, repo_location_json, provider, launch_state,
			session_name, session_code, attempt_no, status, seq_reported,
			head_at_start, dirty_at_start, sandbox_profile, outcome_source,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
		execID, req.DispatchID, req.RepoLocation, req.RepoLocationJSON, provider, string(launchState),
		req.SessionName, 1, string(StatusAccepted), 0,
		req.HeadAtStart, boolToInt(req.DirtyAtStart), req.SandboxProfile,
		now, now,
	); err != nil {
		return nil, false, err
	}
	exec, err := loadByID(ctx, conn, execID)
	if err != nil {
		return nil, false, err
	}
	return exec, true, nil
}
