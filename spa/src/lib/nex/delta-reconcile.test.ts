import { describe, it, expect } from 'vitest'
import { findSuspects, statusDigest, digestDiff } from './delta-reconcile'
import { normalizeDelta, putOverlay, type Overlay } from './execution-overlay'
import { UP_TO_END } from './list-all-executions'
import type { ExecutionSummary } from './types'

const row = (id: string, extra: Partial<ExecutionSummary> = {}) => ({ id, state: 'idle', archived: false, ...extra }) as ExecutionSummary
const pages = [{ ver: 10, upTo: 'b', bseq: 3 }, { ver: 20, upTo: UP_TO_END, bseq: 7 }]
const cacheOf = (items: ExecutionSummary[], vers: Record<string, number>) => ({ items, rowVers: vers })

describe('statusDigest', () => {
  it('covers exactly the six status fields and ignores everything else', () => {
    const base = row('a', { state: 'running', turn_count: 1 })
    expect(statusDigest(row('a', { state: 'running', turn_count: 1, brief: 'x', updated_at: 5 }))).toBe(statusDigest(base))
    for (const over of [
      { state: 'idle' }, { turn_count: 2 }, { last_turn_reason: 'completed' }, { terminal_reason: 'x' }, { archived: true },
      { pending_permission: { request_id: 'r1', tool_name: 't', since: 1 } },
    ] as Partial<ExecutionSummary>[]) {
      expect(statusDigest(row('a', { state: 'running', turn_count: 1, ...over }))).not.toBe(statusDigest(base))
    }
    expect(statusDigest(null)).toBe('absent')
  })
  it('names the first differing field', () => {
    expect(digestDiff(row('a', { state: 'running' }), row('a', { state: 'idle' }))).toEqual({ field: 'state', cached: 'running', fetched: 'idle' })
    expect(digestDiff(null, row('a'))).toMatchObject({ field: 'presence', cached: 'absent' })
  })
})

describe('findSuspects', () => {
  it('V newer than the cached ver with a different digest is a suspect, carrying V, H and the list digest', () => {
    const s = findSuspects(cacheOf([row('a', { state: 'running' })], { a: 5 }), [row('a')], pages, null)
    expect(s).toEqual([{ id: 'a', V: 10, H: 3, listDigest: statusDigest(row('a')), field: 'state', cached: 'running', fetched: 'idle' }])
  })
  it('the same digest, or a cached ver at or past V, is not a suspect', () => {
    expect(findSuspects(cacheOf([row('a')], { a: 5 }), [row('a')], pages, null)).toEqual([])
    expect(findSuspects(cacheOf([row('a', { state: 'running' })], { a: 10 }), [row('a')], pages, null)).toEqual([])
    expect(findSuspects(cacheOf([row('a', { state: 'running' })], { a: 12 }), [row('a')], pages, null)).toEqual([])
  })
  it('a row on one side only is a suspect (fetched-only, and cached-only when the page is newer)', () => {
    const fetchedOnly = findSuspects(cacheOf([], {}), [row('c')], pages, null)
    expect(fetchedOnly).toMatchObject([{ id: 'c', V: 20, H: 7, field: 'presence', cached: 'absent' }])
    const cachedOnly = findSuspects(cacheOf([row('c')], { c: 5 }), [], pages, null)
    expect(cachedOnly).toMatchObject([{ id: 'c', V: 20, listDigest: 'absent', field: 'presence', fetched: 'absent' }])
    expect(findSuspects(cacheOf([row('c')], { c: 25 }), [], pages, null)).toEqual([]) // a delta newer than the page added it
  })
  it('a cached-only row beyond a truncated walk has no covering page and is ignored', () => {
    expect(findSuspects(cacheOf([row('z')], { z: 1 }), [], [{ ver: 10, upTo: 'b', bseq: 3 }], null)).toEqual([])
  })
  it('an id whose overlay entry is newer than its page is already explained', () => {
    const o: Overlay = new Map()
    putOverlay(o, 'a', normalizeDelta(15, row('a', { state: 'running' })))
    expect(findSuspects(cacheOf([row('a', { state: 'running' })], { a: 5 }), [row('a')], pages, o)).toEqual([])
  })
})
