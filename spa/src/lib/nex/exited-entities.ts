// spa/src/lib/nex/exited-entities.ts — conversation entity spec §9 / D10: the Exited tab's rows.
import { entityKeyOf, isLiveRow } from './live-workers'
import { workerLabel } from './worker-label'
import type { ExecutionSummary } from './types'

const newer = (a: ExecutionSummary, b: ExecutionSummary): boolean =>
  a.created_at !== b.created_at ? a.created_at > b.created_at : a.id > b.id

/** Entities in no live state (spec §4.2: any live stint means the entity IS a worker — it is listed live, never as exited), each represented by its latest stint, newest updated_at first. */
export function exitedEntities(items: readonly ExecutionSummary[]): ExecutionSummary[] {
  const live = new Set<string>()
  const best = new Map<string, ExecutionSummary>()
  for (const row of items) {
    const key = entityKeyOf(row)
    if (isLiveRow(row)) live.add(key)
    const cur = best.get(key)
    if (!cur || newer(row, cur)) best.set(key, row)
  }
  return [...best.entries()]
    .filter(([key]) => !live.has(key))
    .map(([, row]) => row)
    .sort((a, b) => b.updated_at - a.updated_at || (a.id < b.id ? 1 : -1))
}

/** Case-insensitive over the worker label, cwd and session id; an empty query matches everything. */
export function matchesExitedQuery(row: ExecutionSummary, q: string): boolean {
  const needle = q.trim().toLowerCase()
  if (needle === '') return true
  return [workerLabel(row), row.cwd, row.session_id ?? '', row.resume_session_id ?? '']
    .some((s) => s.toLowerCase().includes(needle))
}
