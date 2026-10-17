// spa/src/lib/relay/handoff-time.test.ts — local-time arithmetic, so every instant is built from local components.
import { describe, it, expect } from 'vitest'
import { formatHandoffTime } from './handoff-time'

const at = (y: number, mo: number, d: number, h: number, mi: number) => new Date(y, mo - 1, d, h, mi, 30).getTime()

describe('formatHandoffTime', () => {
  const now = at(2026, 10, 17, 14, 5)

  it('the same day prints HH:mm, zero padded', () => {
    expect(formatHandoffTime(at(2026, 10, 17, 9, 7), now)).toBe('09:07')
    expect(formatHandoffTime(at(2026, 10, 17, 14, 5), now)).toBe('14:05')
  })

  it('midnight starts the day: 00:00 today is today, 23:59 the day before carries its date', () => {
    expect(formatHandoffTime(at(2026, 10, 17, 0, 0), now)).toBe('00:00')
    expect(formatHandoffTime(at(2026, 10, 16, 23, 59), now)).toBe('2026-10-16 23:59')
  })

  it('an earlier day prints the date and the time', () => {
    expect(formatHandoffTime(at(2026, 9, 3, 8, 30), now)).toBe('2026-09-03 08:30')
    expect(formatHandoffTime(at(2025, 12, 31, 23, 0), now)).toBe('2025-12-31 23:00')
  })

  it('the same day of another month or year is not today', () => {
    expect(formatHandoffTime(at(2026, 9, 17, 14, 5), now)).toBe('2026-09-17 14:05')
    expect(formatHandoffTime(at(2025, 10, 17, 14, 5), now)).toBe('2025-10-17 14:05')
  })

  it('a time a little ahead of now (clock skew) is judged by the calendar day alone', () => {
    // later today: a time of day
    expect(formatHandoffTime(at(2026, 10, 17, 14, 7), now)).toBe('14:07')
    // the host\'s clock is ahead and the write is already tomorrow: the date is printed
    expect(formatHandoffTime(at(2026, 10, 18, 0, 1), now)).toBe('2026-10-18 00:01')
  })

  it('a time Date cannot hold prints nothing, and never throws', () => {
    expect(formatHandoffTime(1e20, now)).toBe('')
    expect(formatHandoffTime(NaN, now)).toBe('')
    expect(formatHandoffTime(Infinity, now)).toBe('')
  })
})
