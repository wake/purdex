// spa/src/lib/nex/list-all-executions.ts — conversation entity spec §9 / D9:
// follow Nexen's cursor (ids ascending, oldest first) so the newest rows are
// never cut off; bounded, and a repeated cursor is a server fault and rejects.
import { listExecutions } from './nex-api'
import { sanitizeExecutionsPage } from './validate-executions'
import type { ExecutionSummary } from './types'

export const LIST_PAGE_LIMIT = 500
export const LIST_MAX_PAGES = 20

export interface ListAllResult { items: ExecutionSummary[]; dropped: number; truncated: boolean }

/** Pages `listExecutions` until next_cursor is '' (truncated only at LIST_MAX_PAGES; a repeated cursor or malformed page rejects). Resolves null as soon as `isCurrent()` is false after a page. */
export async function listAllExecutions(
  hostId: string,
  opts: { includeArchived: boolean },
  isCurrent: () => boolean = () => true,
): Promise<ListAllResult | null> {
  const items: ExecutionSummary[] = []
  const seenIds = new Set<string>()
  const requested = new Set<string>()
  let dropped = 0
  let cursor = ''
  for (let page = 0; page < LIST_MAX_PAGES; page += 1) {
    requested.add(cursor)
    const raw = await listExecutions(hostId, { includeArchived: opts.includeArchived, limit: LIST_PAGE_LIMIT, ...(cursor ? { cursor } : {}) })
    if (!isCurrent()) return null
    const p = sanitizeExecutionsPage(raw)
    if (p.malformed) throw new Error(`nex: malformed executions page ${page + 1}`)
    for (const item of p.items) {
      if (seenIds.has(item.id)) continue
      seenIds.add(item.id)
      items.push(item)
    }
    dropped += p.dropped
    if (p.nextCursor === '') return { items, dropped, truncated: false }
    if (requested.has(p.nextCursor)) throw new Error(`nex: executions cursor repeated at page ${page + 1}`)
    cursor = p.nextCursor
  }
  return { items, dropped, truncated: true }
}
