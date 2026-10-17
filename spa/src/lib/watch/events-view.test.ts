import { describe, it, expect } from 'vitest'
import { applyFilters, clock, dayKeyOf, DEFAULT_FILTERS, groupByDay, mergeRows, shortDuration } from './events-view'
import type { EventsEntry } from './events-store'
import type { WatchEvent } from './events-types'

const ev = (id: string, at: string, over: Partial<WatchEvent> = {}): WatchEvent => ({ id, host: 'm', kind: 'unmanaged_heavy', at, status: 'pending', ...over })
const ok = (...events: WatchEvent[]): EventsEntry => ({ kind: 'ok', events, at: 1 })
const local = (y: number, mo: number, d: number, h = 12, mi = 0): string => new Date(y, mo - 1, d, h, mi).toISOString()

describe('mergeRows', () => {
  it('merges every host newest first, and leaves out a host that did not answer', () => {
    const rows = mergeRows({
      a: ok(ev('a1', local(2026, 10, 17, 9)), ev('a2', local(2026, 10, 17, 7))),
      b: ok(ev('b1', local(2026, 10, 17, 8))),
      c: { kind: 'error', at: 1 },
    }, ['a', 'b', 'c'])
    expect(rows.map((r) => r.event.id)).toEqual(['a1', 'b1', 'a2'])
    expect(rows.map((r) => r.hostId)).toEqual(['a', 'b', 'a'])
  })
  it('equal times keep the host order given', () => {
    const t = local(2026, 10, 17, 9)
    expect(mergeRows({ a: ok(ev('x', t)), b: ok(ev('y', t)) }, ['b', 'a']).map((r) => r.event.id)).toEqual(['y', 'x'])
  })
})

describe('applyFilters', () => {
  const rows = mergeRows({
    a: ok(ev('1', local(2026, 10, 17, 9), { kind: 'pressure_high' }), ev('2', local(2026, 10, 17, 8), { status: 'handled' })),
    b: ok(ev('3', local(2026, 10, 17, 7), { kind: 'slot_long' })),
  }, ['a', 'b'])
  it('the default shows everything', () => expect(applyFilters(rows, DEFAULT_FILTERS)).toHaveLength(3))
  it('each chip row narrows on its own, and together they intersect', () => {
    expect(applyFilters(rows, { ...DEFAULT_FILTERS, host: 'b' }).map((r) => r.event.id)).toEqual(['3'])
    expect(applyFilters(rows, { ...DEFAULT_FILTERS, kind: 'pressure_high' }).map((r) => r.event.id)).toEqual(['1'])
    expect(applyFilters(rows, { ...DEFAULT_FILTERS, status: 'handled' }).map((r) => r.event.id)).toEqual(['2'])
    expect(applyFilters(rows, { host: 'a', kind: 'unmanaged_heavy', status: 'pending' })).toEqual([])
    expect(applyFilters(rows, { host: 'a', kind: 'all', status: 'pending' }).map((r) => r.event.id)).toEqual(['1'])
  })
})

describe('days', () => {
  const now = new Date(2026, 9, 17, 10, 30).getTime()
  it('today, yesterday, then the date', () => {
    expect(dayKeyOf(new Date(2026, 9, 17, 0, 5).getTime(), now)).toBe('today')
    expect(dayKeyOf(new Date(2026, 9, 16, 23, 59).getTime(), now)).toBe('yesterday')
    expect(dayKeyOf(new Date(2026, 9, 15, 12).getTime(), now)).toBe('2026-10-15')
    expect(dayKeyOf(new Date(2026, 0, 3, 12).getTime(), now)).toBe('2026-01-03')
  })
  it('groups consecutive rows of one day', () => {
    const rows = mergeRows({ a: ok(ev('1', local(2026, 10, 17, 9)), ev('2', local(2026, 10, 17, 8)), ev('3', local(2026, 10, 16, 20)), ev('4', local(2026, 10, 10, 20))) }, ['a'])
    const g = groupByDay(rows, now)
    expect(g.map((x) => [x.day, x.rows.length])).toEqual([['today', 2], ['yesterday', 1], ['2026-10-10', 1]])
  })
  it('formats the clock and the duration', () => {
    expect(clock(local(2026, 10, 17, 9, 5))).toBe('09:05')
    expect([shortDuration(42), shortDuration(185), shortDuration(3720)]).toEqual(['42s', '3m05s', '1h02m'])
  })
})
