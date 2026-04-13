import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { clearModuleRegistry, getModules, getPaneRenderer } from './module-registry'
import { clearNewTabRegistry, getNewTabProviders } from './new-tab-registry'
import { clearSettingsSectionRegistry, getSettingsSections } from './settings-section-registry'
import { registerBuiltinModules } from './register-modules'

function clearAll() {
  clearModuleRegistry()
  clearNewTabRegistry()
  clearSettingsSectionRegistry()
}

describe('registerBuiltinModules', () => {
  beforeEach(() => {
    clearAll()
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    clearAll()
  })

  it('registers all built-in modules', () => {
    registerBuiltinModules()
    const modules = getModules()
    expect(modules.length).toBeGreaterThanOrEqual(8)
    expect(getPaneRenderer('tmux-session')).toBeDefined()
    expect(getPaneRenderer('new-tab')).toBeDefined()
    expect(getPaneRenderer('browser')).toBeDefined()
    expect(getPaneRenderer('hosts')).toBeDefined()
  })

  it('registers browser provider as disabled when no electronAPI', () => {
    registerBuiltinModules()
    const browser = getNewTabProviders().find((p) => p.id === 'browser')
    expect(browser).toBeDefined()
    expect(browser?.disabled).toBe(true)
    expect(browser?.disabledReason).toBe('browser.requires_app')
  })

  it('registers browser provider as enabled when electronAPI present', () => {
    ;(window as unknown as Record<string, unknown>).electronAPI = { tearOffTab: async () => {} }
    registerBuiltinModules()
    const browser = getNewTabProviders().find((p) => p.id === 'browser')
    expect(browser).toBeDefined()
    expect(browser?.disabled).toBe(false)
  })

  it('does not register electron section when no electronAPI', () => {
    registerBuiltinModules()
    const electron = getSettingsSections().find((s) => s.id === 'electron')
    expect(electron).toBeUndefined()
  })

  it('registers electron section when electronAPI present', () => {
    ;(window as unknown as Record<string, unknown>).electronAPI = { tearOffTab: async () => {} }
    registerBuiltinModules()
    const electron = getSettingsSections().find((s) => s.id === 'electron')
    expect(electron).toBeDefined()
  })
})
