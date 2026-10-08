// spa/src/lib/nex/delta-reconcile.ts — the pure half of the SPA safety reconcile (#1866 §4.5, §8 R3-1). A walk is
// compared with the cache BEFORE it is committed; the differences that a delta should already have delivered are
// "suspects", judged later by the effects once the delta stream has caught up with the page.
import { coveringPage, type Overlay } from './execution-overlay'
import type { WalkPage } from './list-all-executions'
import type { ExecutionSummary } from './types'

/** The daemon's status digest (§3.7): the fields whose change must have produced a delta. */
function digestParts(row: ExecutionSummary): Record<string, unknown> {
  return {
    state: row.state,
    pending_permission: row.pending_permission?.request_id ?? null,
    archived: row.archived,
    turn_count: row.turn_count ?? null,
    last_turn_reason: row.last_turn_reason ?? null,
    terminal_reason: row.terminal_reason ?? null,
  }
}

/** A row's digest; `null` (no row on that side) is its own value. */
export function statusDigest(row: ExecutionSummary | null): string {
  return row ? JSON.stringify(digestParts(row)) : 'absent'
}

export interface DigestDiff { field: string; cached: unknown; fetched: unknown }

/** The first differing field of two rows (`presence` when one side has no row), or null when the digests are equal. */
export function digestDiff(cached: ExecutionSummary | null, fetched: ExecutionSummary | null): DigestDiff | null {
  if (cached === null || fetched === null) {
    return cached === fetched ? null : { field: 'presence', cached: cached ? 'present' : 'absent', fetched: fetched ? 'present' : 'absent' }
  }
  const a = digestParts(cached)
  const b = digestParts(fetched)
  for (const field of Object.keys(a)) if (a[field] !== b[field]) return { field, cached: a[field], fetched: b[field] }
  return null
}

/**
 * A difference the cache should not have had: `V` is the page's `ver`, `H` the broadcast high-water mark when the page
 * was read (§8 R3-1), `listDigest` what the page said about the row.
 */
export interface Suspect extends DigestDiff { id: string; V: number; H: number; listDigest: string }

/**
 * Suspects of a versioned walk against the cache. A row is a suspect when the page is newer than the cached row
 * (`V > cached ver`) and the digests differ, or when it is on one side only (a cached-only row counts only when the
 * page is newer than it, otherwise a newer delta put it there). An id whose overlay entry is newer than its page is
 * explained by that delta and skipped.
 */
export function findSuspects(
  cache: { items: readonly ExecutionSummary[]; rowVers?: Record<string, number> },
  fetched: readonly ExecutionSummary[],
  pages: readonly WalkPage[],
  overlay: Overlay | null,
): Suspect[] {
  const vers = cache.rowVers ?? {}
  const cachedById = new Map(cache.items.map((r) => [r.id, r]))
  const fetchedIds = new Set<string>()
  const out: Suspect[] = []
  const consider = (id: string, c: ExecutionSummary | null, f: ExecutionSummary | null, page: WalkPage) => {
    const newer = overlay?.get(id)
    if (newer && newer.ver > page.ver) return
    // A delta seen during the walk that was enqueued before the page was read and carries the very state the page
    // lists is that state in flight (§8 R3-1), even though it was not applied (its ver is not newer than the page).
    if (newer && newer.bseq !== undefined && newer.bseq <= (page.bseq ?? 0) && statusDigest(newer.row) === statusDigest(f)) return
    if (page.ver <= (vers[id] ?? 0) && c) return
    const diff = digestDiff(c, f)
    if (diff) out.push({ id, V: page.ver, H: page.bseq ?? 0, listDigest: statusDigest(f), ...diff })
  }
  for (const f of fetched) {
    fetchedIds.add(f.id)
    const page = coveringPage(pages, f.id)
    if (page) consider(f.id, cachedById.get(f.id) ?? null, f, page)
  }
  for (const c of cache.items) {
    if (fetchedIds.has(c.id)) continue
    const page = coveringPage(pages, c.id)
    if (page) consider(c.id, c, null, page)
  }
  return out
}
