import { describe, it, expect, beforeEach } from 'vitest'
import { useThemeStore } from './useThemeStore'
import { clearThemeRegistry, getTheme, registerTheme } from '../lib/theme-registry'
import { THEME_TOKEN_KEYS, type ThemeTokens } from '../lib/theme-tokens'

function makeTokens(base = '#000000'): ThemeTokens {
  return Object.fromEntries(THEME_TOKEN_KEYS.map((k) => [k, base])) as ThemeTokens
}

describe('useThemeStore', () => {
  beforeEach(() => {
    clearThemeRegistry()
    registerTheme({ id: 'dark', name: 'Dark', tokens: makeTokens('#111'), builtin: true })
    useThemeStore.setState({ activeThemeId: 'dark', customThemes: {} })
  })

  it('defaults to dark theme', () => {
    expect(useThemeStore.getState().activeThemeId).toBe('dark')
  })

  it('setActiveTheme updates activeThemeId and sets data-theme', () => {
    registerTheme({ id: 'light', name: 'Light', tokens: makeTokens('#fff'), builtin: true })
    useThemeStore.getState().setActiveTheme('light')
    expect(useThemeStore.getState().activeThemeId).toBe('light')
    expect(document.documentElement.dataset.theme).toBe('light')
  })

  it('setActiveTheme ignores unknown theme id', () => {
    useThemeStore.getState().setActiveTheme('nonexistent')
    expect(useThemeStore.getState().activeThemeId).toBe('dark')
  })

  it('createCustomTheme creates and registers theme', () => {
    const id = useThemeStore.getState().createCustomTheme('My Theme', 'dark', { accent: '#ff0000' })
    expect(id).toMatch(/^[0-9a-z]{6}$/)
    expect(useThemeStore.getState().customThemes[id]).toBeDefined()
    expect(useThemeStore.getState().customThemes[id].tokens.accent).toBe('#ff0000')
    expect(useThemeStore.getState().customThemes[id].tokens['surface-primary']).toBe('#111')
    expect(getTheme(id)).toBeDefined()
    expect(getTheme(id)!.builtin).toBe(false)
  })

  it('createCustomTheme id does not collide with builtin or existing custom', () => {
    const id1 = useThemeStore.getState().createCustomTheme('A', 'dark', {})
    const id2 = useThemeStore.getState().createCustomTheme('B', 'dark', {})
    expect(id1).not.toBe(id2)
    expect(id1).not.toBe('dark')
  })

  it('updateCustomTheme patches theme', () => {
    const id = useThemeStore.getState().createCustomTheme('Old', 'dark', {})
    useThemeStore.getState().updateCustomTheme(id, { name: 'New', tokens: { ...makeTokens('#111'), accent: '#00ff00' } })
    expect(useThemeStore.getState().customThemes[id].name).toBe('New')
    expect(useThemeStore.getState().customThemes[id].tokens.accent).toBe('#00ff00')
  })

  it('updateCustomTheme ignores unknown id', () => {
    useThemeStore.getState().updateCustomTheme('nope', { name: 'X' })
  })

  it('deleteCustomTheme removes theme and falls back to dark if active', () => {
    const id = useThemeStore.getState().createCustomTheme('Tmp', 'dark', {})
    useThemeStore.getState().setActiveTheme(id)
    useThemeStore.getState().deleteCustomTheme(id)
    expect(useThemeStore.getState().customThemes[id]).toBeUndefined()
    expect(getTheme(id)).toBeUndefined()
    expect(useThemeStore.getState().activeThemeId).toBe('dark')
  })

  it('deleteCustomTheme does not fall back if not active', () => {
    const id = useThemeStore.getState().createCustomTheme('Tmp', 'dark', {})
    useThemeStore.getState().deleteCustomTheme(id)
    expect(useThemeStore.getState().activeThemeId).toBe('dark')
  })

  it('importTheme validates and creates theme', () => {
    const id = useThemeStore.getState().importTheme({
      name: 'Imported',
      tokens: { accent: '#abcdef' },
    })
    expect(id).toMatch(/^[0-9a-z]{6}$/)
    const theme = useThemeStore.getState().customThemes[id]
    expect(theme.name).toBe('Imported')
    expect(theme.tokens.accent).toBe('#abcdef')
    expect(theme.tokens['surface-primary']).toBe('#111')
  })

  it('importTheme deduplicates name', () => {
    useThemeStore.getState().importTheme({ name: 'Dup', tokens: {} })
    const id2 = useThemeStore.getState().importTheme({ name: 'Dup', tokens: {} })
    expect(useThemeStore.getState().customThemes[id2].name).toBe('Dup (2)')
  })
})
