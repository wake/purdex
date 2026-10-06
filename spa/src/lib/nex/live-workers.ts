// spa/src/lib/nex/live-workers.ts — conversation entity spec §4.2 / §9 / D9.
// Worker LIST UIs show live conversations only, one row per entity (its latest stint). The shared
// execution store keeps raw items; only list views call `liveEntityRows`.
import type { ExecutionSummary } from './types'

/** Spec §4.2: live = not archived and not terminated (failed / rejected still count as live). */
export const isLiveRow = (row: Pick<ExecutionSummary, 'archived' | 'state'>): boolean => !row.archived && row.state !== 'terminated'

/** A conversation's identity: its session id, else the one it resumes (before turn 1), else the row id. */
export const entityKeyOf = (row: ExecutionSummary): string => row.session_id || row.resume_session_id || row.id

const newer = (a: ExecutionSummary, b: ExecutionSummary): boolean =>
  a.created_at !== b.created_at ? a.created_at > b.created_at : a.id > b.id

/** Live rows only, one per entity (the latest stint: larger created_at, then larger id), in input order. */
export function liveEntityRows(items: readonly ExecutionSummary[]): ExecutionSummary[] {
  const best = new Map<string, ExecutionSummary>()
  for (const row of items) {
    if (!isLiveRow(row)) continue
    const key = entityKeyOf(row)
    const cur = best.get(key)
    if (!cur || newer(row, cur)) best.set(key, row)
  }
  const keep = new Set(best.values())
  return items.filter((row) => keep.has(row))
}
