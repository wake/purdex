import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

vi.mock('../features/workspace/generated/icon-loader', () => ({
  ALL_ICON_NAMES: [],
  iconLoaders: {},
}))

import { clearNewTabRegistry, getNewTabProviders } from './new-tab-registry'
import { clearPaneRegistry } from './pane-registry'
import { clearSettingsSectionRegistry, getSettingsSections } from './settings-section-registry'
import { registerBuiltinPanes } from './register-panes'

describe('browser provider registration', () => {
  beforeEach(() => {
    clearNewTabRegistry()
    clearPaneRegistry()
    clearSettingsSectionRegistry()
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    clearNewTabRegistry()
    clearPaneRegistry()
    clearSettingsSectionRegistry()
  })

  it('registers browser provider as disabled when no electronAPI', () => {
    registerBuiltinPanes()
    const browser = getNewTabProviders().find((p) => p.id === 'browser')
    expect(browser).toBeDefined()
    expect(browser?.disabled).toBe(true)
    expect(browser?.disabledReason).toBe('browser.requires_app')
  })

  it('registers browser provider as enabled when electronAPI present', () => {
    ;(window as unknown as Record<string, unknown>).electronAPI = { tearOffTab: async () => {} }
    registerBuiltinPanes()
    const browser = getNewTabProviders().find((p) => p.id === 'browser')
    expect(browser).toBeDefined()
    expect(browser?.disabled).toBe(false)
  })
})


describe('electron settings section registration', () => {
  beforeEach(() => {
    clearNewTabRegistry()
    clearPaneRegistry()
    clearSettingsSectionRegistry()
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    clearNewTabRegistry()
    clearPaneRegistry()
    clearSettingsSectionRegistry()
  })

  it('does not register electron section when no electronAPI', () => {
    registerBuiltinPanes()
    const electron = getSettingsSections().find((s) => s.id === 'electron')
    expect(electron).toBeUndefined()
  })

  it('registers electron section when electronAPI present', () => {
    ;(window as unknown as Record<string, unknown>).electronAPI = { tearOffTab: async () => {} }
    registerBuiltinPanes()
    const electron = getSettingsSections().find((s) => s.id === 'electron')
    expect(electron).toBeDefined()
  })
})
