package execution

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
