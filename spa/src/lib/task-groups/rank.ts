// spa/src/lib/task-groups/rank.ts — a tab's place in its group's bar (task groups spec §4.2): a fractional-index string
// over the base-62 digits `0-9A-Za-z` (their code order is their digit order, so a plain string compare orders ranks).
// A rank never ends in the zero digit, so there is always room before it. One module holds the comparator and both
// limits: the generator keeps ranks ≤ `RANK_MAX_GENERATED`, the payload check accepts up to `RANK_MAX_ACCEPTED`.

const DIGITS = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz'
const BASE = DIGITS.length
const ZERO = DIGITS[0]

/** The longest rank this device generates; past it the caller re-ranks the group with `rebalance`. */
export const RANK_MAX_GENERATED = 32
/** The longest rank a payload may carry (`isTabsPayload`). Above the generator limit, so another build's rank is never refused. */
export const RANK_MAX_ACCEPTED = 128

const RANK_SHAPE = /^[0-9A-Za-z]+$/

export function isValidRank(v: unknown): v is string {
  return typeof v === 'string' && v.length >= 1 && v.length <= RANK_MAX_ACCEPTED && RANK_SHAPE.test(v)
}

/** Plain code-unit order: the same on every device. */
export function compareRank(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0
}

/** A string strictly between `a` ('' = the lowest) and `b` (null = no upper bound); the caller has checked `a < b` and that `b` does not end in the zero digit. */
function midpoint(a: string, b: string | null): string {
  if (b !== null) {
    // the common prefix (with `a` padded by zero digits) is kept
    let n = 0
    while ((a[n] ?? ZERO) === b[n]) n += 1
    if (n > 0) return b.slice(0, n) + midpoint(a.slice(n), b.slice(n))
  }
  const digitA = a === '' ? 0 : DIGITS.indexOf(a[0])
  const digitB = b !== null ? DIGITS.indexOf(b[0]) : BASE
  if (digitB - digitA > 1) return DIGITS[Math.round((digitA + digitB) / 2)]
  if (b !== null && b.length > 1) return b.slice(0, 1)
  return DIGITS[digitA] + midpoint(a.slice(1), null)
}

/**
 * A rank between `a` (before) and `b` (after); either may be absent. `null` = the result would pass
 * `RANK_MAX_GENERATED`, or no string sorts between the neighbours (equal ranks — two workspaces that inserted at one spot —
 * or an upper neighbour ending in the zero digit, which a synced payload may carry): re-rank the group (`rebalance`) and
 * ask again. Never throws: ranks come from other devices.
 */
export function rankBetween(a?: string, b?: string): string | null {
  if (a !== undefined && b !== undefined && a >= b) return null
  if (b !== undefined && b.endsWith(ZERO)) return null
  const r = midpoint(a ?? '', b ?? null)
  return r.length > RANK_MAX_GENERATED ? null : r
}

/**
 * `ranks.length` fresh ranks in order, evenly spread, as short as the count allows and never ending in the zero digit.
 * Only the count matters: the caller writes them to its tabs in their shown order.
 */
export function rebalance(ranks: readonly unknown[]): string[] {
  const n = ranks.length
  if (n === 0) return []
  let len = 1
  while (BASE ** len < 2 * (n + 1)) len += 1
  const span = BASE ** len
  const out: string[] = []
  for (let i = 1; i <= n; i += 1) {
    let v = Math.floor((i * span) / (n + 1))
    if (v % BASE === 0) v += 1
    let s = ''
    for (let k = 0; k < len; k += 1) {
      s = DIGITS[v % BASE] + s
      v = Math.floor(v / BASE)
    }
    out.push(s)
  }
  return out
}

interface GroupTab {
  id: string
  createdAt: number
  groupRank?: string
}

/**
 * THE order of a group's tabs (spec §4.2): ranked tabs first, by rank then tab id; then unranked ones by `createdAt`
 * then tab id. `groupTabs` and so every reader sorts with this and nothing else.
 */
export function compareGroupTabs(a: GroupTab, b: GroupTab): number {
  const ar = a.groupRank !== undefined
  const br = b.groupRank !== undefined
  if (ar !== br) return ar ? -1 : 1
  if (ar && br) {
    const byRank = compareRank(a.groupRank!, b.groupRank!)
    if (byRank !== 0) return byRank
  } else if (a.createdAt !== b.createdAt) {
    return a.createdAt < b.createdAt ? -1 : 1
  }
  return compareRank(a.id, b.id)
}
