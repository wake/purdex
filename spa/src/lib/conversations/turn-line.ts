// spa/src/lib/conversations/turn-line.ts — the two lines the deck and the chat draw at a turn's edges (c9d9c7-54 pts 3, 5), in the shape of the
// terminal's `✻ Puzzling… (3m 3s)` and `✻ Brewed for 20s · done 2:58 AM`: while the agent works, a live line at the very end of the stream
// (since when, and what it runs now); when a turn is done, one closing line (how long it took, when it ended). Everything is read from the
// conversation the daemon already sends — the turn header's times and outcome, the steps' status, the header's status; nothing is added.
import type { ConversationItem, StepItem } from './types'

export interface LineTurn {
  started_at?: number
  ended_at?: number
  outcome?: string
  duration_ms?: number
  output_tokens?: number
  items: ConversationItem[]
}

export interface LiveLine {
  kind: 'running' | 'waiting'
  /** When the turn that is running began (ms); null when the conversation does not say (the line then shows no clock). */
  since: number | null
  /** The tool of the step that runs now (the latest running one that is not an Agent/Task), if any. */
  tool: string | null
  /** How many Agent/Task steps are running now. */
  subagents: number
  /** What the running turn's messages have written so far; null when the conversation does not say (or none yet). */
  outputTokens: number | null
  /** Whether any step runs now (a tool without a name still counts): false means the model is thinking or writing. */
  busy: boolean
}

const isStep = (i: ConversationItem): i is StepItem => i.type === 'step'

/** A token count that is a positive whole number, else null (nothing to show). */
const tokensOf = (n: unknown): number | null => (typeof n === 'number' && Number.isFinite(n) && n > 0 ? Math.floor(n) : null)

/** `842` below a thousand, then thousands with one decimal: `9.2k`, `12.4k`, `103.0k` (the terminal's `↓ 9.2k tokens`, without the word). */
export function formatTokens(n: number): string {
  if (n < 1000) return String(n)
  return `${(n / 1000).toFixed(1)}k`
}
/**
 * The live line, or null when the agent is not working. `status` is the conversation header's (`running` | `waiting` | …): `running` and
 * `waiting` are the two that draw. The turn it counts from is the last one while it is still `running`.
 */
export function liveLine(turns: readonly LineTurn[], status: string | undefined): LiveLine | null {
  if (status !== 'running' && status !== 'waiting') return null
  const last = turns[turns.length - 1]
  const running = last !== undefined && (last.outcome === 'running' || last.outcome === undefined) ? last : undefined
  const steps = (running?.items ?? []).filter(isStep).filter((s) => s.status === 'running')
  const subagents = steps.filter((s) => s.kind === 'task').length
  const latest = [...steps].reverse().find((s) => s.kind !== 'task') // subagents are counted, not named
  return {
    kind: status,
    since: typeof running?.started_at === 'number' && running.started_at > 0 ? running.started_at : null,
    tool: latest?.tool || null,
    subagents,
    outputTokens: tokensOf(running?.output_tokens),
    busy: steps.length > 0,
  }
}

export interface DoneLine {
  durationMs: number
  endedAt: number
  /** The turn's output tokens, null when it has none to show. */
  outputTokens: number | null
}

/** The closing line of a finished turn: only `done` (an interrupted or failed turn already says so), and only when both times are known. */
export function doneLine(turn: Pick<LineTurn, 'outcome' | 'started_at' | 'ended_at' | 'duration_ms' | 'output_tokens'>): DoneLine | null {
  if (turn.outcome !== 'done' || typeof turn.ended_at !== 'number') return null
  const ms = typeof turn.duration_ms === 'number' ? turn.duration_ms : typeof turn.started_at === 'number' ? turn.ended_at - turn.started_at : null
  return ms === null || ms < 0 ? null : { durationMs: ms, endedAt: turn.ended_at, outputTokens: tokensOf(turn.output_tokens) }
}

/** `45` / `3m 03s` / `1h 02m` as parts for the locale's unit words (see `deck.dur.*`). */
export type Duration = { kind: 's'; s: number } | { kind: 'ms'; m: number; s: number } | { kind: 'hm'; h: number; m: number }
export function durationParts(ms: number): Duration {
  const total = Math.max(0, Math.floor(ms / 1000))
  if (total < 60) return { kind: 's', s: total }
  if (total < 3600) return { kind: 'ms', m: Math.floor(total / 60), s: total % 60 }
  return { kind: 'hm', h: Math.floor(total / 3600), m: Math.floor((total % 3600) / 60) }
}

type T = (key: string, params?: Record<string, string | number>) => string
const two = (n: number) => String(n).padStart(2, '0')

/** The duration as text in the reader's language. */
export function formatDuration(ms: number, t: T): string {
  const d = durationParts(ms)
  if (d.kind === 's') return t('deck.dur.s', { s: d.s })
  if (d.kind === 'ms') return t('deck.dur.ms', { m: d.m, s: two(d.s) })
  return t('deck.dur.hm', { h: d.h, m: two(d.m) })
}
