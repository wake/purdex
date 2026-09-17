// spa/src/lib/nex/format-duration.test.ts
import { describe, it, expect } from 'vitest'
import { formatDuration } from './format-duration'

describe('formatDuration', () => {
  it('negative → 0.0s', () => {
    expect(formatDuration(-1)).toBe('0.0s')
    expect(formatDuration(-60_000)).toBe('0.0s')
  })

  it('NaN → 0.0s', () => {
    expect(formatDuration(Number.NaN)).toBe('0.0s')
  })

  it('zero → 0.0s', () => {
    expect(formatDuration(0)).toBe('0.0s')
  })

  it('sub-second → one decimal', () => {
    expect(formatDuration(400)).toBe('0.4s')
    expect(formatDuration(49)).toBe('0.0s')
    expect(formatDuration(50)).toBe('0.1s')
  })

  it('seconds → one decimal', () => {
    expect(formatDuration(6_200)).toBe('6.2s')
    expect(formatDuration(6_249)).toBe('6.2s')
    expect(formatDuration(6_250)).toBe('6.3s')
    expect(formatDuration(59_900)).toBe('59.9s')
    expect(formatDuration(59_949)).toBe('59.9s')
  })

  it('59950–59999 rounds to 60.0 and is promoted to 1m 00s', () => {
    expect(formatDuration(59_950)).toBe('1m 00s')
    expect(formatDuration(59_999)).toBe('1m 00s')
  })

  it('minute boundary → Xm YYs with zero-padded seconds', () => {
    expect(formatDuration(60_000)).toBe('1m 00s')
    expect(formatDuration(65_000)).toBe('1m 05s')
    expect(formatDuration(65_999)).toBe('1m 05s')
    expect(formatDuration(720_000)).toBe('12m 00s')
    expect(formatDuration(3_599_999)).toBe('59m 59s')
  })

  it('hour boundary → Xh YYm with zero-padded minutes', () => {
    expect(formatDuration(3_600_000)).toBe('1h 00m')
    expect(formatDuration(3_720_000)).toBe('1h 02m')
    expect(formatDuration(3_779_999)).toBe('1h 02m')
    expect(formatDuration(45_000_000)).toBe('12h 30m')
  })
})
