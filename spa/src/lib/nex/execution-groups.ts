// spa/src/lib/nex/execution-groups.ts — pure helpers behind the sidebar
// Executions view (P-C spec §4.3): bucketing rows by `labels.source` and
// recognising a same-host tmux-session origin.
import type { ExecutionSummary } from './types'

export interface ExecutionGroup {
  source: string
  rows: ExecutionSummary[]
}

/** Newest-first rows bucketed by `labels.source ?? 'local'`; groups ordered by their newest row. */
export function groupBySource(items: ExecutionSummary[]): ExecutionGroup[] {
  const sorted = [...items].sort((a, b) => b.updated_at - a.updated_at)
  const groups = new Map<string, ExecutionSummary[]>()
  for (const row of sorted) {
    const source = row.labels.source ?? 'local'
    const bucket = groups.get(source)
    if (bucket) bucket.push(row)
    else groups.set(source, [row])
  }
  return Array.from(groups, ([source, rows]) => ({ source, rows }))
}

/** The tmux session code when `origin` points at a session on `hostId`; `null` for any other origin. */
export function sameHostSessionCode(origin: string | undefined, hostId: string): string | null {
  const prefix = `purdex://host/${hostId}/session/`
  if (!origin || !origin.startsWith(prefix)) return null
  const code = origin.slice(prefix.length).split('/', 1)[0] ?? ''
  return code === '' ? null : code
}
