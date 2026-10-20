import { describe, expect, it } from 'vitest'
import { RANK_MAX_ACCEPTED, RANK_MAX_GENERATED, compareGroupTabs, compareRank, isValidRank, rankBetween, rebalance } from './rank'

describe('rankBetween', () => {
  it('makes a first rank with no neighbours', () => {
    const r = rankBetween()
    expect(r).not.toBeNull()
    expect(isValidRank(r!)).toBe(true)
  })

  it('goes after the last and before the first', () => {
    const first = rankBetween()!
    const after = rankBetween(first)!
    const before = rankBetween(undefined, first)!
    expect(compareRank(first, after)).toBeLessThan(0)
    expect(compareRank(before, first)).toBeLessThan(0)
  })

  it('goes strictly between any two neighbours, however close', () => {
    let a = rankBetween()!
    let b = rankBetween(a)!
    for (let i = 0; i < 60; i += 1) {
      const m = rankBetween(a, b)
      if (m === null) break
      expect(compareRank(a, m)).toBeLessThan(0)
      expect(compareRank(m, b)).toBeLessThan(0)
      expect(isValidRank(m)).toBe(true)
      if (i % 2 === 0) a = m
      else b = m
    }
  })

  it('never ends in the zero digit, so there is always room before it', () => {
    let b: string | undefined = rankBetween()!
    for (let i = 0; i < 40; i += 1) {
      const r = rankBetween(undefined, b)
      if (r === null) break
      expect(r.endsWith('0')).toBe(false)
      b = r
    }
  })

  it('answers null (re-rank) for neighbours that cannot be ordered — equal ranks (two workspaces inserting at one spot) included — and never throws', () => {
    const a = rankBetween()!
    const b = rankBetween(a)!
    expect(rankBetween(b, a)).toBeNull()
    expect(rankBetween(a, a)).toBeNull()
  })

  it('copes with a synced rank that ends in the zero digit: as the lower neighbour it works, as the upper one it asks for a re-rank', () => {
    const below = rankBetween('A0', 'A1')
    expect(below).not.toBeNull()
    expect(compareRank('A0', below!)).toBeLessThan(0)
    expect(compareRank(below!, 'A1')).toBeLessThan(0)
    const after = rankBetween('A0')!
    expect(compareRank('A0', after)).toBeLessThan(0)
    expect(rankBetween('A', 'A0')).toBeNull() // nothing sorts between them
    expect(() => rankBetween(undefined, 'A0')).not.toThrow()
    const fresh = rebalance(['A', 'A0', 'A00'])
    expect(fresh).toHaveLength(3)
  })

  it('repeated inserts at one spot hit the generator cap (null), and rebalance gives room again', () => {
    let first: string = rankBetween()!
    let hit = false
    const seen: string[] = [first]
    for (let i = 0; i < 2000; i += 1) {
      const r = rankBetween(undefined, first)
      if (r === null) {
        hit = true
        break
      }
      expect(r.length).toBeLessThanOrEqual(RANK_MAX_GENERATED)
      seen.unshift(r)
      first = r
    }
    expect(hit).toBe(true)
    const fresh = rebalance(seen)
    expect(fresh).toHaveLength(seen.length)
    for (let i = 1; i < fresh.length; i += 1) expect(compareRank(fresh[i - 1], fresh[i])).toBeLessThan(0)
    for (const r of fresh) {
      expect(isValidRank(r)).toBe(true)
      expect(r.length).toBeLessThan(RANK_MAX_GENERATED)
    }
    expect(rankBetween(undefined, fresh[0])).not.toBeNull()
    expect(rankBetween(fresh[fresh.length - 1])).not.toBeNull()
  })
})

describe('rankBetween — any pair of syncable ranks', () => {
  it('is strictly between the neighbours or null, never throws, whatever the (even zero-ending) strings', () => {
    let seed = 12345
    const rnd = (n: number) => {
      seed = (Math.imul(seed, 1103515245) + 12345) >>> 0
      return seed % n
    }
    const ALPHA = '0019AZaz'
    const word = () => Array.from({ length: 1 + rnd(5) }, () => ALPHA[rnd(ALPHA.length)]).join('')
    for (let i = 0; i < 3000; i += 1) {
      const a = rnd(5) === 0 ? undefined : word()
      const b = rnd(5) === 0 ? undefined : word()
      const r = rankBetween(a, b)
      if (r === null) continue
      expect(isValidRank(r)).toBe(true)
      if (a !== undefined) expect(compareRank(a, r), `${a} < ${r}`).toBeLessThan(0)
      if (b !== undefined) expect(compareRank(r, b), `${r} < ${b}`).toBeLessThan(0)
    }
  })
})

describe('limits', () => {
  it('the check limit is above the generator limit', () => {
    expect(RANK_MAX_ACCEPTED).toBe(128)
    expect(RANK_MAX_GENERATED).toBe(32)
    expect(RANK_MAX_ACCEPTED).toBeGreaterThan(RANK_MAX_GENERATED)
  })

  it('isValidRank checks type, alphabet and length', () => {
    expect(isValidRank('V')).toBe(true)
    expect(isValidRank('a'.repeat(RANK_MAX_ACCEPTED))).toBe(true)
    expect(isValidRank('a'.repeat(RANK_MAX_ACCEPTED + 1))).toBe(false)
    expect(isValidRank('')).toBe(false)
    expect(isValidRank('a-b')).toBe(false)
    expect(isValidRank(5)).toBe(false)
    expect(isValidRank(undefined)).toBe(false)
  })
})

describe('rebalance', () => {
  it('spreads any count in order, with ranks that end in a non-zero digit', () => {
    for (const n of [0, 1, 2, 5, 61, 62, 200, 3000]) {
      const r = rebalance(Array.from({ length: n }, () => 'x'))
      expect(r).toHaveLength(n)
      for (let i = 0; i < r.length; i += 1) {
        expect(isValidRank(r[i])).toBe(true)
        expect(r[i].endsWith('0')).toBe(false)
        if (i > 0) expect(compareRank(r[i - 1], r[i])).toBeLessThan(0)
      }
    }
  })
})

describe('compareGroupTabs', () => {
  const t = (id: string, createdAt: number, groupRank?: string) => ({ id, createdAt, groupRank })

  it('ranked tabs come first, by rank', () => {
    const list = [t('c', 1, 'b'), t('a', 1, 'c'), t('b', 1, 'a')]
    expect(list.sort(compareGroupTabs).map((x) => x.id)).toEqual(['b', 'c', 'a'])
  })

  it('equal ranks order by tab id', () => {
    const list = [t('z', 1, 'm'), t('a', 9, 'm'), t('m', 5, 'm')]
    expect(list.sort(compareGroupTabs).map((x) => x.id)).toEqual(['a', 'm', 'z'])
  })

  it('unranked tabs follow the ranked ones, by createdAt then tab id', () => {
    const list = [t('u3', 30), t('r', 99, 'a'), t('u1', 10), t('u2b', 20), t('u2a', 20)]
    expect(list.sort(compareGroupTabs).map((x) => x.id)).toEqual(['r', 'u1', 'u2a', 'u2b', 'u3'])
  })

  it('is a consistent total order whatever the input order', () => {
    const base = [t('a', 5, 'k'), t('b', 1), t('c', 1), t('d', 3, 'a'), t('e', 3, 'k')]
    const want = [...base].sort(compareGroupTabs).map((x) => x.id)
    for (let i = 0; i < 10; i += 1) {
      const shuffled = [...base].sort(() => Math.random() - 0.5)
      expect(shuffled.sort(compareGroupTabs).map((x) => x.id)).toEqual(want)
    }
  })
})
