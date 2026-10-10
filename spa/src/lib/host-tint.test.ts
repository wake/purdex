// The tint is a fixed share of the host colour over the theme's surface, so the worst case is a preset colour on a theme: --text-primary
// must stay at 4.5:1 on every one. The theme values are read from themes.css, so a retuned theme is checked, not a copy of it.
import { describe, it, expect } from 'vitest'
// node:fs / node:path / __dirname are untyped under tsconfig.app.json (deliberately no @types/node; see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
import { HOST_COLOR_PRESETS } from './host-color'
import { hexToRgb } from './color-space'
import { HOST_TINT_PCT, hostTintBackground } from './host-tint'

// @ts-expect-error __dirname is untyped here.
const themes = readFileSync(resolve(__dirname, '../styles/themes.css'), 'utf8')
const tokenOf = (theme: string, token: string): string => {
  const from = themes.indexOf(`[data-theme="${theme}"] {`)
  const block = themes.slice(from, themes.indexOf('\n}', from)) // this theme's block only: a missing token must fail, not read the next theme's
  return new RegExp(`${token}:\\s*(#[0-9a-fA-F]{6})`).exec(block)![1]
}
type Rgb = { r: number; g: number; b: number }
const lum = ({ r, g, b }: Rgb) => {
  const f = (c: number) => { const s = c / 255; return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4 }
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)
}
const contrast = (a: Rgb, b: Rgb) => { const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05) }

describe('host tint', () => {
  it('mixes the host colour into the theme surface at a middle share', () => {
    expect(hostTintBackground('#3b82f6')).toBe(`color-mix(in srgb, #3b82f6 ${HOST_TINT_PCT}%, var(--surface-secondary))`)
    expect(HOST_TINT_PCT).toBeGreaterThanOrEqual(30)
  })
  it.each(['dark', 'light', 'nord', 'dracula'])('keeps text-primary at 4.5:1 or better on %s for every preset host colour', (theme) => {
    const surface = hexToRgb(tokenOf(theme, '--surface-secondary'))!
    const text = hexToRgb(tokenOf(theme, '--text-primary'))!
    const k = HOST_TINT_PCT / 100
    for (const preset of HOST_COLOR_PRESETS) {
      const h = hexToRgb(preset)!
      const ground = { r: h.r * k + surface.r * (1 - k), g: h.g * k + surface.g * (1 - k), b: h.b * k + surface.b * (1 - k) }
      expect(contrast(text, ground), `${preset} on ${theme}`).toBeGreaterThanOrEqual(4.5)
    }
  })
})
