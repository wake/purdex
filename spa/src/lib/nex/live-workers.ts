// spa/src/lib/nex/live-workers.ts — conversation entity spec §4.2 / §9 / D9.
// Worker LIST UIs show live conversations only, one row per entity (its latest stint). The shared
// execution store keeps raw items; only list views call `liveEntityRows`.
import type { ExecutionSummary } from './types'
import { isTestCwd } from './test-cwd'
import { matchesExecutionQuery } from './execution-search'

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

export interface LiveRowFilter {
  /** `normal` drops test cwds, `test` keeps only those; omitted keeps every row. */
  filter?: 'normal' | 'test'
  /** Search text (`matchesExecutionQuery`); blank keeps every row. */
  query?: string
  /** The host's home, for the `~` display form of a cwd. */
  home?: string
  /** The host's `session_title` capability (`selectSessionTitleSupported`): lets the search see the title a row shows. */
  titleSupported?: boolean
}

/** `liveEntityRows`, then the cwd split and the search. The one place the Workers list and 測試用 agree on a row set. */
export function filterLiveRows(items: readonly ExecutionSummary[], { filter, query = '', home = '', titleSupported = false }: LiveRowFilter = {}): ExecutionSummary[] {
  let rows = liveEntityRows(items)
  if (filter) rows = rows.filter((row) => isTestCwd(row.cwd) === (filter === 'test'))
  if (query.trim() !== '') rows = rows.filter((row) => matchesExecutionQuery(row, query, home, titleSupported))
  return rows
}
