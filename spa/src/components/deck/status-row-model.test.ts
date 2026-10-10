import { describe, it, expect } from 'vitest'
import { formatTokens, shortReset, splitModel, tokensLeft } from './status-row-model'

describe('status-row-model', () => {
  const NOW = 1_800_000_000_000
  it('shortReset: minutes, hours, days; null when passed or absent', () => {
    expect(shortReset(NOW + 45 * 60_000, NOW)).toBe('45m')
    expect(shortReset(NOW + 133 * 60_000, NOW)).toBe('2h')
    expect(shortReset(NOW + (3 * 24 + 4) * 3_600_000, NOW)).toBe('3d')
    expect(shortReset(NOW - 1, NOW)).toBeNull()
    expect(shortReset(null, NOW)).toBeNull()
    expect(shortReset(undefined, NOW)).toBeNull()
    expect(shortReset(NaN, NOW)).toBeNull()
    expect(shortReset(NOW, NaN)).toBeNull()
  })
  it('formatTokens', () => {
    expect(formatTokens(620_000)).toBe('620K')
    expect(formatTokens(1_000_000)).toBe('1M')
    expect(formatTokens(1_500_000)).toBe('1.5M')
    expect(formatTokens(800)).toBe('800')
  })
  it('splitModel pulls a trailing window off', () => {
    expect(splitModel('Opus 5.5 (1M)')).toEqual({ name: 'Opus 5.5', window: '(1M)' })
    expect(splitModel('Sonnet 5.5')).toEqual({ name: 'Sonnet 5.5', window: null })
  })
  it('tokensLeft is the remaining share of the window, clamped', () => {
    expect(tokensLeft(1_000_000, 38)).toBe(620_000)
    expect(tokensLeft(1_000_000, 120)).toBe(0)
    expect(tokensLeft(1_000_000, -5)).toBe(1_000_000)
  })
})
