// spa/src/lib/nex/stint-attribution.ts — conversation entity spec §10.3 / D20:
// which earlier worker stint wrote each prelude line, and the prelude's runs
// of lines with one attribution (a PreludeSegment each). Pure.
import type { PreludeItem } from './prelude-wire'
import type { Stint } from './entity-stints'

export type Attribution = ReadonlyMap<string /* pos */, string /* stint id */>

export const NO_ATTRIBUTION: Attribution = new Map()

/** A worker's entrypoint (`sdk-cli`, `sdk-ts`, …); the prelude labels these segments "Headless (worker)". */
export const isWorkerEntrypoint = (entrypoint: string): boolean => entrypoint.startsWith('sdk')

/**
 * Entrypoint of each item = the entrypoint of the nearest prelude.segment at or before it, in `items` order.
 * An item before any loaded marker is unknown and never attributed (D20).
 * A worker item (entrypoint starts with "sdk") with a non-null offset `o` goes to the stint with the largest boundary ≤ o.
 * Terminal ("cli") items, items with a null offset, and items below the first stint's boundary are not attributed.
 * Every item kind follows the same rule; a marker's own entrypoint is its own.
 */
export function attributeItems(items: readonly PreludeItem[], stints: readonly Stint[]): Attribution {
  const out = new Map<string, string>()
  if (stints.length === 0) return out
  let entrypoint: string | null = null
  for (const it of items) {
    if (it.kind === 'prelude.segment') entrypoint = it.entrypoint
    if (entrypoint === null || !isWorkerEntrypoint(entrypoint) || it.offset === null) continue
    const id = stintAt(stints, it.offset)
    if (id !== null) out.set(it.pos, id)
  }
  return out
}

/** The stint with the largest boundary ≤ `offset`; of equal boundaries the newer by createdAt, in any input order. */
function stintAt(stints: readonly Stint[], offset: number): string | null {
  let best: Stint | null = null
  for (const s of stints) {
    if (s.boundary > offset) continue
    if (best === null || s.boundary > best.boundary || (s.boundary === best.boundary && s.createdAt >= best.createdAt)) best = s
  }
  return best?.id ?? null
}

/** Units `[start, end)` drawn by one PreludeSegment; `stintId` null = a plain transcript segment. */
export interface PreludeRun { stintId: string | null; key: string; start: number; end: number }

/**
 * Maximal runs of consecutive units (room: entries; chat: blocks) with one
 * attribution. A unit's attribution is its first pos's (a chat span's first
 * message). A run is keyed `${stintId ?? 'plain'}:${lastPos}`, the last pos
 * its last unit covers: older pages only grow the front, so a run's key never
 * moves while it grows, and an unattributed prelude is one stable run.
 */
export function attributionRuns<T>(units: readonly T[], poses: (unit: T) => readonly [first: string, last: string], attribution: Attribution): PreludeRun[] {
  const ids = units.map((u) => attribution.get(poses(u)[0]) ?? null)
  const out: PreludeRun[] = []
  let start = 0
  for (let i = 0; i < units.length; i++) {
    if (i + 1 < units.length && ids[i + 1] === ids[i]) continue
    out.push({ stintId: ids[i], key: `${ids[i] ?? 'plain'}:${poses(units[i])[1]}`, start, end: i + 1 })
    start = i + 1
  }
  return out
}
