import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import {
  clearModuleRegistry,
  getModules,
  getPaneRenderer,
  registerModule,
  type ModuleDefinition,
} from './module-registry'
import { clearNewTabRegistry, getNewTabProviders } from './new-tab-registry'
import { clearSettingsSectionRegistry, getSettingsSections } from './settings-section-registry'
import { clearInterfaceSubsectionRegistry, getInterfaceSubsections } from './interface-subsection-registry'
import { registerBuiltinModules, dispatchSettingsContributions } from './register-modules'
import {
  clearContributions,
  listContributions,
  getContribution,
} from './settings-contribution-registry'

const FakeComponent = () => null

function clearAll() {
  clearModuleRegistry()
  clearNewTabRegistry()
  clearSettingsSectionRegistry()
  clearInterfaceSubsectionRegistry()
  clearContributions()
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

  it('registers tmux agent monitor section in dev mode', () => {
    registerBuiltinModules()
    const monitor = getSettingsSections().find((s) => s.id === 'tmux-agent-monitor')
    expect(monitor).toBeDefined()
    expect(monitor?.label).toBe('settings.section.tmux_agent_monitor')
    expect(monitor?.order).toBe(21)
  })

  it('registers interface section with order=2', () => {
    registerBuiltinModules()
    const sections = getSettingsSections()
    const iface = sections.find((s) => s.id === 'interface')
    expect(iface).toBeDefined()
    expect(iface?.order).toBe(2)
    expect(iface?.component).toBeDefined()
  })

  it('registers interface subsections: new-tab enabled, pane/sidebar disabled', () => {
    registerBuiltinModules()
    const subs = getInterfaceSubsections()
    expect(subs.map((s) => s.id)).toEqual(['new-tab', 'pane', 'sidebar'])
    expect(subs[0].disabled).toBeFalsy()
    expect(subs[1].disabled).toBe(true)
    expect(subs[2].disabled).toBe(true)
  })
})

describe('settings contribution dispatch', () => {
  beforeEach(() => {
    clearAll()
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    clearAll()
  })

  it('dispatches a single declared contribution into the contribution registry', () => {
    const mod: ModuleDefinition = {
      id: 'testmod',
      name: 'Test Module',
      settings: [
        {
          localId: 'general',
          scope: 'purdex',
          order: 0,
          labelKey: 'testmod.general.label',
          component: FakeComponent,
        },
      ],
    }
    registerModule(mod)
    dispatchSettingsContributions()

    const items = listContributions('purdex')
    expect(items.map((c) => c.id)).toContain('testmod.general')
    const entry = getContribution('testmod.general')
    expect(entry).toBeDefined()
    expect(entry?.moduleId).toBe('testmod')
    expect(entry?.localId).toBe('general')
    expect(entry?.id).toBe('testmod.general')
  })

  it('dispatches multiple contributions across different scopes', () => {
    const mod: ModuleDefinition = {
      id: 'multi',
      name: 'Multi Module',
      settings: [
        { localId: 'p1', scope: 'purdex', order: 1, labelKey: 'p1', component: FakeComponent },
        { localId: 'h1', scope: 'host', order: 2, labelKey: 'h1', component: FakeComponent },
        { localId: 'w1', scope: 'workspace', order: 3, labelKey: 'w1', component: FakeComponent },
      ],
    }
    registerModule(mod)
    dispatchSettingsContributions()

    expect(listContributions('purdex').map((c) => c.id)).toEqual(['multi.p1'])
    expect(listContributions('host').map((c) => c.id)).toEqual(['multi.h1'])
    expect(listContributions('workspace').map((c) => c.id)).toEqual(['multi.w1'])
  })

  it('system-fills moduleId and composes id as `${moduleId}.${localId}`', () => {
    const mod: ModuleDefinition = {
      id: 'myscope',
      name: 'My Scope',
      settings: [
        { localId: 'alpha', scope: 'purdex', order: 0, labelKey: 'x', component: FakeComponent },
      ],
    }
    registerModule(mod)
    dispatchSettingsContributions()

    const entry = getContribution('myscope.alpha')
    expect(entry).toBeDefined()
    expect(entry?.moduleId).toBe('myscope')
    expect(entry?.id).toBe('myscope.alpha')
  })

  // --- Invariant I1 ---

  it('I1: non-empty globalConfig + settings scope=purdex → throws naming the module', () => {
    const mod: ModuleDefinition = {
      id: 'dualpurdex',
      name: 'Dual Purdex',
      globalConfig: [{ key: 'x', type: 'string', label: 'x' }],
      settings: [
        { localId: 'g1', scope: 'purdex', order: 0, labelKey: 'g1', component: FakeComponent },
      ],
    }
    registerModule(mod)
    expect(() => dispatchSettingsContributions()).toThrow(/dualpurdex.*globalConfig.*purdex/)
  })

  it('I1: non-empty workspaceConfig + settings scope=workspace → throws naming the module', () => {
    const mod: ModuleDefinition = {
      id: 'dualws',
      name: 'Dual Workspace',
      workspaceConfig: [{ key: 'x', type: 'string', label: 'x' }],
      settings: [
        { localId: 'w1', scope: 'workspace', order: 0, labelKey: 'w1', component: FakeComponent },
      ],
    }
    registerModule(mod)
    expect(() => dispatchSettingsContributions()).toThrow(/dualws.*workspaceConfig.*workspace/)
  })

  it('I1: module with only globalConfig (no settings) does NOT throw', () => {
    const mod: ModuleDefinition = {
      id: 'legacy',
      name: 'Legacy',
      globalConfig: [{ key: 'x', type: 'string', label: 'x' }],
    }
    registerModule(mod)
    expect(() => dispatchSettingsContributions()).not.toThrow()
  })

  it('I1: globalConfig: [] (empty) + settings scope=purdex does NOT throw', () => {
    const mod: ModuleDefinition = {
      id: 'emptylegacy',
      name: 'Empty Legacy',
      globalConfig: [],
      settings: [
        { localId: 'g1', scope: 'purdex', order: 0, labelKey: 'g1', component: FakeComponent },
      ],
    }
    registerModule(mod)
    expect(() => dispatchSettingsContributions()).not.toThrow()
    expect(getContribution('emptylegacy.g1')).toBeDefined()
  })

  it('I1: non-empty globalConfig + settings scope=host does NOT throw (host has no legacy counterpart)', () => {
    const mod: ModuleDefinition = {
      id: 'mixedhost',
      name: 'Mixed Host',
      globalConfig: [{ key: 'x', type: 'string', label: 'x' }],
      settings: [
        { localId: 'h1', scope: 'host', order: 0, labelKey: 'h1', component: FakeComponent },
      ],
    }
    registerModule(mod)
    expect(() => dispatchSettingsContributions()).not.toThrow()
    expect(getContribution('mixedhost.h1')).toBeDefined()
  })

  // --- Re-entry ---

  it('re-entry: dispatching the same module twice with fresh settings declarations stays idempotent', () => {
    registerModule({
      id: 'reentry',
      name: 'Re-entry',
      settings: [
        { localId: 'x', scope: 'purdex', order: 0, labelKey: 'x', component: FakeComponent },
      ],
    })
    dispatchSettingsContributions()

    // Simulate a second pass (e.g. HMR without dispose). Replace the module
    // with a fresh declaration object — per registry §6.4, same id but a
    // different object reference must throw.
    registerModule({
      id: 'reentry',
      name: 'Re-entry',
      settings: [
        { localId: 'x', scope: 'purdex', order: 0, labelKey: 'x', component: FakeComponent },
      ],
    })
    expect(() => dispatchSettingsContributions()).not.toThrow()
    expect(listContributions('purdex').map((c) => c.id)).toEqual(['reentry.x'])
  })

  it('re-entry: after clearAll() (including contributions), registerBuiltinModules() succeeds', () => {
    registerBuiltinModules()
    clearAll()
    expect(() => registerBuiltinModules()).not.toThrow()
  })

  it('re-entry: registerBuiltinModules() is idempotent while no module declares settings', () => {
    // Sanity: PR-1 built-in modules do not yet declare `settings`, so the
    // dispatch pass is a no-op and re-entry should be safe. This guards
    // against a regression where a future built-in module silently adds
    // `settings` without clearing the contribution registry on HMR.
    expect(() => {
      registerBuiltinModules()
      registerBuiltinModules()
    }).not.toThrow()
  })
})
