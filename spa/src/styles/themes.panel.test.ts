import { describe, it, expect } from 'vitest'
// See src/index.css.test.ts for why these node imports carry @ts-expect-error under tsconfig.app.json.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'

// The panel area (TeamPanelArea: team and personal panels, the recap history) reads text-secondary and text-muted brighter than the rest of
// the App: `.panel-text-tiers` mixes each toward the theme's text-primary. These tests take the mix out of themes.css and work out what it
// gives on every built-in theme, on the surfaces the panel draws on.

// @ts-expect-error __dirname is untyped here.
const css: string = readFileSync(resolve(__dirname, 'themes.css'), 'utf8')
const theme = css.match(/@theme\s*\{([^}]*)\}/)?.[1] ?? ''
const rule = css.match(/\.panel-text-tiers\s*\{([^}]*)\}/)?.[1] ?? ''
const blockOf = (name: string): string => {
  const start = css.indexOf(`[data-theme="${name}"] {`)
  return start < 0 ? '' : css.slice(start, css.indexOf('\n}', start))
}
const tokenOf = (block: string, token: string): string => block.match(new RegExp(`--${token}:\\s*(#[0-9a-fA-F]{6})`))![1]
/** The share of the tier's own colour `.panel-text-tiers` keeps (the rest is text-primary), from `color-mix(in srgb, var(--text-X) N%, var(--text-primary))`. */
const keptShare = (tier: 'secondary' | 'muted'): number => {
  const m = rule.match(new RegExp(`--color-text-${tier}:\\s*color-mix\\(in srgb,\\s*var\\(--text-${tier}\\)\\s*(\\d+(?:\\.\\d+)?)%,\\s*var\\(--text-primary\\)\\)`))
  if (!m) throw new Error(`.panel-text-tiers does not mix text-${tier} toward text-primary`)
  return Number(m[1]) / 100
}

const rgb = (hex: string) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16))
const channel = (c: number) => { const s = c / 255; return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4 }
const luminance = (hex: string) => { const [r, g, b] = rgb(hex).map(channel); return 0.2126 * r + 0.7152 * g + 0.0722 * b }
const contrast = (a: string, b: string) => { const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05) }
const mix = (a: string, b: string, keep: number) => '#' + rgb(a).map((v, i) => Math.round(v * keep + rgb(b)[i] * (1 - keep)).toString(16).padStart(2, '0')).join('')

const THEMES = ['dark', 'light', 'nord', 'dracula'] as const
const tiers = (name: string) => {
  const block = blockOf(name)
  const primary = tokenOf(block, 'text-primary')
  const secondary = tokenOf(block, 'text-secondary')
  const muted = tokenOf(block, 'text-muted')
  return {
    surface: tokenOf(block, 'surface-elevated'), // the panel area's own background
    primary, secondary, muted,
    panelSecondary: mix(secondary, primary, keptShare('secondary')),
    panelMuted: mix(muted, primary, keptShare('muted')),
  }
}

describe('themes.css — the panel area\'s text tiers', () => {
  it('remaps text-secondary and text-muted, and nothing else', () => {
    const props = [...rule.matchAll(/(--[\w-]+)\s*:/g)].map((m) => m[1]).sort()
    expect(props).toEqual(['--color-text-muted', '--color-text-secondary'])
  })

  it('outside the panel the tiers are the theme\'s own (the @theme mapping is untouched)', () => {
    expect(theme).toMatch(/--color-text-secondary:\s*var\(--text-secondary\);/)
    expect(theme).toMatch(/--color-text-muted:\s*var\(--text-muted\);/)
  })

  it.each(THEMES)('%s: both tiers read clearer on the panel than the theme\'s own, and primary > secondary > muted still holds', (name) => {
    const t = tiers(name)
    expect(contrast(t.panelSecondary, t.surface)).toBeGreaterThan(contrast(t.secondary, t.surface))
    expect(contrast(t.panelMuted, t.surface)).toBeGreaterThan(contrast(t.muted, t.surface))
    expect(contrast(t.primary, t.surface)).toBeGreaterThan(contrast(t.panelSecondary, t.surface))
    expect(contrast(t.panelSecondary, t.surface)).toBeGreaterThan(contrast(t.panelMuted, t.surface))
  })

  it.each(['dark', 'light'] as const)('%s: muted reaches 4.5:1 and secondary 7:1 on the panel', (name) => {
    const t = tiers(name)
    expect(contrast(t.panelMuted, t.surface)).toBeGreaterThanOrEqual(4.5)
    expect(contrast(t.panelSecondary, t.surface)).toBeGreaterThanOrEqual(7)
  })
})
