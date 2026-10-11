// spa/src/lib/nex/transcript-search.ts — the matching core of transcript search: a *unit* is one searchable piece of text with the id of the DOM
// element that will hold it (`data-search-unit`) and the fold keys to open for it to be on screen (`reveal`); `findMatches` finds a query in a
// list of units (NFC, case-insensitive, literal, CJK-aware minimum length), keeps the matches around the reader when there are too many, and
// the anchor / identity helpers keep the current match steady while the list changes underneath it.
// The index that built the units from the room and chat transcripts went with those transcripts (U4-5c); the shared search over the deck's own
// conversation items (#2665) builds its units for this core.
// Pure: no React, no store.

export interface SearchUnit {
  id: string
  text: string
  /** Fold keys to expand, outermost first, for `text` to be rendered under `[data-search-unit=id]`. */
  reveal: string[]
}

export interface SearchMatch {
  unitId: string
  /** `[start, end)` in the unit's `text`. */
  start: number
  end: number
  /** Which occurrence of the unit this is (0-based) — also in a list that starts mid-unit. */
  ordinal: number
  reveal: string[]
}

/** The shortest query, in code points, that searches (A10)… */
export const SEARCH_MIN_CHARS = 2
/** …unless it holds a CJK character: one Han character (`錯`, `檔`) already narrows a transcript. */
export const SEARCH_MIN_CHARS_CJK = 1

// Bopomofo (注音) is Taiwan's; the long-vowel mark ー (U+30FC) and its half
// width ｰ (U+FF70) are Script=Common, so they are named on their own (A F8).
const CJK = /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}\p{Script=Bopomofo}ーｰ]/u

/**
 * The query as it is searched: NFC, trimmed. null when that leaves nothing,
 * or fewer code points than the minimum — 1 when it holds a CJK character,
 * else 2 (a single Latin letter matches nearly every line).
 */
export function normalizeQuery(query: string): string | null {
  const q = query.normalize('NFC').trim()
  if (!q) return null
  const min = CJK.test(q) ? SEARCH_MIN_CHARS_CJK : SEARCH_MIN_CHARS
  return [...q].length < min ? null : q
}

/**
 * The query (normalised by `normalizeQuery`) as a case-insensitive,
 * **literal** pattern: every regex metacharacter is escaped, so `a.b` does
 * not match `axb`. A RegExp rather than `toLowerCase` + `indexOf` because
 * lower-casing can change a string's length (`İ` → `i̇`), which would shift
 * every offset after it. null when the query does not search.
 */
export function searchPattern(query: string): RegExp | null {
  const q = normalizeQuery(query)
  if (q === null) return null
  return new RegExp(q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'giu')
}

/** The most matches `findMatches` returns by default; past it the UI shows `10000+`. */
export const SEARCH_MATCH_LIMIT = 10_000

export interface SearchResult {
  matches: SearchMatch[]
  /** More occurrences exist than `matches` holds (the limit was reached). */
  truncated: boolean
  /** Some of them lie before `matches[0]`… */
  truncatedBefore: boolean
  /** …some after the last of `matches`. */
  truncatedAfter: boolean
}

/** Every occurrence in one unit, in order. */
function unitMatches(unit: SearchUnit, pattern: RegExp): SearchMatch[] {
  pattern.lastIndex = 0
  const out: SearchMatch[] = []
  for (const m of unit.text.matchAll(pattern)) {
    out.push({ unitId: unit.id, start: m.index, end: m.index + m[0].length, ordinal: out.length, reveal: unit.reveal })
  }
  return out
}

/**
 * Every occurrence of `query` in `units`, in unit order then position;
 * non-overlapping. At most `limit` — a two-letter query over a long session
 * can occur hundreds of thousands of times, and nobody steps through those
 * one by one.
 *
 * Past the limit the matches kept are those around `anchor`, a unit position
 * (user decision 2026-09-27: the ones near what is on screen, not the
 * oldest — so the newest content stays reachable): up to half the limit
 * before it, the rest from it on; a side that has fewer gives its share to
 * the other. `anchor` 0 keeps the first `limit`; `units.length`, the last.
 * `anchorOrdinal` puts the anchor on that match of its unit: the unit's
 * earlier matches are before it — a single unit can hold more than `limit`,
 * and a window centred on its start could never move past them (#1495
 * re-review item 3). Each side scans at most `limit + 1` matches beyond the
 * anchor unit.
 */
export function findMatches(
  units: readonly SearchUnit[], query: string, limit = SEARCH_MATCH_LIMIT, anchor = 0, anchorOrdinal = 0,
): SearchResult {
  const pattern = searchPattern(query)
  if (!pattern) return { matches: [], truncated: false, truncatedBefore: false, truncatedAfter: false }
  const at = Math.max(0, Math.min(anchor, units.length))
  const after: SearchMatch[] = []
  // Nearest first: the anchor unit's matches before its ordinal, then the
  // unit before it, each unit's last match first.
  const before: SearchMatch[] = []
  if (at < units.length) {
    // (Pushed one by one: spreading a unit's 10^5 matches overflows the stack.)
    const ms = unitMatches(units[at], pattern)
    const split = Math.max(0, Math.min(anchorOrdinal, ms.length))
    for (let k = split - 1; k >= 0; k--) before.push(ms[k])
    for (let k = split; k < ms.length; k++) after.push(ms[k])
  }
  for (let u = at + 1; u < units.length && after.length <= limit; u++) {
    for (const m of unitMatches(units[u], pattern)) after.push(m)
  }
  for (let u = at - 1; u >= 0 && before.length <= limit; u--) {
    const ms = unitMatches(units[u], pattern)
    for (let k = ms.length - 1; k >= 0; k--) before.push(ms[k])
  }
  const b = Math.min(before.length, Math.max(Math.floor(limit / 2), limit - after.length))
  const a = Math.min(after.length, limit - b)
  const truncatedBefore = before.length > b
  const truncatedAfter = after.length > a
  return {
    matches: [...before.slice(0, b).reverse(), ...after.slice(0, a)],
    truncated: truncatedBefore || truncatedAfter,
    truncatedBefore,
    truncatedAfter,
  }
}

/**
 * A unit position that survives a recompute, like MatchIdentity: the unit's
 * id when there is one (units landing before it shift its position), else a
 * bare position — `ANCHOR_START`, `ANCHOR_END`. `ordinal` narrows it to
 * that match of its unit (findMatches' `anchorOrdinal`); it holds only while
 * the unit itself is there — re-seated on a successor, it is that unit's start.
 */
export interface UnitAnchor {
  unitId: string | null
  unitPos: number
  ordinal?: number
}
export const ANCHOR_START: UnitAnchor = { unitId: null, unitPos: 0 }
export const ANCHOR_END: UnitAnchor = { unitId: null, unitPos: Number.MAX_SAFE_INTEGER }

/** Where `anchor` is in `units` now: its unit's position, else its stored one (clamped). */
export function anchorPosition(units: readonly SearchUnit[], anchor: UnitAnchor): number {
  const at = anchor.unitId === null ? undefined : unitPositions(units).get(anchor.unitId)
  return Math.min(at ?? anchor.unitPos, units.length)
}

/** The match of its unit `anchor` sits on: its `ordinal` while that unit is in `units`, else 0. */
export function anchorOrdinal(units: readonly SearchUnit[], anchor: UnitAnchor): number {
  if (!anchor.ordinal || anchor.unitId === null) return 0
  return unitPositions(units).has(anchor.unitId) ? anchor.ordinal : 0
}

/** The anchor at the unit `id`; the end when it is not a unit. */
export function unitAnchor(units: readonly SearchUnit[], id: string | null): UnitAnchor {
  const at = id === null ? undefined : unitPositions(units).get(id)
  return at === undefined ? ANCHOR_END : { unitId: id, unitPos: at }
}

/**
 * `a` (a MatchIdentity or a UnitAnchor) re-seated from `prev` onto `next`:
 * its unit's new position; when `next` lacks that unit, the position of the
 * first unit after it in `prev` that `next` has (else the end) — when `prev`
 * lacked it too, its stored position already names that successor — so the
 * match after it takes over, even across room ⇄ chat, whose unit lists
 * differ. Returns `a` itself when nothing moved.
 */
export function relocate<T extends { unitId: string | null; unitPos: number }>(
  prev: readonly SearchUnit[], next: readonly SearchUnit[], a: T,
): T {
  if (a.unitId === null) return a
  const nextPos = unitPositions(next)
  let unitPos = nextPos.get(a.unitId)
  if (unitPos === undefined) {
    // Gone from `prev` already: `unitPos` is its successor's place there.
    const was = unitPositions(prev).get(a.unitId)
    const from = was === undefined ? a.unitPos : was + 1
    unitPos = next.length
    for (let k = from; k < prev.length; k++) {
      const at = nextPos.get(prev[k].id)
      if (at !== undefined) {
        unitPos = at
        break
      }
    }
  }
  return unitPos === a.unitPos ? a : { ...a, unitPos }
}

/** The first match in the unit at position `anchor` or after it; -1 when none. */
export function firstAtOrAfter(units: readonly SearchUnit[], matches: readonly SearchMatch[], anchor: number): number {
  const pos = unitPositions(units)
  return matches.findIndex((m) => (pos.get(m.unitId) ?? -1) >= anchor)
}

/**
 * Which match is current, in terms that survive a recompute (A5): its unit,
 * its ordinal among that unit's matches, and the unit's position (to find
 * what follows it once it is gone). A list index would not do — a match
 * landing before it (a chat tools line gaining an operation) shifts it.
 */
export interface MatchIdentity {
  unitId: string
  ordinal: number
  unitPos: number
}

function unitPositions(units: readonly SearchUnit[]): Map<string, number> {
  const pos = new Map<string, number>()
  units.forEach((u, i) => { if (!pos.has(u.id)) pos.set(u.id, i) })
  return pos
}

/** The identity of `matches[i]`; null when there is no such match. */
export function matchIdentity(units: readonly SearchUnit[], matches: readonly SearchMatch[], i: number): MatchIdentity | null {
  const match = matches[i]
  if (!match) return null
  return { unitId: match.unitId, ordinal: match.ordinal, unitPos: unitPositions(units).get(match.unitId) ?? -1 }
}

/**
 * The index of the match `identity` names in a recomputed list: the same
 * match when it still exists, else the nearest one after where it was, else
 * the last. No identity → the first; no matches → -1.
 */
export function findCurrent(units: readonly SearchUnit[], matches: readonly SearchMatch[], identity: MatchIdentity | null): number {
  if (matches.length === 0) return -1
  if (!identity) return 0
  const pos = unitPositions(units)
  // Where its unit is now, when it is still there (units may have landed before it).
  const at = pos.get(identity.unitId) ?? identity.unitPos
  for (let i = 0; i < matches.length; i++) {
    const m = matches[i]
    const p = pos.get(m.unitId) ?? -1
    // `>=`: when its unit is gone, `at` is where it was, and the unit that
    // moved up into that place is the one that followed it (R1-4).
    if (m.unitId === identity.unitId ? m.ordinal >= identity.ordinal : p >= at) return i
  }
  return matches.length - 1
}
