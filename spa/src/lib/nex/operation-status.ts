// spa/src/lib/nex/operation-status.ts — what state one tool call is in, decided
// once for every view that draws it (R2 plan T2.0). The room's OperationBlock
// and chat's operation lines both read it, so "failed" in chat cannot drift
// from "failed" in the room. Pure: no React, no store.
import type { ToolActivity, ToolCallActivity } from './tool-activity'
import type { ToolResultFacts } from './tool-result-facts'
import type { OperationResult } from './operations'

/** `pending` is a call with nothing said about it yet — no activity, no facts, no result. */
export type OpStatus = ToolActivity['status'] | 'streaming' | 'pending'

/**
 * What the block is: the lifecycle variant when there is one, else the N2
 * status, else what the raw frame's `is_error` says. N2 outranks the raw
 * frame both ways — a denial it flagged is not downgraded by a result that
 * arrived without `is_error`, and an `is_error` it contradicts is not an
 * error (P-B3 R3 / codex R2 A1).
 */
export function resolveStatus(
  activity: ToolCallActivity | undefined,
  facts: ToolResultFacts | undefined,
  result: OperationResult | null,
): OpStatus {
  if (activity) return activity.status
  if (facts?.status) return facts.status
  if (result) return result.isError ? 'error' : 'done'
  return 'pending'
}
