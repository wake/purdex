// spa/src/lib/conversations/deck-format.ts — the pure rules the deck draws by (U3 spec §4): which caption a user block
// gets, what the status slot of a step says, how an output is cut to its last lines, and the adapter that gives a wire
// diff to the room's `ToolDiffView`. No React, no store.
import type { DiffHunk as ActivityHunk, ToolActivity } from '../nex/tool-activity'
import type { DiffHunk, StepDiff, StepItem, StepOutput, SystemItem, UserItem } from './types'

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

/** Lines in a text, without allocating one string per line (an output can be large). A trailing newline ends a line. */
function countLines(text: string): number {
  if (text === '') return 0
  let n = 1
  for (let i = text.indexOf('\n'); i >= 0 && i < text.length - 1; i = text.indexOf('\n', i + 1)) n++
  return n
}

/**
 * The last `OUTPUT_TAIL_LINES` lines of an output. Scans from the end for the newlines it needs instead of splitting the
 * whole text, and reads a payload with missing or wrong-typed fields as empty rather than throwing in a render.
 */
export function outputTail(out: StepOutput): OutputTail {
  const raw = typeof out?.text === 'string' ? out.text : ''
  const text = raw.endsWith('\n') ? raw.slice(0, -1) : raw
  const given = Number.isFinite(out?.total_lines) && out.total_lines > 0 ? Math.floor(out.total_lines) : 0
  let start = 0
  let found = 0
  for (let at = text.length; found < OUTPUT_TAIL_LINES && at > 0;) {
    const i = text.lastIndexOf('\n', at - 1)
    if (i < 0) break
    found++
    start = i + 1
    at = i
  }
  if (found < OUTPUT_TAIL_LINES) {
    const shown = text === '' ? 0 : found + 1
    return { text, totalLines: Math.max(given, shown), cut: out?.truncated === true || given > shown }
  }
  return { text: text.slice(start), totalLines: Math.max(given, countLines(text)), cut: true }
}

/** A bare text as a `StepOutput` (a `command_output` item has only text), so one fold draws both. */
export function textOutput(text: string, truncated = false): StepOutput {
  return { text, total_lines: countLines(text), total_bytes: text.length, truncated }
}

type ActivityDiff = NonNullable<ToolActivity['diff']>

/** How many diff lines the deck draws before 「顯示全部 N 行」 (spec §4). */
export const DIFF_DECK_LINES = 16

/** A hunk list read defensively: a missing list, or a hunk without a `lines` array, counts as nothing. */
function safeHunks(diff: StepDiff): DiffHunk[] {
  return Array.isArray(diff.hunks) ? diff.hunks.filter((h) => h && Array.isArray(h.lines)) : []
}

/**
 * The first `DIFF_DECK_LINES` lines of a diff, hunk by hunk (a hunk whose lines are all past the budget is dropped), with
 * the line count of the whole diff and whether anything was left off — by this cap or by the daemon.
 */
export function capDiff(diff: StepDiff): { diff: StepDiff; totalLines: number; cut: boolean } {
  const hunks = safeHunks(diff)
  const totalLines = hunks.reduce((n, h) => n + h.lines.length, 0)
  let left = DIFF_DECK_LINES
  const kept: DiffHunk[] = []
  for (const h of hunks) {
    if (left <= 0) break
    kept.push(h.lines.length <= left ? h : { ...h, lines: h.lines.slice(0, left) })
    left -= h.lines.length
  }
  return { diff: { ...diff, hunks: kept }, totalLines, cut: totalLines > DIFF_DECK_LINES || diff.truncated === true }
}

/** The wire diff as the room's `ToolDiffView` takes it (camelCase hunks, `truncated` always a boolean). */
export function toActivityDiff(diff: StepDiff): ActivityDiff {
  const hunks: ActivityHunk[] = safeHunks(diff).map((h) => ({
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
  /** A command's output (`command_output`): folded to 「輸出 · N 行」, opening to its last lines. */
  | { kind: 'output'; text: string }
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
  if (item.kind === 'command_output' && text !== '' && (text.includes('\n') || text.length > SHORT_NOTICE_CHARS)) {
    return { kind: 'output', text }
  }
  return text.includes('\n') || label.length > SHORT_NOTICE_CHARS ? { kind: 'note', text: label } : { kind: 'notice', text: label }
}
