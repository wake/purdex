// spa/src/lib/nex/operation-status.ts — what state one tool call is in, decided
// once for every view that draws it (R2 plan T2.0). The room's OperationBlock
// and chat's operation lines both read it, so "failed" in chat cannot drift
// from "failed" in the room. Pure: no React, no store.
import { toToolCallActivity, type ToolActivity, type ToolCallActivity } from './tool-activity'
import type { ToolResultFacts } from './tool-result-facts'
import { blockKey, toolResultText, type BlockKey, type OperationIndex, type OperationResult } from './operations'
import type { ContentBlock, StreamMessage } from './message-types'

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

/** How chat draws one operation (spec §5): in the turn's tools line, as an `Edited …` line, or as a red line. */
export type OperationKind = 'plain' | 'edited' | 'failed'

export interface TurnOperation {
  /** The block's position, as the room keys it (and folds it). */
  key: BlockKey
  msgIndex: number
  blockIndex: number
  kind: OperationKind
  status: OpStatus
}

/** The N2 entry for a tool_use id, by own key: ids are untrusted strings (`constructor`). */
export function toolEntryFor(tools: Record<string, ToolActivity> | undefined, id: string | undefined): ToolActivity | undefined {
  return tools && id && Object.hasOwn(tools, id) ? tools[id] : undefined
}

function blocksOf(message: StreamMessage | undefined): ContentBlock[] {
  const content = (message as { message?: { content?: unknown } } | undefined)?.message?.content
  return Array.isArray(content) ? (content as ContentBlock[]) : []
}

/**
 * A turn's top-level operations in call order, each sorted by the rule the
 * room draws it by (R2 plan T2.0): `failed` when `resolveStatus` says error or
 * denied, else `edited` when the N2 facts carry a diff, else `plain`.
 *
 * "Operations" are exactly the blocks the room draws as an OperationBlock at
 * the top level (MessageRow): every assistant `tool_use` — a Task is plain,
 * its subagent is drawn inside it — and every `tool_result` no call claimed
 * (an orphan, which the room draws as a block of its own). A subagent's
 * frames (`childIndexes`) are skipped: their calls live inside the Task. The
 * status inputs are the ones MessageRow hands OperationBlock, so both views
 * agree on every call.
 */
export function classifyTurnOperations(
  messages: StreamMessage[],
  turn: { start: number; end: number },
  index: OperationIndex,
  tools: Record<string, ToolActivity> | undefined,
): TurnOperation[] {
  const ops: TurnOperation[] = []
  const end = Math.min(turn.end, messages.length)
  for (let mi = Math.max(0, turn.start); mi < end; mi++) {
    if (index.childIndexes.has(mi)) continue
    const msg = messages[mi]
    if (msg.type !== 'assistant' && msg.type !== 'user') continue
    blocksOf(msg).forEach((block, bi) => {
      const key = blockKey(mi, bi)
      let facts: ToolResultFacts | undefined
      let status: OpStatus
      if (msg.type === 'assistant' && block.type === 'tool_use') {
        const entry = toolEntryFor(tools, block.id)
        facts = entry
        // An activity's status does not depend on the clock, so `now` is moot here.
        status = resolveStatus(entry ? toToolCallActivity(entry, 0) : undefined, entry, index.resultForCall.get(key) ?? null)
      } else if (msg.type === 'user' && block.type === 'tool_result' && !index.consumedResults.has(key)) {
        facts = toolEntryFor(tools, block.tool_use_id)
        status = resolveStatus(undefined, facts, { text: toolResultText(block.content), isError: block.is_error ?? false })
      } else {
        return
      }
      const kind: OperationKind = status === 'error' || status === 'denied' ? 'failed'
        : facts?.diff !== undefined ? 'edited'
        : 'plain'
      ops.push({ key, msgIndex: mi, blockIndex: bi, kind, status })
    })
  }
  return ops
}
