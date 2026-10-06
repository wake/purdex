// spa/src/lib/nex/stint-enrichment.ts — conversation entity spec §10.4: what
// an earlier worker stint's own event log adds to the prelude lines it wrote.
// The live view's reducer (applyDurableEvent) over the stint's events, then
// the parts a segment draws with: each call's real status and duration, its
// subagent task rows, and the reduced messages and each turn's cost by the assistant
// message ids it answered (§10.4 `result` frames), and the images each send carried. Pure.
import { parseAttachmentMeta, type AttachmentMeta } from './attachments'
import { costSummary, type TurnCost } from './cost-summary'
import { applyDurableEvent, defaultExecutionState } from './event-reducer'
import type { StreamMessage } from './message-types'
import { subagentTasksByToolUse, type TaskTable } from './tasks'
import type { ToolActivity } from './tool-activity'
import type { NexEvent, WorkerTask } from './types'

export interface StintEnrichment {
  /** By tool_use_id, from the raw frames and the N2 tool events; a segment's entry wins over the transcript's. Settled calls only. */
  tools: Readonly<Record<string, ToolActivity>>
  /** Subagent task rows by their Task call's tool_use_id (`subagentTasksByToolUse`). Settled rows only. */
  subagentTasks: ReadonlyMap<string, WorkerTask>
  /** The stint's reduced messages. */
  messages: readonly StreamMessage[]
  /**
   * Each top-level assistant `message.id` -> the TurnCost of the top-level `result` that closed its turn
   * (costSummary's own turns, paired positionally with the result-delimited id groups). An id after the last result maps to nothing.
   */
  costByMessageId: ReadonlyMap<string, TurnCost>
  /**
   * The k-th entry is the k-th `execution.delegated` / `execution.message_accepted` that carried attachments, in event
   * order: its `attachments` as parseAttachmentMeta keeps them. An event with no valid one adds nothing.
   */
  attachmentsByPrompt: readonly (readonly AttachmentMeta[])[]
  /** The log ran past the budget: only its first `budget` events were applied. */
  truncated: boolean
}

/** Events applied per stint (spec §10.6); past it the segment says so in a muted line. */
export const ENRICHMENT_EVENT_BUDGET = 5000

/**
 * `events.slice(0, budget)` reduced with applyDurableEvent from a fresh state.
 * `truncated` = there were more than `budget` (the cache fetches one page
 * past it, so exactly `budget` is not mistaken for more).
 * A call or a subagent still running at the end is left out: its answer lies
 * past the budget, or the stint was cut off. The transcript's own entry
 * stands for it then — never a live clock in the prelude (spec §5.2).
 */
export function enrichFromEvents(events: readonly NexEvent[], budget = ENRICHMENT_EVENT_BUDGET): StintEnrichment {
  const applied = events.slice(0, budget)
  const s = applied.reduce(applyDurableEvent, defaultExecutionState())
  // fromEntries defines own data properties: a `__proto__` id stays an id.
  const tools = Object.fromEntries(Object.entries(s.tools).filter(([, t]) => t.status !== 'running'))
  const settled: TaskTable = Object.fromEntries(Object.entries(s.tasks).filter(([, row]) => row.status !== 'running'))
  return {
    tools, subagentTasks: subagentTasksByToolUse(settled), messages: s.messages, costByMessageId: costByMessageId(s.messages),
    attachmentsByPrompt: attachmentsByPrompt(applied), truncated: events.length > budget,
  }
}

/** Each send's attachments (`delegated` opens the stint, `message_accepted` every later turn), in event order; a send with none is skipped. */
function attachmentsByPrompt(events: readonly NexEvent[]): AttachmentMeta[][] {
  const out: AttachmentMeta[][] = []
  for (const ev of events) {
    if (ev.kind !== 'execution.delegated' && ev.kind !== 'execution.message_accepted') continue
    const list = parseAttachmentMeta(ev.payload?.attachments)
    if (list.length > 0) out.push(list)
  }
  return out
}

/** costSummary's turns, each keyed by the top-level assistant message ids seen since the previous top-level result. */
function costByMessageId(messages: readonly StreamMessage[]): ReadonlyMap<string, TurnCost> {
  const turns = costSummary(messages).turns
  const out = new Map<string, TurnCost>()
  let pending: string[] = []
  let turn = 0
  for (const m of messages) {
    const p = m as { type?: unknown; parent_tool_use_id?: unknown; message?: { id?: unknown } }
    if (p.parent_tool_use_id != null) continue
    if (p.type === 'assistant') {
      if (typeof p.message?.id === 'string') pending.push(p.message.id)
    } else if (p.type === 'result') {
      // costSummary counts the same frames (C1), so the n-th result is turns[n].
      for (const id of pending) out.set(id, turns[turn])
      pending = []
      turn++
    }
  }
  return out
}
