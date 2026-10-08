import { describe, it, expect } from 'vitest'
import { commitWalk, coveringPage, normalizeDelta, putOverlay, type Overlay } from './execution-overlay'
import { UP_TO_END } from './list-all-executions'
import type { ExecutionSummary } from './types'

const row = (id: string, extra: Partial<ExecutionSummary> = {}) => ({ id, state: 'idle', archived: false, ...extra }) as ExecutionSummary
const ids = (r: { items: ExecutionSummary[] }) => r.items.map((i) => i.id)
const overlay = (entries: Record<string, [number, ExecutionSummary | null]>): Overlay => {
  const o: Overlay = new Map()
  for (const [id, [ver, r]] of Object.entries(entries)) putOverlay(o, id, normalizeDelta(ver, r))
  return o
}
const pages = [{ ver: 10, upTo: 'b' }, { ver: 20, upTo: UP_TO_END }]

describe('normalizeDelta', () => {
  it('a null row and an archived row are tombstones, anything else an upsert', () => {
    expect(normalizeDelta(3, null)).toEqual({ ver: 3, row: null })
    expect(normalizeDelta(3, row('a', { archived: true }))).toEqual({ ver: 3, row: null })
    const r = row('a')
    expect(normalizeDelta(3, r)).toEqual({ ver: 3, row: r })
  })
})

describe('putOverlay', () => {
  it('a higher ver replaces, a lower or equal one does not', () => {
    const o: Overlay = new Map()
    putOverlay(o, 'a', normalizeDelta(5, row('a', { brief: 'new' })))
    putOverlay(o, 'a', normalizeDelta(4, null))
    expect(o.get('a')!.ver).toBe(5)
    putOverlay(o, 'a', normalizeDelta(6, null))
    expect(o.get('a')).toEqual({ ver: 6, row: null })
  })
})

describe('coveringPage', () => {
  it('an id equal to upTo belongs to that page; the final page takes everything after', () => {
    expect(coveringPage(pages, 'b')).toBe(pages[0])
    expect(coveringPage(pages, 'a')).toBe(pages[0])
    expect(coveringPage(pages, 'c')).toBe(pages[1])
  })
  it('a truncated walk has no page for ids beyond the last upTo', () => {
    expect(coveringPage([{ ver: 1, upTo: 'b' }], 'z')).toBeUndefined()
  })
})

describe('commitWalk', () => {
  const walk = [row('a'), row('b'), row('c')]

  it('keys each row with its covering page ver and changes nothing without an overlay', () => {
    const r = commitWalk(walk, pages, new Map())
    expect(ids(r)).toEqual(['a', 'b', 'c'])
    expect(r.vers).toEqual({ a: 10, b: 10, c: 20 })
  })

  it('an overlay upsert newer than its page wins; an older or equal one is overwritten by the walk', () => {
    const newer = row('a', { brief: 'delta' })
    const r = commitWalk(walk, pages, overlay({ a: [11, newer], c: [20, row('c', { brief: 'equal' })], b: [9, row('b', { brief: 'old' })] }))
    expect(r.items.find((i) => i.id === 'a')!.brief).toBe('delta')
    expect(r.vers.a).toBe(11)
    expect(r.items.find((i) => i.id === 'c')!.brief).toBeUndefined()
    expect(r.items.find((i) => i.id === 'b')!.brief).toBeUndefined()
  })

  it('a tombstone beats an older list row and is ignored against a newer one', () => {
    const r = commitWalk(walk, pages, overlay({ a: [11, null], c: [19, null] }))
    expect(ids(r)).toEqual(['b', 'c'])
    expect(r.vers.a).toBeUndefined()
  })

  it('an archived delta in the overlay becomes a tombstone and the commit removes that id', () => {
    expect(ids(commitWalk(walk, pages, overlay({ b: [15, row('b', { archived: true })] })))).toEqual(['a', 'c'])
  })

  it('a ver between its covering page and the next page wins only against its covering page', () => {
    // b is covered by page 0 (ver 10); a delta at 15 beats it. c is covered by page 1 (ver 20); the same delta loses.
    const r = commitWalk(walk, pages, overlay({ b: [15, null], c: [15, null] }))
    expect(ids(r)).toEqual(['a', 'c'])
  })

  it('a row that appears in an already-walked range (unarchive, create) is present after the commit', () => {
    const r = commitWalk(walk, pages, overlay({ aa: [11, row('aa')] }))
    expect(ids(r)).toEqual(['a', 'aa', 'b', 'c'])
    expect(r.vers.aa).toBe(11)
  })

  it('an empty final page still covers ids beyond the previous upTo', () => {
    const p = [{ ver: 10, upTo: 'b' }, { ver: 20, upTo: UP_TO_END }]
    const r = commitWalk([row('a'), row('b')], p, overlay({ d: [15, row('d')], e: [25, row('e')] }))
    expect(ids(r)).toEqual(['a', 'b', 'e'])
  })

  it('a truncated walk lets the overlay win beyond its last page', () => {
    const r = commitWalk([row('a'), row('b')], [{ ver: 10, upTo: 'b' }], overlay({ z: [1, row('z')] }))
    expect(ids(r)).toEqual(['a', 'b', 'z'])
  })

  it('does not resurrect a removed row: the stale upsert is outranked by its later tombstone', () => {
    const o = overlay({ a: [11, row('a', { brief: 'stale' })] })
    putOverlay(o, 'a', normalizeDelta(12, null))
    expect(ids(commitWalk(walk, pages, o))).toEqual(['b', 'c'])
  })

  it('an unversioned walk (ver 0) lets any delta win', () => {
    const r = commitWalk(walk, [{ ver: 0, upTo: UP_TO_END }], overlay({ b: [1, null] }))
    expect(ids(r)).toEqual(['a', 'c'])
  })
})
