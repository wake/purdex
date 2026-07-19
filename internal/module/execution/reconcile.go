package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
)

// This file implements the M0 startup reconcile sweep + manual reclaim (spec §5.4
// / plan P.9). On daemon start every live execution (status ∈ {accepted, running})
// is reconciled once against tmux reality: a session that is still alive keeps
// running (the global terminal seam will classify its eventual exit); a session
// that is gone means the execution ended without its terminal report reaching
// Ploom, so it is judged terminal and the report is enqueued for outbound replay.
// A launch that crashed half-way (launch_state=launching) is failed, and any
// orphan tmux session left behind is collected by name so it cannot keep mutating
// the repo. Automatic lease/heartbeat/timeout is deliberately out of scope (M1);
// M0 provides exactly "startup reconcile once" + "manual reclaim".

// SessionControl probes and collects tmux sessions by the pre-generated
// crash-recovery session_name (spec §4.3). HasSession is the liveness probe;
// KillSession collects an orphan by name. Satisfied by tmux.Executor; tests
// inject a fake.
type SessionControl interface {
	HasSession(name string) bool
	KillSession(name string) error
}

// reconcileStore is the runtime-store slice the reconciler drives: enumerate the
// live executions and commit terminal outcomes (fenced). MarkTerminal handles
// both accepted→failed (launching crash) and running→failed (launched, session
// gone) since both are legal edges.
type reconcileStore interface {
	ListLive() ([]*Execution, error)
	MarkTerminal(execID string, to Status, source OutcomeSource) error
}

// Reconciler performs the startup reconcile sweep and manual reclaim. It never
// relaunches — recovery only classifies (the launch fence lives in the durable
// cut, P.6) — and it is idempotent: a second pass over rows it already drove
// terminal is a no-op (ListLive excludes terminal rows; a lost mark race skips
// the report).
type Reconciler struct {
	store    reconcileStore
	sessions SessionControl
	reporter TerminalReporter
	diff     diffFunc
	daemonID string
	logf     func(format string, args ...any)
}

// NewReconciler builds a reconciler. store is *ExecutionStore; sessions is
// core.Tmux (tmux.Executor); reporter is the dispatch-side terminal outbox
// adapter; diff is BuildDiffArtifact (nil → transcript-only artifacts); daemonID
// scopes the artifact pointers.
func NewReconciler(store reconcileStore, sessions SessionControl, reporter TerminalReporter, diff diffFunc, daemonID string) *Reconciler {
	return &Reconciler{
		store:    store,
		sessions: sessions,
		reporter: reporter,
		diff:     diff,
		daemonID: daemonID,
		logf:     log.Printf,
	}
}

// Reconcile sweeps every live execution once (spec §5.4). Per-execution errors
// are joined and returned but never abort the sweep — one wedged row must not
// stop the rest from converging.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	_, err := r.sweep(ctx)
	return err
}

// sweep reconciles all live executions and returns how many were swept.
func (r *Reconciler) sweep(ctx context.Context) (int, error) {
	live, err := r.store.ListLive()
	if err != nil {
		return 0, fmt.Errorf("reconcile: list live: %w", err)
	}
	var errs []error
	for _, exec := range live {
		if err := r.reconcileOne(ctx, exec); err != nil {
			errs = append(errs, err)
		}
	}
	return len(live), errors.Join(errs...)
}

// ReclaimByID reconciles a single stuck execution by id — the manual-reclaim
// entry (spec §5.4). It filters the live set so only a live (accepted/running)
// row is acted on; an unknown or already-terminal id is a no-op returning
// found=false.
func (r *Reconciler) ReclaimByID(ctx context.Context, execID string) (found bool, err error) {
	live, err := r.store.ListLive()
	if err != nil {
		return false, fmt.Errorf("reclaim: list live: %w", err)
	}
	for _, exec := range live {
		if exec.ExecutionID == execID {
			return true, r.reconcileOne(ctx, exec)
		}
	}
	return false, nil
}

// reconcileOne classifies one live execution against tmux reality.
func (r *Reconciler) reconcileOne(ctx context.Context, exec *Execution) error {
	if exec.Status.IsTerminal() {
		return nil // defensive: ListLive never returns terminal rows
	}
	alive := r.sessions.HasSession(exec.SessionName)

	if exec.LaunchState == LaunchLaunched {
		if alive {
			// Session still running. status is already running; the global terminal
			// seam (keyed by the persisted session_code) will classify its eventual
			// process exit — nothing to re-mount per execution. Idempotent no-op.
			r.logf("[execution] reconcile: %s still running (session %s alive)", exec.ExecutionID, exec.SessionName)
			return nil
		}
		// Launched but the session is gone → the process exited without a terminal
		// report reaching Ploom. M0 keeps no post-crash exit record, so classify
		// failed (exit_only) and let outbound replay (§3.3) deliver it.
		return r.finishFailed(ctx, exec, "execution_error",
			"execution session ended without a terminal report (recovered on daemon restart)")
	}

	// launch_state launching/none: the launch never confirmed launched. Judge
	// failed. If the session_name's tmux session somehow exists (NewSession
	// succeeded but MarkLaunched never ran — extreme interleaving) collect the
	// orphan by name so it cannot keep mutating the repo (spec §5.4 / R2 #2).
	if alive {
		if err := r.sessions.KillSession(exec.SessionName); err != nil {
			r.logf("[execution] reconcile: kill orphan session %s (execution %s): %v", exec.SessionName, exec.ExecutionID, err)
		}
	}
	return r.finishFailed(ctx, exec, "launch_failed",
		"launch did not complete before daemon restart")
}

// finishFailed commits failed(exit_only) and enqueues the failed terminal report
// with pointer-first artifacts. It is idempotent: losing the terminal-transition
// race (the row is already terminal) skips the enqueue so no duplicate report is
// emitted — mirroring TerminalProcessor.Handle.
func (r *Reconciler) finishFailed(ctx context.Context, exec *Execution, errCode, errMsg string) error {
	if err := r.store.MarkTerminal(exec.ExecutionID, StatusFailed, OutcomeExitOnly); err != nil {
		if errors.Is(err, ErrIllegalTransition) {
			return nil // already terminal — idempotent no-op
		}
		return fmt.Errorf("reconcile: mark failed %s: %w", exec.ExecutionID, err)
	}
	// Reflect the committed status on the row copy handed to the reporter.
	exec.Status = StatusFailed
	exec.OutcomeSource = sql.NullString{String: string(OutcomeExitOnly), Valid: true}

	arts := buildTerminalArtifacts(ctx, r.diff, r.logf, r.daemonID, exec, StatusFailed)
	return r.reporter.EnqueueTerminal(exec, StatusFailed, arts, errCode, errMsg)
}

// Compile-time assertions the production types satisfy the reconcile ports.
var _ reconcileStore = (*ExecutionStore)(nil)
