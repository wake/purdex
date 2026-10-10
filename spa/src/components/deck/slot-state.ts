// spa/src/components/deck/slot-state.ts — which state the status row's state cell shows (U3-5), from what the conversation says:
// the header status (running | waiting | idle | error | ended | unknown) and the newest item. Pure.
import type { ConversationItem, StepItem } from '../../lib/conversations/types'
import type { SlotState } from './StateSlot'

export interface SlotModel {
  state: SlotState
  exitCode?: number
  /** Since when the running step has been going (epoch ms); the row counts from it. */
  runningSince?: number
}

const isStep = (i: ConversationItem | undefined): i is StepItem => i?.type === 'step'

/**
 * - the agent waits for the person (header `waiting`) → waiting, above all else;
 * - the agent is working (header `running`) → running, counted from the running step (or the newest item);
 * - otherwise the NEWEST item decides: a denied step → denied, a failed step → `exit N` when it carries a command exit code,
 *   else failed; an `error` header with nothing newer to say → failed;
 * - everything else (idle, ended, unknown, an agent message that came after the failure) → idle.
 */
export function slotModel(status: string, items: readonly ConversationItem[]): SlotModel {
  const last = items[items.length - 1]
  if (status === 'waiting') return { state: 'waiting' }
  // Waiting for the person (an approval, a question) outranks everything: ask is never covered by a clock or an old result.
  if (status === 'running') {
    let since: number | undefined
    for (let i = items.length - 1; i >= 0; i--) {
      const it = items[i]
      if (isStep(it) && it.status === 'running') { since = it.started_at; break }
    }
    since ??= isStep(last) ? last.started_at : last?.at
    return { state: 'running', runningSince: typeof since === 'number' ? since : undefined }
  }
  if (isStep(last)) {
    if (last.status === 'denied') return { state: 'denied' }
    if (last.status === 'failed') {
      const code = last.command?.exit_code
      return typeof code === 'number' && code > 0 ? { state: 'exit', exitCode: code } : { state: 'failed' }
    }
  }
  if (status === 'error') return { state: 'failed' }
  return { state: 'idle' }
}
