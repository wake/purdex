// The watcher area draws text and thin strokes in status colours. The light theme's --status-* are background tints, so the area uses
// the `-text` classes: only the light theme defines success/error text values; every other theme falls back to its own status colour.
import { describe, it, expect } from 'vitest'
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'
// @ts-expect-error __dirname is untyped here.
const css: string = readFileSync(resolve(__dirname, '../../styles/themes.css'), 'utf8')

const themes: Record<string, Record<string, string>> = {}
for (const m of css.matchAll(/\[data-theme="([\w-]+)"\]\s*\{([^}]*)\}/g)) {
  themes[m[1]] = {}
  for (const d of m[2].matchAll(/--([\w-]+):\s*([^;]+);/g)) themes[m[1]][d[1]] = d[2].trim()
}

const lum = (hex: string): number => {
  const c = [1, 3, 5].map((i) => Number.parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2]
}
const contrast = (a: string, b: string): number => { const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p); return (x + 0.05) / (y + 0.05) }

describe('status text tokens', () => {
  it('Tailwind maps success/error text with a fallback to the plain status colour', () => {
    expect(css).toContain('--color-status-success-text: var(--status-success-text, var(--status-success));')
    expect(css).toContain('--color-status-error-text: var(--status-error-text, var(--status-error));')
  })
  it('the light theme has text-strength success / error / warning, readable (>= 4.5:1) on its surface', () => {
    const light = themes.light
    for (const k of ['status-success-text', 'status-error-text', 'status-warning-text']) {
      expect(light[k], k).toBeDefined()
      expect(contrast(light[k], light['surface-primary'] ?? '#f5f5f5')).toBeGreaterThanOrEqual(4.5)
    }
  })
  it('the other themes define no success/error text token, so they fall back to the colours they use today', () => {
    for (const [name, vars] of Object.entries(themes)) {
      if (name === 'light') continue
      expect(vars['status-success-text'], name).toBeUndefined()
      expect(vars['status-error-text'], name).toBeUndefined()
    }
  })
})
