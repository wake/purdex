package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
)

// This file implements the M0 terminal outcome path (spec §5.3 / §6 / plan P.8):
// the pure outcome-classification rule (timepoint = process exit, success/failure
// = the `result` protocol event when present), plus the TerminalProcessor that
// consumes a process-exit signal, classifies it, captures the diff artifact, and
// durably enqueues the completed/failed report through the injected reporter.

// ResultOutcome carries what the stream layer captured from the `result` protocol
// event for one execution (spec §5.3). HasResult is false when no `result` line
// was ever seen before the subprocess exited — the M0 degraded path.
type ResultOutcome struct {
	// HasResult is true when a `result` stream-json line was captured.
	HasResult bool
	// IsError mirrors result.is_error: true for a refusal / tool failure / agent
	// error even though the process may still exit 0.
	IsError bool
	// Subtype mirrors result.subtype (success / error_max_turns /
	// error_during_execution / …); carried for the failed report's detail.
	Subtype string
}

// ClassifyOutcome decides the terminal status and its audit source from the
// process exit code and the captured `result` (spec §5.3). The caller only
// invokes this at the process-exit timepoint, so the timing is already settled
// here — this is purely the success/failure split:
//
//   - exit != 0 or signaled → failed (exit code is authoritative)
//   - exit 0 + result.is_error → failed
//   - exit 0 + result ok → completed
//   - exit 0 + no result → completed (degraded; outcome_source=exit_only)
//
// outcome_source records which signal we actually had: OutcomeResult when a
// `result` event was captured, OutcomeExitOnly for the degraded exit-only path.
func ClassifyOutcome(exitCode int, signaled bool, res ResultOutcome) (Status, OutcomeSource) {
	source := OutcomeExitOnly
	if res.HasResult {
		source = OutcomeResult
	}
	if signaled || exitCode != 0 {
		return StatusFailed, source
	}
	// exit 0: the result event, when present, is the authority.
	if res.HasResult && res.IsError {
		return StatusFailed, OutcomeResult
	}
	return StatusCompleted, source
}

// terminalStore is the runtime-store slice the processor writes through:
// resolve the execution by its deeplink session_code, then commit the terminal
// outcome atomically (fenced).
type terminalStore interface {
	GetBySessionCode(sessionCode string) (*Execution, bool, error)
	MarkTerminal(execID string, to Status, source OutcomeSource) error
}

// TerminalReporter durably enqueues the completed/failed report (with its
// pointer-first artifacts) for one execution. It is implemented in the dispatch
// module, which owns the report outbox; execution must not import dispatch (that
// would cycle), so it is injected. errCode/errMsg are populated only for failed.
type TerminalReporter interface {
	EnqueueTerminal(exec *Execution, status Status, artifacts []Artifact, errCode, errMsg string) error
}

// diffFunc captures the diff artifact for one execution; injected so the
// processor is unit-testable without shelling out to git. Production wires
// BuildDiffArtifact.
type diffFunc func(ctx context.Context, daemonID, executionID, repoPath, headAtStart string) (Artifact, error)

// TerminalProcessor consumes the authoritative process-exit signal for one
// execution (surfaced by the stream terminal seam), classifies the outcome,
// captures the diff/transcript artifacts, commits the terminal status +
// outcome_source, and durably enqueues the terminal report. Its input is
// session_code (the stream bridge's relay key), which it resolves to the
// execution row.
type TerminalProcessor struct {
	store    terminalStore
	reporter TerminalReporter
	diff     diffFunc
	daemonID string
	logf     func(format string, args ...any)
}

// NewTerminalProcessor builds a processor. store is *ExecutionStore; reporter is
// the dispatch-side terminal outbox adapter; diff is BuildDiffArtifact; daemonID
// scopes the artifact pointers (the daemon's host id).
func NewTerminalProcessor(store terminalStore, reporter TerminalReporter, diff diffFunc, daemonID string) *TerminalProcessor {
	return &TerminalProcessor{
		store:    store,
		reporter: reporter,
		diff:     diff,
		daemonID: daemonID,
		logf:     log.Printf,
	}
}

// Handle processes one process-exit signal. It is idempotent: an unknown
// session_code, an already-terminal row, or a lost terminal-transition race all
// return nil without enqueuing a report. The timepoint is the caller's (this is
// only invoked on process exit), so premature-terminal is structurally impossible
// here — a captured result alone never reaches this method.
func (p *TerminalProcessor) Handle(ctx context.Context, sessionCode string, exitCode int, signaled bool, res ResultOutcome) error {
	exec, ok, err := p.store.GetBySessionCode(sessionCode)
	if err != nil {
		return err
	}
	if !ok {
		// A relay we never launched (or whose row is gone) — nothing to report.
		p.logf("[execution] terminal signal for unknown session_code %q — dropping", sessionCode)
		return nil
	}
	if exec.Status.IsTerminal() {
		return nil // already decided — idempotent no-op
	}

	status, source := ClassifyOutcome(exitCode, signaled, res)
	artifacts := p.buildArtifacts(ctx, exec, status)

	// Commit the terminal outcome first (fenced). If we lose the transition race
	// (a concurrent reconcile/terminal already finished it) we must not enqueue a
	// duplicate report.
	if err := p.store.MarkTerminal(exec.ExecutionID, status, source); err != nil {
		if errors.Is(err, ErrIllegalTransition) {
			return nil
		}
		return err
	}
	// Reflect the committed status on the row copy handed to the reporter.
	exec.Status = status
	exec.OutcomeSource = sql.NullString{String: string(source), Valid: true}

	var errCode, errMsg string
	if status == StatusFailed {
		errCode, errMsg = failureDetail(exitCode, signaled, res)
	}
	return p.reporter.EnqueueTerminal(exec, status, artifacts, errCode, errMsg)
}

// buildArtifacts assembles the pointer-first artifacts for a terminal report: a
// diff (always for completed; for failed only when the tree actually changed) and
// the transcript pointer. A diff-capture error is logged but never wedges the
// report — the transcript pointer still ships.
func (p *TerminalProcessor) buildArtifacts(ctx context.Context, exec *Execution, status Status) []Artifact {
	var arts []Artifact
	diffArt, err := p.diff(ctx, p.daemonID, exec.ExecutionID, exec.RepoLocation, exec.HeadAtStart)
	switch {
	case err != nil:
		p.logf("[execution] diff capture execution=%s: %v", exec.ExecutionID, err)
	case status == StatusCompleted || diffHasChanges(diffArt):
		arts = append(arts, diffArt)
	}
	arts = append(arts, TranscriptArtifact(p.daemonID, exec.ExecutionID))
	return arts
}

// diffHasChanges reports whether a diff artifact's summary shows any changed
// files. Used to decide whether a failed report should carry the diff.
func diffHasChanges(a Artifact) bool {
	files, _ := a.Meta["files"].(int)
	return files > 0
}

// failureDetail maps a failed outcome onto a stable {code, message} for the
// report error object (m0-contract §2). A result-reported error wins; otherwise
// the process signal / non-zero exit is described.
func failureDetail(exitCode int, signaled bool, res ResultOutcome) (code, msg string) {
	switch {
	case res.HasResult && res.IsError:
		sub := res.Subtype
		if sub == "" {
			sub = "error"
		}
		return "execution_error", "agent result error: " + sub
	case signaled:
		return "execution_error", "subprocess terminated by signal"
	default:
		return "execution_error", fmt.Sprintf("subprocess exited with code %d", exitCode)
	}
}

