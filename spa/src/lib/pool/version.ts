// spa/src/lib/pool/version.ts — comparing wwd versions (`1.0.0-alpha.755`). Adjacent versions are compatible (plan WWD-7: a mismatch is
// shown, not an error), so this only says which hosts are behind the newest, for the overview's version column.

type Part = number | string

function parts(v: string): Part[] {
  return v.split(/[.-]/).filter((p) => p !== '').map((p) => (/^\d+$/.test(p) ? Number(p) : p))
}

/** > 0 when `a` is newer. Numbers compare as numbers; a release is newer than its own prerelease (`1.0.0` > `1.0.0-alpha.9`). */
export function compareWwdVersion(a: string, b: string): number {
  const pa = parts(a)
  const pb = parts(b)
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const x = pa[i]
    const y = pb[i]
    if (x === y) continue
    if (x === undefined) return typeof y === 'string' ? 1 : -1 // a has ended: a release beats its prerelease tail, a longer number tail wins
    if (y === undefined) return typeof x === 'string' ? -1 : 1
    if (typeof x === 'number' && typeof y === 'number') return x - y
    return String(x) < String(y) ? -1 : 1
  }
  return 0
}

/** The hosts (keys) whose version is behind the newest one; a host with no version is unknown, not behind. */
export function olderThanNewest(versions: Record<string, string>): Set<string> {
  const known = Object.entries(versions).filter(([, v]) => v !== '')
  if (known.length < 2) return new Set()
  const newest = known.reduce((best, [, v]) => (compareWwdVersion(v, best) > 0 ? v : best), known[0][1])
  return new Set(known.filter(([, v]) => compareWwdVersion(v, newest) < 0).map(([k]) => k))
}
