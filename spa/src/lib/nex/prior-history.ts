// spa/src/lib/nex/prior-history.ts — R4 T2.2 (Q3): does this execution's cost
// include spend from before a hand-over?
//
// Only a hand-over sets `resume_session_id` (purdex internal/module/nex/
// handoff.go, `execution.Request.ResumeSessionID`), and Nexen uses it on turn 1
// only (execution/launch.go `resolveLaunchPlan`, the `turn.Idx ==
// firstTurnIdx && exec.ResumeSessionID != ""` branch). When turn 1 really
// resumed, its `result.total_cost_usd` carries the resumed session's earlier
// spend, and the pane's cost (cost-summary.ts) shows it as turn 1's cost.
//
// The two ways turn 1 does NOT resume, and the summary field each leaves:
//   - Delegate's preflight finds no transcript → the execution is `rejected`
//     with a `reject_reason` (execution/service.go, `s.reject(...)`); no turn
//     ever runs.
//   - The launch-time gate (launch.go, `resumeTargetPresent` failing) ends
//     turn 1 with reason `session_expired` and — because `fatal` is not
//     cleared for a consumer-supplied resume id — fails the execution through
//     `FinishTurnFailingExecution`, which writes `terminal_reason =
//     'session_expired'` (and `last_turn_reason`, state `failed`).
// `terminal_reason` is the signal, not `last_turn_reason`: the latter is
// overwritten by every later turn, and a LATER turn can also end
// `session_expired` (launch.go's owned-session branch clears `fatal`, so the
// execution stays idle) without undoing turn 1's successful resume. Only
// turn 1 can make `terminal_reason` be `session_expired`.
//
// Review #7: the summary alone cannot tell "turn 1 resumed" from "turn 1 has
// not produced anything yet" (still running, a fatal launch error with no
// result, terminated first). There is no prior spend to note until a
// top-level `result` actually billed something, so the note also needs one
// costed turn in the pane's `costSummary` (computed once by the caller).
//
// R4 T4.1: a list row has no history loaded; there the row's own rollup
// `cost_usd` (a number > 0) is the "something was billed" evidence. Both
// call sites share `turnOneResumed`.
import type { CostSummary } from './cost-summary'
import type { ExecutionSummary } from './types'

/** The summary-only half: a hand-over set `resume_session_id`, and turn 1 did not fail to resume. */
function turnOneResumed(summary: ExecutionSummary | null): summary is ExecutionSummary {
  if (!summary?.resume_session_id) return false
  if (summary.state === 'rejected' || summary.reject_reason) return false
  return summary.terminal_reason !== 'session_expired'
}

/** The pane: evidence is a costed top-level result in its own `costSummary`. */
export function costIncludesPriorHistory(summary: ExecutionSummary | null, cost: CostSummary | null): boolean {
  return turnOneResumed(summary) && cost?.turns.some((t) => t.costUsd > 0) === true
}

/** A list row: evidence is the rollup `cost_usd` (a number > 0). */
export function rowCostIncludesPriorHistory(row: ExecutionSummary): boolean {
  return turnOneResumed(row) && typeof row.cost_usd === 'number' && row.cost_usd > 0
}
