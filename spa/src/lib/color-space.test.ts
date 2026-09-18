import { describe, it, expect } from 'vitest'
import { hexToRgb, rgbaString } from './color-space'

describe('hexToRgb', () => {
  it('parses #rrggbb (any case)', () => {
    expect(hexToRgb('#3b82f6')).toEqual({ r: 59, g: 130, b: 246 })
    expect(hexToRgb('#3B82F6')).toEqual({ r: 59, g: 130, b: 246 })
  })
  it.each(['3b82f6', '#abc', '#gggggg', '', 'red'])('rejects %j', (bad) => {
    expect(hexToRgb(bad)).toBeNull()
  })
})

describe('rgbaString', () => {
  it('formats alpha percent as a 0–1 fraction', () => {
    expect(rgbaString('#3b82f6', 22)).toBe('rgba(59, 130, 246, 0.22)')
    expect(rgbaString('#3b82f6', 100)).toBe('rgba(59, 130, 246, 1)')
    expect(rgbaString('#3b82f6', 0)).toBe('rgba(59, 130, 246, 0)')
  })
  it('returns null for an invalid hex', () => {
    expect(rgbaString('red', 50)).toBeNull()
  })
})
