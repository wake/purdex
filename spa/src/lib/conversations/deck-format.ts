// spa/src/lib/conversations/deck-format.ts — the pure rules the deck draws by (U3 spec §4): which caption a user block
// gets, what the status slot of a step says, how an output is cut to its last lines, and the adapter that gives a wire
// diff to the room's `ToolDiffView`. No React, no store.
import type { DiffHunk as ActivityHunk, ToolActivity } from '../nex/tool-activity'
import type { StepDiff, StepItem, StepOutput, SystemItem, UserItem } from './types'

/** How many lines of an output the deck opens to (spec §4: "the last 10 lines"). */
export const OUTPUT_TAIL_LINES = 10

/** `HH:mm` in the viewer's zone, 24-hour, as the user blocks and the compaction line carry it. */
export function formatClock(ms: number): string {
  const d = new Date(ms)
  const two = (n: number) => String(n).padStart(2, '0')
  return `${two(d.getHours())}:${two(d.getMinutes())}`
}

export type UserCaption =
  | { kind: 'you'; time: string }
  | { kind: 'queued' }
  | { kind: 'from'; name: string }
  | { kind: 'background' }
  | { kind: 'schedule' }

/** The caption of a user block by where the message came from; a source this build does not know reads as the user. */
export function userCaption(item: Pick<UserItem, 'source' | 'from' | 'at'>): UserCaption {
  switch (item.source) {
    case 'queued': return { kind: 'queued' }
    case 'peer': return { kind: 'from', name: item.from?.name ?? item.from?.kind ?? '' }
    case 'task': case 'background': return { kind: 'background' }
    case 'schedule': case 'scheduled': return { kind: 'schedule' }
    default: return { kind: 'you', time: formatClock(item.at) }
  }
}

export type StepChip =
  | { kind: 'running' }
  | { kind: 'failed' }
  | { kind: 'denied' }
  | { kind: 'interrupted' }
  | { kind: 'exit'; code: number }

/**
 * What the status slot of a step shows (spec §4). `denial: interrupted` is 已中斷; any other or unknown denial is 已拒絕;
 * a failed step with a non-zero exit code says `exit N`, a plain failure 「失敗」; done shows nothing.
 */
export function stepChip(step: Pick<StepItem, 'status' | 'denial' | 'command'>): StepChip | null {
  switch (step.status) {
    case 'running': return { kind: 'running' }
    case 'denied': return step.denial === 'interrupted' ? { kind: 'interrupted' } : { kind: 'denied' }
    case 'failed': {
      const code = step.command?.exit_code
      return typeof code === 'number' && code !== 0 ? { kind: 'exit', code } : { kind: 'failed' }
    }
    case 'done': {
      // A command that finished with a non-zero exit is reported as done by some sources; the slot still tells.
      const code = step.command?.exit_code
      return typeof code === 'number' && code !== 0 ? { kind: 'exit', code } : null
    }
    default: return null
  }
}

export interface OutputTail {
  text: string
  /** The line count of the whole output (the daemon's own when it has one). */
  totalLines: number
  /** Lines were left off the front, or the daemon cut the payload: the view says 「…已截斷」. */
  cut: boolean
}

/** The last `OUTPUT_TAIL_LINES` lines of an output. */
export function outputTail(out: StepOutput): OutputTail {
  const text = out.text.endsWith('\n') ? out.text.slice(0, -1) : out.text
  const lines = text === '' ? [] : text.split('\n')
  const totalLines = Math.max(out.total_lines, lines.length)
  if (lines.length <= OUTPUT_TAIL_LINES) return { text, totalLines, cut: out.truncated || totalLines > lines.length }
  return { text: lines.slice(-OUTPUT_TAIL_LINES).join('\n'), totalLines, cut: true }
}

/** The lines of an output the user can see once it is open: the whole text, for the right panel's 「顯示全部」. */
export function outputLineCount(out: StepOutput): number {
  return Math.max(out.total_lines, out.text === '' ? 0 : out.text.replace(/\n$/, '').split('\n').length)
}

type ActivityDiff = NonNullable<ToolActivity['diff']>

/** The wire diff as the room's `ToolDiffView` takes it (camelCase hunks, `truncated` always a boolean). */
export function toActivityDiff(diff: StepDiff): ActivityDiff {
  const hunks: ActivityHunk[] = (diff.hunks ?? []).map((h) => ({
    oldStart: h.old_start, oldLines: h.old_lines, newStart: h.new_start, newLines: h.new_lines, lines: h.lines,
  }))
  return { path: diff.path, added: diff.added, removed: diff.removed, hunks, truncated: diff.truncated === true }
}

/** The line range a read step names: `read: {offset, limit}` (1-based offset) → 「第 a–b 行」; open-ended or absent → null. */
export function readRange(step: Pick<StepItem, 'read'>): { from: number; to: number } | null {
  const r = step.read
  if (!r || typeof r.limit !== 'number' || r.limit <= 0) return null
  const from = typeof r.offset === 'number' && r.offset > 0 ? r.offset : 1
  return { from, to: from + r.limit - 1 }
}

// eslint-disable-next-line no-control-regex
const ANSI = /\u001b\[[0-9;]*[A-Za-z]/g

export type SystemView =
  | { kind: 'interrupted' }
  | { kind: 'compacted'; time: string }
  /** A short notice: small centred grey text. */
  | { kind: 'notice'; text: string }
  /** A long machine note: folded under 「系統」. */
  | { kind: 'note'; text: string }

const SHORT_NOTICE_CHARS = 80

function detailText(detail: unknown): string {
  if (typeof detail === 'string') return detail
  if (detail && typeof detail === 'object') {
    const d = detail as Record<string, unknown>
    if (typeof d.text === 'string') return d.text
    if (typeof d.message === 'string') return d.message
    if (typeof d.summary === 'string') return d.summary
  }
  return ''
}

/** How a system item is drawn (spec §4: notices small, long notes folded, compaction and interrupt their own lines). */
export function systemView(item: Pick<SystemItem, 'kind' | 'detail' | 'at'>): SystemView {
  if (item.kind === 'interrupted') return { kind: 'interrupted' }
  if (item.kind === 'compacted') return { kind: 'compacted', time: formatClock(item.at) }
  const text = detailText(item.detail).replace(ANSI, '').trim()
  const label = text === '' ? item.kind : text
  return text.includes('\n') || label.length > SHORT_NOTICE_CHARS ? { kind: 'note', text: label } : { kind: 'notice', text: label }
}
