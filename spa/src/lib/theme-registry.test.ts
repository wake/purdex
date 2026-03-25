import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerTheme, getTheme, getAllThemes, unregisterTheme, clearThemeRegistry,
} from './theme-registry'
import type { ThemeTokens } from './theme-tokens'
import { THEME_TOKEN_KEYS } from './theme-tokens'

function makeTokens(base = '#000000'): ThemeTokens {
  return Object.fromEntries(THEME_TOKEN_KEYS.map((k) => [k, base])) as ThemeTokens
}

describe('theme-registry', () => {
  beforeEach(() => clearThemeRegistry())

  it('registers and retrieves a theme', () => {
    registerTheme({ id: 'test', name: 'Test', tokens: makeTokens(), builtin: false })
    expect(getTheme('test')).toBeDefined()
    expect(getTheme('test')!.name).toBe('Test')
  })

  it('returns undefined for unregistered theme', () => {
    expect(getTheme('nope')).toBeUndefined()
  })

  it('getAllThemes returns all registered themes', () => {
    registerTheme({ id: 'a', name: 'A', tokens: makeTokens(), builtin: false })
    registerTheme({ id: 'b', name: 'B', tokens: makeTokens(), builtin: true })
    expect(getAllThemes()).toHaveLength(2)
  })

  it('registerTheme is idempotent (overwrites)', () => {
    registerTheme({ id: 'x', name: 'V1', tokens: makeTokens(), builtin: false })
    registerTheme({ id: 'x', name: 'V2', tokens: makeTokens(), builtin: false })
    expect(getTheme('x')!.name).toBe('V2')
    expect(getAllThemes()).toHaveLength(1)
  })

  it('unregisterTheme removes non-builtin theme', () => {
    registerTheme({ id: 'custom', name: 'C', tokens: makeTokens(), builtin: false })
    unregisterTheme('custom')
    expect(getTheme('custom')).toBeUndefined()
  })

  it('unregisterTheme refuses to remove builtin theme', () => {
    registerTheme({ id: 'dark', name: 'Dark', tokens: makeTokens(), builtin: true })
    unregisterTheme('dark')
    expect(getTheme('dark')).toBeDefined()
  })

  it('clearThemeRegistry removes all themes', () => {
    registerTheme({ id: 'a', name: 'A', tokens: makeTokens(), builtin: true })
    registerTheme({ id: 'b', name: 'B', tokens: makeTokens(), builtin: false })
    clearThemeRegistry()
    expect(getAllThemes()).toHaveLength(0)
  })
})
