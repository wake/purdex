// #2006: `--status-warning` is a fill (the light theme's is #fef3cd, a pale yellow); text needs its own token. Every builtin
// theme must carry `--status-warning-text` in themes.css (what the app renders) and in register-themes.ts (what the editor
// shows), the Tailwind class must map to it, and it must be readable on the surfaces text sits on.
import { describe, it, expect, beforeEach } from 'vitest'
import { THEME_TOKEN_KEYS } from './theme-tokens'
import { clearThemeRegistry, getAllThemes } from './theme-registry'
import { registerBuiltinThemes } from './register-themes'

// node:fs / node:path / __dirname are untyped under tsconfig.app.json (see src/index.css.test.ts); they work at runtime.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
// @ts-expect-error __dirname is untyped here.
const themesCss: string = readFileSync(resolve(__dirname, '../styles/themes.css'), 'utf8')

const blocks = (): Record<string, Record<string, string>> => {
  const out: Record<string, Record<string, string>> = {}
  for (const m of themesCss.matchAll(/\[data-theme="([\w-]+)"\]\s*\{([^}]*)\}/g)) {
    const vars: Record<string, string> = {}
    for (const d of m[2].matchAll(/--([\w-]+):\s*([^;]+);/g)) vars[d[1]] = d[2].trim()
    out[m[1]] = vars
  }
  return out
}

const lum = (hex: string): number => {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]
}
const contrast = (a: string, b: string): number => {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

beforeEach(() => { clearThemeRegistry(); registerBuiltinThemes() })

describe('status-warning-text', () => {
  it('is a theme token key', () => {
    expect(THEME_TOKEN_KEYS).toContain('status-warning-text')
  })

  it('the Tailwind colour maps to the CSS variable, falling back to the fill for a theme that lacks it (text-status-warning-text)', () => {
    expect(themesCss).toMatch(/--color-status-warning-text:\s*var\(--status-warning-text,\s*var\(--status-warning\)\);/)
  })

  it('every builtin theme defines every token key in themes.css and in the editor copy (the new one with the same value in both)', () => {
    const css = blocks()
    const themes = getAllThemes()
    expect(themes.map((t) => t.id).sort()).toEqual(Object.keys(css).sort())
    for (const t of themes) {
      for (const key of THEME_TOKEN_KEYS) {
        expect(css[t.id][key], `themes.css ${t.id} lacks --${key}`).toBeTruthy()
        expect(t.tokens[key], `register-themes ${t.id} lacks ${key}`).toBeTruthy()
      }
      expect(css[t.id]['status-warning-text'].toLowerCase(), `${t.id}: css and editor copy differ`).toBe(t.tokens['status-warning-text'].toLowerCase())
    }
  })

  it('the light theme\'s is the dark amber #8a6d00; the dark-surface themes keep their warning colour', () => {
    const css = blocks()
    expect(css.light['status-warning-text']).toBe('#8a6d00')
    for (const id of ['dark', 'nord', 'dracula']) expect(css[id]['status-warning-text']).toBe(css[id]['status-warning'])
  })

  it('is readable (WCAG AA, 4.5:1) on every theme\'s primary and elevated surface', () => {
    const css = blocks()
    for (const [id, v] of Object.entries(css)) {
      for (const surface of ['surface-primary', 'surface-elevated']) {
        expect(contrast(v['status-warning-text'], v[surface]), `${id} on ${surface}`).toBeGreaterThanOrEqual(4.5)
      }
    }
  })
})
