// spa/src/lib/nex/execution-search.ts — the search over live execution rows (Settings → Worker → 測試用 「執行中」).
// `matchesConversationQuery` takes a ConversationRow; a live row is an ExecutionSummary, so it has its own matcher.
import { shortenHome } from './conversation-search'
import { workerRowName } from './worker-row-name'
import type { ExecutionSummary } from './types'

/**
 * Case-insensitive substring match of the trimmed query over the cwd (as displayed `~/…` and as it is), the brief,
 * the execution id, the provider and the name the row shows (`workerRowName`: the session title only when
 * `titleSupported`, so a cached title the row does not show is never searchable). A blank query matches every row.
 */
export function matchesExecutionQuery(
  row: Pick<ExecutionSummary, 'cwd' | 'brief' | 'id' | 'provider' | 'session_title'>,
  q: string,
  home: string,
  titleSupported: boolean,
): boolean {
  const needle = q.trim().toLowerCase()
  if (needle === '') return true
  const cwd = row.cwd ?? ''
  const fields = [cwd === '' ? '' : shortenHome(cwd, home), cwd, row.brief ?? '', row.id, row.provider ?? '', workerRowName(row, titleSupported)]
  return fields.some((s) => s.toLowerCase().includes(needle))
}
