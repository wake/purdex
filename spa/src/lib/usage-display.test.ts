import { describe, it, expect } from 'vitest'
import { epochToMs, formatResetsIn, parseCcUsage, remainingPct, usedPct, usageTone } from './usage-display'

describe('parseCcUsage', () => {
  it('reads the real payload field names; resets_at is epoch seconds', () => {
    const u = parseCcUsage({
      context_window: { used_percentage: 23 },
      rate_limits: {
        five_hour: { used_percentage: 24, resets_at: 1791372000 },
        seven_day: { used_percentage: 73, resets_at: 1791471600 },
      },
    })
    expect(u).toEqual({
      context: 23,
      fiveHour: { pct: 24, resetsAtMs: 1791372000_000 },
      sevenDay: { pct: 73, resetsAtMs: 1791471600_000 },
    })
  })
  it('keeps context when rate_limits is absent, and null windows stay null (never 0)', () => {
    expect(parseCcUsage({ context_window: { used_percentage: 5 } })).toEqual({ context: 5, fiveHour: null, sevenDay: null })
    expect(parseCcUsage({ context_window: { used_percentage: 0 } })?.context).toBe(0)
  })
  it('is null when nothing usable is there', () => {
    expect(parseCcUsage(null)).toBeNull()
    expect(parseCcUsage({})).toBeNull()
    expect(parseCcUsage({ context_window: { used_percentage: null }, rate_limits: { five_hour: {} } })).toBeNull()
  })
  it('a window with a percentage but no reset time has resetsAtMs null', () => {
    expect(parseCcUsage({ rate_limits: { five_hour: { used_percentage: 3 } } })?.fiveHour).toEqual({ pct: 3, resetsAtMs: null })
  })
})

describe('epochToMs', () => {
  it('accepts seconds and milliseconds, rejects junk', () => {
    expect(epochToMs(1791372000)).toBe(1791372000_000)
    expect(epochToMs(1791372000_000)).toBe(1791372000_000)
    expect(epochToMs(0)).toBeNull()
    expect(epochToMs('x')).toBeNull()
  })
})

describe('remainingPct / usedPct', () => {
  it('remaining is 100 - used, rounded and clamped to 0-100', () => {
    expect(remainingPct(15)).toBe(85)
    expect(remainingPct(15.4)).toBe(85)
    expect(remainingPct(120)).toBe(0)
    expect(remainingPct(-5)).toBe(100)
  })
  it('used is clamped to 0-100 (the ring never over- or under-draws)', () => {
    expect(usedPct(15)).toBe(15)
    expect(usedPct(120)).toBe(100)
    expect(usedPct(-5)).toBe(0)
  })
})

describe('usageTone', () => {
  it('shifts at 70 and 90 (inclusive)', () => {
    expect(usageTone(69)).toBe('ok')
    expect(usageTone(70)).toBe('warn')
    expect(usageTone(89)).toBe('warn')
    expect(usageTone(90)).toBe('danger')
  })
})

describe('formatResetsIn', () => {
  const now = 1_000_000_000_000
  it('formats minutes, hours and days', () => {
    expect(formatResetsIn(now + 45 * 60_000, now)).toBe('45m')
    expect(formatResetsIn(now + (2 * 60 + 13) * 60_000, now)).toBe('2h13m')
    expect(formatResetsIn(now + (2 * 60 + 5) * 60_000, now)).toBe('2h05m')
    expect(formatResetsIn(now + (3 * 24 + 4) * 3_600_000, now)).toBe('3d4h')
  })
  it('is null once passed', () => {
    expect(formatResetsIn(now - 1000 * 60, now)).toBeNull()
  })
})
