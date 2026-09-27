// spa/src/lib/nex/subagent-close-out.ts — a subagent's close-out
// from its task row (R4 T3.3, user decision Q2): running → a ticking elapsed;
// ended → `26k tokens · 8 tools · 12s` from `usage`, each part only when the
// row has it, then a status word — `failed` in red, `stopped` (killed) and
// `interrupted` (lost: the contract's "unknown", not a failure) neutral.
// Never a cost (`task_end.cost_usd` is always null) and never a placeholder.
// Drawn by SubagentBlock, or on the Task call's own header when the call has
// no children to fold (a background subagent, frames not arrived).
import { formatCoarseDuration } from './format-duration'
import type { WorkerTask } from './types'

type TFunction = (key: string, params?: Record<string, string | number>) => string

/** 900 → '900', 1 500 → '1.5k', 26 400 → '26k', 1 500 000 → '1.5M'. */
export function formatTokenCount(n: number): string {
  const short = (v: number, unit: string) => `${v < 10 ? Math.round(v * 10) / 10 : Math.round(v)}${unit}`
  if (n >= 1_000_000) return short(n / 1_000_000, 'M')
  if (n >= 1_000) return short(n / 1_000, 'k')
  return String(Math.max(0, Math.round(n)))
}

const count = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v >= 0

export interface CloseOut {
  /** The parts from `usage`, or null when the row has none (the caller keeps its own tool count). */
  usage: string[] | null
  /** Running only: time since `started_at` (none without a clock). */
  elapsed: string | null
  status: { text: string; error: boolean } | null
}

export function closeOut(task: WorkerTask, now: number | undefined, t: TFunction): CloseOut {
  let usage: string[] | null = null
  if (task.usage) {
    const u = task.usage
    usage = []
    if (count(u.total_tokens)) usage.push(t('room.subagent.tokens', { tokens: formatTokenCount(u.total_tokens) }))
    if (count(u.tool_uses)) usage.push(t(`room.subagent.tools_${u.tool_uses === 1 ? 'one' : 'other'}`, { count: u.tool_uses }))
    if (count(u.duration_ms)) usage.push(formatCoarseDuration(u.duration_ms))
  }
  const running = task.status === 'running'
  const elapsed = running && task.started_at !== null && now !== undefined ? formatCoarseDuration(now - task.started_at) : null
  const status = task.status === 'failed' ? { text: t('room.subagent.status.failed'), error: true }
    : task.status === 'killed' ? { text: t('room.subagent.status.killed'), error: false }
    : task.status === 'lost' ? { text: t('room.subagent.status.lost'), error: false }
    : null
  return { usage: running ? null : usage, elapsed, status }
}
