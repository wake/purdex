// The journal's kind chips (spec §11, the approved prototype's seven kinds, each its own colour) use theme tokens, never colour codes.
// The six hues that are not warning live in themes.css only (the theme editor's token list is untouched): each builtin theme gives
// its own value, a custom theme that lacks them falls back to a token it does have. 待確認 is `journal-warning-text`, not one of these.
import { describe, it, expect } from 'vitest'

// node:fs / node:path / __dirname are untyped under tsconfig.app.json (see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
// @ts-expect-error __dirname is untyped here.
const themesCss: string = readFileSync(resolve(__dirname, '../../styles/themes.css'), 'utf8')

// @ts-expect-error __dirname is untyped here.
const DIR: string = __dirname
const source = (rel: string): string => readFileSync(resolve(DIR, rel), 'utf8')

const KINDS = ['exec', 'exec-done', 'task-new', 'task-done', 'comm', 'request'] as const
const FALLBACK: Record<(typeof KINDS)[number], string> = {
  exec: 'var(--accent)', 'exec-done': 'var(--status-success-text, var(--status-success))', 'task-new': 'var(--accent)',
  'task-done': 'var(--status-success-text, var(--status-success))', comm: 'var(--text-secondary)', request: 'var(--accent)',
}
// The approved prototype's colours (docs/prototypes/workbook-journal-v4/components/types.ts KIND_COLOR), used as they are on the dark theme.
const PROTOTYPE = { exec: '#5b8def', 'exec-done': '#2f9e6b', 'task-new': '#f08a4b', 'task-done': '#b5e07a', comm: '#bb9af7', request: '#f2647f' } as const

const blocks = (): Record<string, Record<string, string>> => {
  const out: Record<string, Record<string, string>> = {}
  for (const m of themesCss.matchAll(/\[data-theme="([\w-]+)"\]\s*\{([^}]*)\}/g)) {
    const vars: Record<string, string> = {}
    for (const d of m[2].matchAll(/--([\w-]+):\s*([^;]+);/g)) vars[d[1]] = d[2].trim()
    out[m[1]] = vars
  }
  return out
}

type Rgb = [number, number, number]
const rgb = (hex: string): Rgb => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)) as Rgb
const lin = (v: number): number => { const s = v / 255; return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4 }
const lum = (c: Rgb): number => 0.2126 * lin(c[0]) + 0.7152 * lin(c[1]) + 0.0722 * lin(c[2])
const ratio = (a: Rgb, b: Rgb): number => { const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05) }
const over = (bg: Rgb, fg: Rgb, a: number): Rgb => bg.map((v, i) => v * (1 - a) + fg[i] * a) as Rgb

describe('journal kind tokens', () => {
  it('the Tailwind colours map to the variables and fall back to a token every theme has (a custom theme lacks the new variables)', () => {
    for (const k of KINDS) {
      const re = new RegExp(`--color-journal-kind-${k}:\\s*var\\(--journal-kind-${k},\\s*${FALLBACK[k].replace(/[()]/g, '\\$&')}\\);`)
      expect(themesCss, `--color-journal-kind-${k}`).toMatch(re)
    }
  })

  it('every builtin theme gives all six values', () => {
    const css = blocks()
    expect(Object.keys(css).sort()).toEqual(['dark', 'dracula', 'light', 'nord'])
    for (const [id, v] of Object.entries(css)) for (const k of KINDS) expect(v[`journal-kind-${k}`], `${id} lacks --journal-kind-${k}`).toMatch(/^#[0-9a-f]{6}$/i)
  })

  it('the dark theme uses the prototype\'s colours as approved', () => {
    const dark = blocks().dark
    for (const k of KINDS) expect(dark[`journal-kind-${k}`].toLowerCase()).toBe(PROTOTYPE[k])
  })

  it('the other themes keep chip text at WCAG AA (4.5:1) on the chip itself: the card surface with 15% of the chip colour over it (M: the light theme reusing the dark hues)', () => {
    for (const [id, v] of Object.entries(blocks())) {
      if (id === 'dark') continue
      for (const surface of ['surface-elevated', 'surface-secondary']) {
        for (const k of KINDS) {
          const colour = rgb(v[`journal-kind-${k}`])
          const chip = over(rgb(v[surface]), colour, 0.15)
          expect(ratio(colour, chip), `${id} ${k} on ${surface}`).toBeGreaterThanOrEqual(4.5)
        }
      }
    }
  })

  // The 待確認 chip (text-journal-warning-text on bg-status-warning/15, KindChip) and the person's highlighted name (the same text colour
  // on bg-status-warning/20, PersonMark) are not `journal-kind-*` tokens, so the test above does not cover them.
  it('the sources use the opacities the next test assumes', () => {
    expect(source('../../components/journal/KindChip.tsx')).toContain('text-journal-warning-text bg-status-warning/15')
    expect(source('../../components/journal/PersonMark.tsx')).toContain('bg-status-warning/20 text-journal-warning-text')
  })

  it.each([['the 待確認 chip', 0.15], ['the highlighted person name', 0.2]] as const)('the other themes keep %s at WCAG AA (4.5:1): journal-warning-text (else status-warning-text) on the card surface with the warning fill over it', (_what, alpha) => {
    for (const [id, v] of Object.entries(blocks())) {
      if (id === 'dark') continue
      // The rows sit on the card (CardFrame: bg-surface-elevated); no journal component draws on surface-secondary.
      const bg = over(rgb(v['surface-elevated']), rgb(v['status-warning']), alpha)
      // Only Nord sets --journal-warning-text; the other themes fall back to their status-warning-text (the CSS fallback chain).
      expect(ratio(rgb(v['journal-warning-text'] ?? v['status-warning-text']), bg), id).toBeGreaterThanOrEqual(4.5)
    }
  })
})
