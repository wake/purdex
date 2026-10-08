// spa/src/lib/nex/execution-overlay.ts — the pure half of #1866 §4.3. While a list walk is in flight, deltas keep
// arriving; they are held here, keyed by execution id, and reconciled against the walk's pages at commit so a row
// the walk read BEFORE a change is not allowed to undo that change (and a removed row is not resurrected).
import { UP_TO_END, type WalkPage } from './list-all-executions'
import type { ExecutionSummary } from './types'

/** A delta as stored: an upsert `{ver, row}` or a tombstone `{ver, null}`. */
/** `bseq` is the broadcast number of the delta that made the entry; the safety reconcile uses it as evidence (§8 R3-1). */
export interface OverlayEntry { ver: number; row: ExecutionSummary | null; bseq?: number }
export type Overlay = Map<string, OverlayEntry>

/**
 * The one normalization: the store holds non-archived rows only, so a delta whose row is `null` or has
 * `archived: true` is a tombstone. An archived upsert can therefore never reach the overlay as a row.
 */
export function normalizeDelta(ver: number, row: ExecutionSummary | null, bseq?: number): OverlayEntry {
  return { ver, row: row === null || row.archived === true ? null : row, ...(bseq !== undefined ? { bseq } : {}) }
}

/** A later (higher ver) entry replaces an earlier one for the same id. */
export function putOverlay(overlay: Overlay, id: string, entry: OverlayEntry): void {
  const cur = overlay.get(id)
  if (!cur || entry.ver > cur.ver) overlay.set(id, entry)
}

/** The first page with `id <= upTo` (ids compare as plain strings); undefined beyond a truncated walk. */
export function coveringPage(pages: readonly WalkPage[], id: string): WalkPage | undefined {
  return pages.find((p) => p.upTo === UP_TO_END || id <= p.upTo)
}

/**
 * Commit a walk: the walk's rows keyed by their covering page's ver, then every overlay entry whose ver is newer
 * than its covering page (always, beyond a truncated walk) applied on top — an upsert adds or replaces, a
 * tombstone removes. Rows stay in id order, which is the order Nexen pages in.
 */
export function commitWalk(
  rows: readonly ExecutionSummary[],
  pages: readonly WalkPage[],
  overlay: Overlay,
): { items: ExecutionSummary[]; vers: Record<string, number> } {
  const byId = new Map<string, ExecutionSummary>()
  const vers: Record<string, number> = {}
  for (const r of rows) {
    byId.set(r.id, r)
    vers[r.id] = coveringPage(pages, r.id)?.ver ?? 0
  }
  let inserted = false
  for (const [id, entry] of overlay) {
    const cover = coveringPage(pages, id)
    if (cover && entry.ver <= cover.ver) continue
    if (entry.row === null) {
      byId.delete(id)
      delete vers[id]
    } else {
      if (!byId.has(id)) inserted = true
      byId.set(id, entry.row)
      vers[id] = entry.ver
    }
  }
  const items = [...byId.values()]
  if (inserted) items.sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
  return { items, vers }
}
