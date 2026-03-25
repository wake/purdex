import { describe, it, expect } from 'vitest'
import { THEME_TOKEN_KEYS, TOKEN_METADATA, type ThemeTokenKey, type ThemeTokens, tokensToCss } from './theme-tokens'

describe('theme-tokens', () => {
  it('exports 23 token keys', () => {
    expect(THEME_TOKEN_KEYS).toHaveLength(23)
  })

  it('every key has metadata with label and group', () => {
    for (const key of THEME_TOKEN_KEYS) {
      const meta = TOKEN_METADATA[key]
      expect(meta, `missing metadata for ${key}`).toBeDefined()
      expect(meta.label).toBeTruthy()
      expect(meta.group).toBeTruthy()
    }
  })

  it('groups are one of the 6 defined groups', () => {
    const validGroups = ['surface', 'text', 'border', 'accent', 'terminal', 'status']
    for (const key of THEME_TOKEN_KEYS) {
      expect(validGroups).toContain(TOKEN_METADATA[key].group)
    }
  })

  it('ThemeTokenKey type matches THEME_TOKEN_KEYS', () => {
    const keys: ThemeTokenKey[] = [...THEME_TOKEN_KEYS]
    expect(keys.length).toBe(23)
  })

  it('tokensToCss converts tokens to CSS variable declarations', () => {
    const tokens = Object.fromEntries(THEME_TOKEN_KEYS.map((k) => [k, '#000'])) as ThemeTokens
    tokens.accent = '#ff0000'
    const css = tokensToCss(tokens)
    expect(css).toContain('--accent: #ff0000;')
    expect(css).toContain('--surface-primary: #000;')
  })
})
