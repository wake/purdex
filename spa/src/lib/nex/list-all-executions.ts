// spa/src/lib/nex/list-all-executions.ts — conversation entity spec §9 / D9:
// follow Nexen's cursor (ids ascending, oldest first) so the newest rows are
// never cut off; bounded, and a repeated cursor stops the walk with the rows so far (stuck, not truncated).
// Delta mode (#1866 §4.3) additionally records each page's `pdx.ver` and the id range it covers, retries a busy
// daemon page (R3-3), and starts over when the daemon's epoch changes mid-walk.
import { listExecutions, type ListExecutionsOptions } from './nex-api'
import { sanitizeExecutionsPage } from './validate-executions'
import { NexApiError, type ExecutionSummary } from './types'

export const LIST_PAGE_LIMIT = 500
export const LIST_MAX_PAGES = 20
/** Delta-mode pages are smaller, so the daemon's slot is held for less (§3.8). */
export const DELTA_PAGE_LIMIT = 100
/** `upTo` of the final page: sorts after every ASCII id. */
export const UP_TO_END = '∞'

const BUSY_RETRIES = 5
const BUSY_BACKOFF_MS = 250
const BUSY_BACKOFF_CAP_MS = 2000
const EPOCH_RESTARTS = 2

/** One page's read order and the ids it answers for: the first page with `id <= upTo` is an id's covering page. */
export interface WalkPage { ver: number; upTo: string }

export interface ListAllResult {
  items: ExecutionSummary[]
  /** One entry per page walked; `ver` is 0 in legacy mode and for a page without a valid stamp. */
  pages: WalkPage[]
  /** The daemon epoch every stamped page agreed on; undefined for a legacy or unversioned walk. */
  epoch?: string
  dropped: number
  truncated: boolean
  stuck: boolean
  stuckPage: number | null
}

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms))

/**
 * Pages `listExecutions` until next_cursor is '' (truncated only at LIST_MAX_PAGES;
 * a repeated cursor resolves stuck; a malformed page rejects).
 * Resolves null as soon as `isCurrent()` is false after a page or a backoff wait.
 */
export async function listAllExecutions(
  hostId: string,
  opts: { includeArchived: boolean; sessionId?: string; labels?: Record<string, string>; delta?: boolean },
  isCurrent: () => boolean = () => true,
): Promise<ListAllResult | null> {
  const delta = opts.delta === true
  const limit = delta ? DELTA_PAGE_LIMIT : LIST_PAGE_LIMIT
  let items: ExecutionSummary[] = []
  let pages: WalkPage[] = []
  let seenIds = new Set<string>()
  let requested = new Set<string>()
  let epoch: string | undefined
  let dropped = 0
  let cursor = ''
  let restarts = 0
  for (let page = 0; page < LIST_MAX_PAGES; page += 1) {
    requested.add(cursor)
    const pageOpts: ListExecutionsOptions = {
      includeArchived: opts.includeArchived,
      limit,
      ...(delta ? { pdxRetry: true } : {}),
      ...(opts.sessionId ? { sessionId: opts.sessionId } : {}),
      ...(opts.labels ? { labels: opts.labels } : {}),
      ...(cursor ? { cursor } : {}),
    }
    let raw: Awaited<ReturnType<typeof listExecutions>>
    for (let attempt = 0; ; attempt += 1) {
      try {
        raw = await listExecutions(hostId, pageOpts)
        break
      } catch (err) {
        if (!delta || !(err instanceof NexApiError) || err.code !== 'nex_busy' || attempt >= BUSY_RETRIES) throw err
        if (!isCurrent()) return null
        await sleep(Math.min(BUSY_BACKOFF_MS * 2 ** attempt, BUSY_BACKOFF_CAP_MS))
        if (!isCurrent()) return null
      }
    }
    if (!isCurrent()) return null
    const p = sanitizeExecutionsPage(raw)
    if (p.malformed) throw new Error(`nex: malformed executions page ${page + 1}`)
    if (delta && !p.pdx) console.warn('nex-delta: executions page without a valid pdx stamp; rows are unversioned', { hostId, page: page + 1 })
    if (delta && p.pdx) {
      if (epoch !== undefined && p.pdx.epoch !== epoch) {
        // Another daemon process answered: its vers are not comparable with the earlier pages'.
        if (restarts >= EPOCH_RESTARTS) throw new Error('nex: daemon epoch kept changing during the executions walk')
        restarts += 1
        items = []; pages = []; seenIds = new Set(); requested = new Set(); dropped = 0; cursor = ''
        epoch = undefined
        page = -1
        continue
      }
      epoch = p.pdx.epoch
    }
    for (const item of p.items) {
      if (seenIds.has(item.id)) continue
      seenIds.add(item.id)
      items.push(item)
    }
    dropped += p.dropped
    const ver = delta && p.pdx ? p.pdx.ver : 0
    const last = p.nextCursor === ''
    pages.push({ ver, upTo: last ? UP_TO_END : p.nextCursor })
    if (last) return { items, pages, epoch, dropped, truncated: false, stuck: false, stuckPage: null }
    if (requested.has(p.nextCursor)) return { items, pages, epoch, dropped, truncated: false, stuck: true, stuckPage: page + 1 }
    cursor = p.nextCursor
  }
  return { items, pages, epoch, dropped, truncated: true, stuck: false, stuckPage: null }
}
