// spa/src/lib/nex/stint-enrichment.ts — conversation entity spec §10.4: what
// an earlier worker stint's own event log adds to the prelude lines it wrote.
// The live view's reducer (applyDurableEvent) over the stint's events, then
// the parts a segment draws with: each call's real status and duration, its
// subagent task rows, and the reduced messages (Task 32 reads the turns'
// results from them). Pure.
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
  const s = events.slice(0, budget).reduce(applyDurableEvent, defaultExecutionState())
  // fromEntries defines own data properties: a `__proto__` id stays an id.
  const tools = Object.fromEntries(Object.entries(s.tools).filter(([, t]) => t.status !== 'running'))
  const settled: TaskTable = Object.fromEntries(Object.entries(s.tasks).filter(([, row]) => row.status !== 'running'))
  return { tools, subagentTasks: subagentTasksByToolUse(settled), messages: s.messages, truncated: events.length > budget }
}
