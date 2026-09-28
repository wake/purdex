// spa/src/lib/nex/turns.ts — spec §4.1: a turn is a container, not a
// decoration, and its boundaries are explicit in the data. The reducer
// records them in ExecutionState.turnStarts when execution.message_accepted
// / execution.delegated arrive; this module turns that list into ranges and
// locates each range's opening user line. Pure: no React, no store.
import type { StreamMessage, UserMessage } from './message-types'

/** The sentinel CC sends for an interrupt; it is a user text block but not an opening line. */
export const INTERRUPT_TEXT = '[Request interrupted by user]'

export interface RoomTurn {
  /** Index into `messages` of the first message in this turn. */
  start: number
  /** Exclusive end. */
  end: number
  /**
   * The opening user line's index, or null when the turn has none — a
   * leading group before the first boundary, or a boundary whose payload
   * carried no text. The turn container exists either way.
   */
  openerIndex: number | null
  /**
   * Index into `turnStarts` (and so into the index-aligned `turnMeta`) of the
   * boundary that opened this turn; null for the leading group `groupTurns`
   * adds when the first boundary is not 0 — no daemon event opened it.
   */
  boundary: number | null
}

/** A user message that reads as the human's own line (not a tool result, not the interrupt sentinel, not a subagent's prompt). */
export function isOpeningLine(msg: StreamMessage): boolean {
  if (msg.type !== 'user') return false
  // StreamMessage's catch-all member defeats narrowing on `type`.
  const u = msg as UserMessage
  if (u.parent_tool_use_id != null) return false
  return u.message.content.some(b => b.type === 'text' && b.text !== INTERRUPT_TEXT)
}

/**
 * Shape `turnStarts` into contiguous ranges that partition `messages`.
 * Starts are clamped to `[0, messages.length]` and sorted; repeats are kept
 * (a repeat is an empty turn the daemon declared, not a duplicate). A start
 * clamped to `messages.length` yields an empty trailing range — the same
 * shape as a turn that just opened and has no message yet.
 */
export function groupTurns(messages: StreamMessage[], turnStarts: readonly number[]): RoomTurn[] {
  const len = messages.length
  // Each start keeps the turnStarts index it came from, so a range maps back
  // to its boundary even if the list arrived out of order (stable sort).
  const starts: { start: number; boundary: number | null }[] = turnStarts
    .map((s, i) => ({ start: Math.min(Math.max(s, 0), len), boundary: i }))
    .sort((a, b) => a.start - b.start)
  if (len > 0 && (starts.length === 0 || starts[0].start !== 0)) starts.unshift({ start: 0, boundary: null })

  return starts.map(({ start, boundary }, i) => {
    const end = i + 1 < starts.length ? starts[i + 1].start : len
    let openerIndex: number | null = null
    for (let j = start; j < end; j++) {
      if (isOpeningLine(messages[j])) {
        openerIndex = j
        break
      }
    }
    return { start, end, openerIndex, boundary }
  })
}
