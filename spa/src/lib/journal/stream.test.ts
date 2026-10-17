// spa/src/lib/journal/stream.test.ts — local-time labels and the day dividers of the stream (J-2c). Times are built from local
// calendar parts, so the test holds in any time zone.
import { describe, it, expect } from 'vitest'
import { dayKey, timeOf, withDays } from './stream'
import type { JournalEntry } from './types'

const at = (y: number, mo: number, d: number, h: number, mi: number) => new Date(y, mo - 1, d, h, mi).getTime()
const e = (id: string, t: number): JournalEntry => ({ id, kind: 'exec', by: '', at: t, actor: 'A', text: '', refs: [], items: [], detail: '' })

describe('stream labels', () => {
  it('local date and time, zero padded', () => {
    expect(dayKey(at(2026, 3, 5, 0, 7))).toBe('2026-03-05')
    expect(timeOf(at(2026, 3, 5, 0, 7))).toBe('00:07')
    expect(timeOf(at(2026, 10, 14, 23, 59))).toBe('23:59')
  })
  it('a day boundary is the local midnight', () => {
    expect(dayKey(at(2026, 10, 14, 23, 59))).toBe('2026-10-14')
    expect(dayKey(at(2026, 10, 15, 0, 0))).toBe('2026-10-15')
  })
})

describe('withDays', () => {
  it('puts one divider before the first entry of each day, entries keep their order', () => {
    const items = withDays([e('a', at(2026, 10, 14, 9, 0)), e('b', at(2026, 10, 14, 18, 0)), e('c', at(2026, 10, 15, 8, 0))])
    expect(items.map((i) => (i.type === 'day' ? `day ${i.day}` : i.entry.id))).toEqual(['day 2026-10-14', 'a', 'b', 'day 2026-10-15', 'c'])
  })
  it('no entries, no dividers', () => {
    expect(withDays([])).toEqual([])
  })
})
