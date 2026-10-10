// spa/src/lib/conversations/turn-row.ts — the chat's work rows (U3 plan D9, spec §2.9 / §5): one row per RUN of work, not per
// turn. A run is the consecutive steps of a turn; `thinking` does not break it, anything else (user, agent_text, system,
// an unknown item) does. The text rules are iOS's (DialogueWork.summary) and are checked only against the committed
// `testdata/conversation/v1/render/turn-rows.json`.
import type { ConversationItem, StepItem, ThinkingItem } from './types'

/** Kind → category, in the order the row lists them. */
const CATEGORIES = ['execute', 'edit', 'read', 'search', 'fetch', 'task', 'other'] as const
export type RunCategory = (typeof CATEGORIES)[number]
export const CATEGORY_LABEL: Record<RunCategory, string> = {
  execute: '指令', edit: '編輯', read: '讀取', search: '搜尋', fetch: '網頁', task: '子 agent', other: '其他',
}

export const categoryOf = (kind: string): RunCategory =>
  (CATEGORIES as readonly string[]).includes(kind) && kind !== 'other' ? (kind as RunCategory) : 'other'

export interface TurnRun {
  /** The run's steps, in order. */
  steps: StepItem[]
  stepIds: string[]
  running: boolean
  /** The finished row's text; `null` while a step of the run is still running (the live progress message). */
  text: string | null
  /** `<類別> <step summary>` of the run's latest step, only while running (UI: 「正在：<latest>」). */
  latest?: string
  /** min(started_at) over the steps: the live clock counts from here. */
  startedAt: number
}

/** What a turn's items cut into, in order: a run, or one item that breaks runs (and thinking outside any run). */
export type Segment = { kind: 'run'; run: TurnRun } | { kind: 'item'; item: ConversationItem }

export interface TurnRows {
  turnId: string
  index: number
  segments: Segment[]
  runs: TurnRun[]
}

/** 「N 秒」 / 「N 分」 / 「N 分 M 秒」 / 「N 時」 / 「N 時 M 分」. */
export function formatSpan(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000))
  if (total < 60) return `${total} 秒`
  if (total < 3600) {
    const m = Math.floor(total / 60)
    const s = total % 60
    return s === 0 ? `${m} 分` : `${m} 分 ${s} 秒`
  }
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  return m === 0 ? `${h} 時` : `${h} 時 ${m} 分`
}

function runText(steps: StepItem[], startedAt: number): string {
  const end = Math.max(...steps.map((s) => s.started_at + (s.duration_ms ?? 0)))
  const counts = new Map<RunCategory, number>()
  let failed = 0
  let denied = 0
  let interrupted = 0
  for (const s of steps) {
    const c = categoryOf(s.kind)
    counts.set(c, (counts.get(c) ?? 0) + 1)
    if (s.status === 'failed') failed++
    else if (s.status === 'denied') { if (s.denial === 'interrupted') interrupted++; else denied++ }
  }
  const kinds = CATEGORIES.filter((c) => counts.get(c)).map((c) => `${counts.get(c)} 個${CATEGORY_LABEL[c]}`).join('、')
  const parts = [`處理了 ${formatSpan(end - startedAt)}`]
  if (kinds) parts.push(kinds)
  if (failed) parts.push(`${failed} 失敗`)
  if (denied) parts.push(`${denied} 已拒絕`)
  if (interrupted) parts.push(`${interrupted} 已中斷`)
  return parts.join(' · ')
}

export function makeRun(steps: StepItem[]): TurnRun {
  const startedAt = Math.min(...steps.map((s) => s.started_at))
  const running = steps.some((s) => s.status === 'running')
  const last = steps[steps.length - 1]
  return {
    steps, stepIds: steps.map((s) => s.id), running, startedAt,
    text: running ? null : runText(steps, startedAt),
    latest: running ? `${CATEGORY_LABEL[categoryOf(last.kind)]} ${last.summary}` : undefined,
  }
}

const isThinking = (it: ConversationItem): it is ThinkingItem => it.type === 'thinking'
const isStep = (it: ConversationItem): it is StepItem => it.type === 'step'

/** Cuts one turn's items into segments. Thinking between two steps of a run is absorbed; thinking elsewhere stays an item. */
export function segmentItems(items: ConversationItem[]): Segment[] {
  const out: Segment[] = []
  let steps: StepItem[] = []
  let pending: ConversationItem[] = [] // thinking seen since the last step: it belongs to the run only if another step follows
  const flush = () => {
    if (steps.length) out.push({ kind: 'run', run: makeRun(steps) })
    for (const p of pending) out.push({ kind: 'item', item: p })
    steps = []
    pending = []
  }
  for (const it of items) {
    if (isStep(it)) { steps.push(it); pending = [] } else if (isThinking(it) && steps.length) pending.push(it)
    else { flush(); out.push({ kind: 'item', item: it }) }
  }
  flush()
  return out
}

export function turnRows(turn: { id: string; index: number; items: ConversationItem[] }): TurnRows {
  const segments = segmentItems(turn.items)
  const runs = segments.flatMap((s) => (s.kind === 'run' ? [s.run] : []))
  return { turnId: turn.id, index: turn.index, segments, runs }
}
