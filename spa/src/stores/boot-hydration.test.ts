// #1385 — the real boot order, reproduced: the storage is synchronous, so zustand's
// persist hydrates INSIDE `create()`, before the module's `const useXStore` is
// initialised, and before main.tsx gets to call `registerBuiltin*()`. Each test
// resets the module graph (fresh, empty registries), seeds localStorage, and only
// then imports the store — nothing is pre-registered.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import zhTW from '../locales/zh-TW.json'

beforeEach(() => {
  vi.resetModules()
  localStorage.clear()
  document.documentElement.lang = ''
  delete document.documentElement.dataset.theme
})

describe('boot hydration (#1385)', () => {
  it('i18n: a persisted zh-TW is fully applied at boot', async () => {
    const registry = await import('../lib/locale-registry')
    registry.clearLocaleRegistry()
    localStorage.setItem(
      'purdex-i18n',
      JSON.stringify({ state: { activeLocaleId: 'zh-TW', customLocales: {} }, version: 1 }),
    )

    const { useI18nStore } = await import('./useI18nStore')

    expect.soft(useI18nStore.persist.hasHydrated()).toBe(true)
    expect.soft(useI18nStore.getState().activeLocaleId).toBe('zh-TW')
    expect.soft(useI18nStore.getState().t('nav.new_workspace')).toBe(zhTW['nav.new_workspace'])
    expect.soft(document.documentElement.lang).toBe('zh-TW')
  })

  it('theme: a persisted light theme is applied to the DOM at boot', async () => {
    const registry = await import('../lib/theme-registry')
    registry.clearThemeRegistry()
    localStorage.setItem(
      'purdex-themes',
      JSON.stringify({ state: { activeThemeId: 'light', customThemes: {} }, version: 1 }),
    )

    const { useThemeStore } = await import('./useThemeStore')

    expect.soft(useThemeStore.persist.hasHydrated()).toBe(true)
    expect.soft(useThemeStore.getState().activeThemeId).toBe('light')
    expect.soft(document.documentElement.dataset.theme).toBe('light')
  })

  // The repair branch: only a theme that no longer exists makes the callback write to the store.
  it('theme: a persisted theme that no longer exists falls back to dark at boot', async () => {
    const registry = await import('../lib/theme-registry')
    registry.clearThemeRegistry()
    localStorage.setItem(
      'purdex-themes',
      JSON.stringify({ state: { activeThemeId: 'gone', customThemes: {} }, version: 1 }),
    )

    const { useThemeStore } = await import('./useThemeStore')

    expect.soft(useThemeStore.persist.hasHydrated()).toBe(true)
    expect.soft(useThemeStore.getState().activeThemeId).toBe('dark')
    expect.soft(document.documentElement.dataset.theme).toBe('dark')
  })

  it('UI settings: an out-of-range keepAliveCount is clamped at boot', async () => {
    localStorage.setItem(
      'purdex-ui-settings',
      JSON.stringify({ state: { terminalRenderer: 'webgl', keepAliveCount: 99 }, version: 4 }),
    )

    const { useUISettingsStore, KEEPALIVE_MAX_WEBGL } = await import('./useUISettingsStore')

    expect.soft(useUISettingsStore.persist.hasHydrated()).toBe(true)
    expect.soft(useUISettingsStore.getState().keepAliveCount).toBe(KEEPALIVE_MAX_WEBGL)
  })
})
