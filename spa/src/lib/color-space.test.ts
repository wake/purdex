import { describe, it, expect } from 'vitest'
import { hexToRgb, rgbaString, hexToHsl, hslToHex, hexToHsv, hsvToHex } from './color-space'
import { HOST_COLOR_PRESETS } from './host-color'

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

describe('hexToHsl / hslToHex', () => {
  it('converts primaries and greys', () => {
    expect(hexToHsl('#ff0000')).toEqual({ h: 0, s: 100, l: 50 })
    expect(hexToHsl('#00ff00')).toEqual({ h: 120, s: 100, l: 50 })
    expect(hexToHsl('#0000ff')).toEqual({ h: 240, s: 100, l: 50 })
    expect(hexToHsl('#000000')).toEqual({ h: 0, s: 0, l: 0 })
    expect(hexToHsl('#ffffff')).toEqual({ h: 0, s: 0, l: 100 })
    expect(hexToHsl('#808080')?.s).toBe(0)
  })

  it('rejects anything that is not #rrggbb', () => {
    expect(hexToHsl('3b82f6')).toBeNull()
    expect(hexToHsl('#abc')).toBeNull()
  })

  it.each(HOST_COLOR_PRESETS)('round-trips preset %s exactly', (hex) => {
    expect(hslToHex(hexToHsl(hex)!)).toBe(hex)
  })

  it('hslToHex clamps s/l, wraps h, and lowercases', () => {
    expect(hslToHex({ h: 360, s: 100, l: 50 })).toBe('#ff0000')
    expect(hslToHex({ h: -120, s: 100, l: 50 })).toBe('#0000ff')
    expect(hslToHex({ h: 0, s: 150, l: -5 })).toBe('#000000')
    expect(hslToHex({ h: 0, s: 0, l: 200 })).toBe('#ffffff')
  })

  it('rgbaString(hslToHex(x)) stays consistent with hexToRgb', () => {
    const hex = hslToHex({ h: 217.2, s: 91.2, l: 59.8 })
    expect(hexToRgb(hex)).toEqual({ r: 59, g: 130, b: 246 })
  })
})

describe('hexToHsv / hsvToHex', () => {
  it('converts primaries, black and white', () => {
    expect(hexToHsv('#ff0000')).toEqual({ h: 0, s: 100, v: 100 })
    expect(hexToHsv('#00ff00')).toEqual({ h: 120, s: 100, v: 100 })
    expect(hexToHsv('#0000ff')).toEqual({ h: 240, s: 100, v: 100 })
    expect(hexToHsv('#000000')).toEqual({ h: 0, s: 0, v: 0 })
    expect(hexToHsv('#ffffff')).toEqual({ h: 0, s: 0, v: 100 })
    expect(hexToHsv('#808080')).toEqual({ h: 0, s: 0, v: (128 / 255) * 100 })
  })
  it('rejects non-#rrggbb', () => {
    expect(hexToHsv('abc')).toBeNull()
  })
  it.each(HOST_COLOR_PRESETS)('round-trips preset %s exactly', (hex) => {
    expect(hsvToHex(hexToHsv(hex)!)).toBe(hex)
  })
  it('hsvToHex: white at s=0,v=100; black at v=0; pure hue at s=100,v=100; wraps hue', () => {
    expect(hsvToHex({ h: 200, s: 0, v: 100 })).toBe('#ffffff')
    expect(hsvToHex({ h: 200, s: 100, v: 0 })).toBe('#000000')
    expect(hsvToHex({ h: 360, s: 100, v: 100 })).toBe('#ff0000')
    expect(hsvToHex({ h: -120, s: 100, v: 100 })).toBe('#0000ff')
    expect(hsvToHex({ h: 120, s: 150, v: 200 })).toBe('#00ff00')
  })
})
