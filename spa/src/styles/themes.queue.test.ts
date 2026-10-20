import { describe, it, expect } from 'vitest'
// See src/index.css.test.ts for why these node imports carry @ts-expect-error under tsconfig.app.json.
// @ts-expect-error node:fs is untyped here.
import { readFileSync } from 'node:fs'
// @ts-expect-error node:path is untyped here.
import { resolve } from 'node:path'

// @ts-expect-error __dirname is untyped here.
const css: string = readFileSync(resolve(__dirname, 'themes.css'), 'utf8')
const theme = css.match(/@theme\s*\{([^}]*)\}/)?.[1] ?? ''
const blockOf = (name: string): string => {
  const start = css.indexOf(`[data-theme="${name}"] {`)
  return start < 0 ? '' : css.slice(start, css.indexOf('\n}', start))
}

describe('themes.css — the resource pool queue strip colours (monitoring tabs MON-3a, M1)', () => {
  it('@theme maps both tokens, each with a fallback for a custom theme that lacks them', () => {
    expect(theme).toMatch(/--color-queue-heavy:\s*var\(--queue-heavy,\s*[^)]+\)/)
    expect(theme).toMatch(/--color-queue-light:\s*var\(--queue-light,\s*[^)]+\)/)
  })

  it.each(['dark', 'light', 'nord', 'dracula'])('the %s theme gives both a colour', (name) => {
    const block = blockOf(name)
    expect(block, name).not.toBe('')
    expect(block).toMatch(/--queue-heavy:\s*#[0-9a-fA-F]{6}/)
    expect(block).toMatch(/--queue-light:\s*#[0-9a-fA-F]{6}/)
  })

  it('the light theme uses darker hues than the dark one (contrast on a light surface)', () => {
    const lum = (hex: string) => { const n = parseInt(hex.slice(1), 16); return ((n >> 16) & 255) * 0.3 + ((n >> 8) & 255) * 0.59 + (n & 255) * 0.11 }
    for (const token of ['queue-heavy', 'queue-light']) {
      const pick = (name: string) => blockOf(name).match(new RegExp(`--${token}:\\s*(#[0-9a-fA-F]{6})`))![1]
      expect(lum(pick('light'))).toBeLessThan(lum(pick('dark')))
    }
  })

  it('the tokens are not in the theme token keys: a custom theme need not define them', async () => {
    const { THEME_TOKEN_KEYS } = await import('../lib/theme-tokens')
    expect(THEME_TOKEN_KEYS).not.toContain('queue-heavy')
    expect(THEME_TOKEN_KEYS).not.toContain('queue-light')
  })
})
