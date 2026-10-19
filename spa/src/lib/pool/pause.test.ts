import { describe, it, expect, beforeEach } from 'vitest'
import { holdsOf, loadRecentClocks, parseClock, pauseName, PAUSE_HOURS, RECENT_MAX, rememberClock, saveRecentClocks, untilClock, untilInHours } from './pause'

// Local time throughout: the end time of a custom clock is the next time the wall clock reads it.
const at = (h: number, m: number, day = 15) => new Date(2026, 9, day, h, m, 0, 0).getTime()

describe('the end time of a pause', () => {
  it('N 小時 is exactly N hours after now; the lengths offered are 1, 3 and 8', () => {
    expect(PAUSE_HOURS).toEqual([1, 3, 8])
    expect(untilInHours(1_000_000, 1)).toBe(1_000_000 + 3_600_000)
    expect(untilInHours(1_000_000, 8)).toBe(1_000_000 + 8 * 3_600_000)
  })

  it('a clock time still ahead today ends today; one already past ends tomorrow; the same minute is already past', () => {
    expect(untilClock(at(10, 0), '18:30')).toBe(at(18, 30))
    expect(untilClock(at(20, 0), '18:30')).toBe(at(18, 30, 16))
    expect(untilClock(at(18, 30), '18:30')).toBe(at(18, 30, 16))
    expect(untilClock(at(23, 59), '00:00')).toBe(at(0, 0, 16))
  })

  it('text that is no clock time gives nothing', () => {
    for (const bad of ['', '25:00', '12:60', 'ab:cd', '1830', '12:5']) expect(untilClock(at(10, 0), bad)).toBeNull()
    expect(parseClock('7:05')).toEqual({ h: 7, m: 5 })
  })
})

describe('the name and the holds of a pause', () => {
  it('is one plain path segment, and two made in different milliseconds differ', () => {
    expect(pauseName(1_790_000_000_123)).toMatch(/^app-pause-[0-9a-z]+$/)
    expect(pauseName(1_790_000_000_123)).not.toBe(pauseName(1_790_000_000_124))
  })
  it('holds heavy, and light as well when asked', () => {
    expect(holdsOf(false)).toEqual(['heavy'])
    expect(holdsOf(true)).toEqual(['heavy', 'light'])
  })
})

describe('the custom end times remembered', () => {
  beforeEach(() => localStorage.clear())

  it('keeps the newest first, each once, at most three, written as hh:mm', () => {
    let r: string[] = []
    for (const c of ['18:30', '9:05', '22:00', '23:00']) r = rememberClock(r, c)
    expect(r).toEqual(['23:00', '22:00', '09:05'])
    expect(r).toHaveLength(RECENT_MAX)
    expect(rememberClock(r, '22:00')).toEqual(['22:00', '23:00', '09:05'])
    expect(rememberClock(r, 'nonsense')).toEqual(r)
  })

  it('survives a reload of the page, drops what is not a clock time, and a browser that refuses storage remembers nothing', () => {
    saveRecentClocks(['18:30', '09:05'])
    expect(loadRecentClocks()).toEqual(['18:30', '09:05'])
    localStorage.setItem('pdx:pool:recent-pause-clocks', JSON.stringify(['18:30', 'x', 5]))
    expect(loadRecentClocks()).toEqual(['18:30'])
    localStorage.setItem('pdx:pool:recent-pause-clocks', '{not json')
    expect(loadRecentClocks()).toEqual([])
  })
})
