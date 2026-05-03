import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import {
  clearModuleRegistry,
  getModule,
  getModules,
  getPaneRenderer,
  registerModule,
  type ModuleDefinition,
} from './module-registry'
import { SETTINGS_ORDER } from './settings-order'
import { clearNewTabRegistry, getNewTabProviders } from './new-tab-registry'
import {
  clearSettingsSectionRegistry,
  getSettingsSections,
  registerSettingsSection,
} from './settings-section-registry'
import { clearInterfaceSubsectionRegistry, getInterfaceSubsections } from './interface-subsection-registry'
import { registerBuiltinModules, dispatchSettingsContributions } from './register-modules'
import { resetDeprecationWarningsForTest, resetSettingsContributionsForHmr } from './dispatch-settings-contributions'
import {
  clearContributions,
  listContributions,
  getContribution,
} from './settings-contribution-registry'
import { isModuleOwnedContribution } from './settings-contribution-types'
import { clearHostBuiltinSources } from './host-builtin-sections'
import enLocale from '../locales/en.json'
import zhLocale from '../locales/zh-TW.json'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { PlaceholderSettingsSection } from '../components/settings/PlaceholderSettingsSection'
import { sortDisableableModulesForSwitchboard } from '../components/settings/ModulesSwitchboardSection'

const FakeComponent = () => null

function clearAll() {
  clearModuleRegistry()
  clearNewTabRegistry()
  clearSettingsSectionRegistry()
  clearInterfaceSubsectionRegistry()
  clearContributions()
  clearHostBuiltinSources()
}

describe('registerBuiltinModules', () => {
  beforeEach(() => {
    clearAll()
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
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

  it('registers memory-monitor kind with Performance Monitor display label', () => {
    registerBuiltinModules()

    const monitor = getModules().find((module) => module.id === 'memory-monitor')
    expect(monitor?.name).toBe('Performance Monitor')
    expect(getPaneRenderer('memory-monitor')).toBeDefined()
  })

  it('registers Performance Monitor as a settings page', () => {
    registerBuiltinModules()

    const contribution = listContributions('purdex').find((item) => item.id === 'memory-monitor.performance-monitor')
    expect(contribution?.localId).toBe('performance-monitor')
    // Sidebar short label switched to `settings.section.monitor` (spec §4.5).
    // Inner-page heading + pane label still resolve `performance_monitor.title`.
    expect(contribution?.labelKey).toBe('settings.section.monitor')
  })

  it('hides Performance Monitor settings page when the module is disabled', () => {
    useModuleEnabledStore.getState().setEnabled('memory-monitor', false)
    registerBuiltinModules()

    expect(listContributions('purdex').some((item) => item.id === 'memory-monitor.performance-monitor')).toBe(false)
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

// ---------------------------------------------------------------------------
// §3.3 — Built-in sections flow through legacy adapter into new registry.
// ---------------------------------------------------------------------------

describe('registerBuiltinModules → new contribution registry (PR-2)', () => {
  beforeEach(() => {
    clearAll()
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    clearAll()
  })

  it('built-in legacy sections land under _builtin.legacy-section in purdex scope', () => {
    registerBuiltinModules()
    const contribs = listContributions('purdex')
    const legacyIds = contribs
      .filter((c) => c.moduleId === '_builtin.legacy-section')
      .map((c) => c.localId)

    // Core legacy set always present (no caps gating). After the F3
    // follow-up the `module-config` global wrapper is restored (still
    // needed while `ModuleDefinition.globalConfig` remains public —
    // tracked by #574 for removal alongside HSR PR-5). The reserved
    // `workspace` row stays removed (PR-3 decision 5a — nothing
    // consumes the reserved-items plumbing and the entry itself is dead).
    //
    // Always-on: appearance / terminal / interface / module-config.
    // Electron / dev-environment / tmux-agent-monitor are gated by
    // PlatformCapabilities / import.meta.env.DEV. `sync` was promoted to
    // a structural module by PR-2 (spec §4.3) and is no longer a legacy
    // section; `editor-buffers` was removed when the Editor module
    // migrated to HSR — see R1-3 below.
    for (const id of ['appearance', 'terminal', 'interface', 'module-config']) {
      expect(legacyIds).toContain(id)
    }
    expect(legacyIds.length).toBeGreaterThanOrEqual(4)
    // Sync no longer surfaces under the legacy adapter namespace.
    expect(legacyIds).not.toContain('sync')
  })

  it('PR-3: reserved workspace section is no longer registered', () => {
    registerBuiltinModules()
    // Removed by PR-3 per plan decision 5a: `workspace` was the sole reserved
    // entry; after its removal the reserved-items plumbing has been dropped.
    const contribs = listContributions('purdex')
    expect(contribs.find((c) => c.localId === 'workspace')).toBeUndefined()
    expect(getSettingsSections().find((s) => s.id === 'workspace')).toBeUndefined()
  })

  it('F3: global module-config section is registered (restored until globalConfig deprecates — #574)', () => {
    registerBuiltinModules()
    // F3 follow-up restored `module-config` — removing it left
    // `ModuleDefinition.globalConfig` API live-but-unreachable (silent
    // dead-end). Deferred removal to HSR PR-5 tracked by #574.
    //
    // Order moved 8 → 10 by settings-architecture-fix PR-1 (spec §4.1.4)
    // so the Modules Switchboard sits at the top of the modules group
    // instead of colliding with link-detect. Sourced from SETTINGS_ORDER
    // constants now.
    const contribs = listContributions('purdex')
    const entry = contribs.find((c) => c.localId === 'module-config')
    expect(entry).toBeDefined()
    expect(entry?.order).toBe(10)
    expect(getSettingsSections().find((s) => s.id === 'module-config')).toBeDefined()
  })

  it('dispatch timing: legacy contributions survive repeated dispatches', () => {
    registerBuiltinModules()
    const before = listContributions('purdex')
      .filter((c) => c.moduleId === '_builtin.legacy-section')
      .map((c) => c.id)
    expect(before.length).toBeGreaterThan(0)

    // Re-push legacy sections (simulating HMR or additional
    // registerBuiltinModules call) before dispatching again. Real flow
    // guarantees this because registerBuiltinModules runs ahead of dispatch.
    registerSettingsSection({
      id: 'appearance',
      label: 'settings.section.appearance',
      order: 0,
      component: () => null,
    })
    dispatchSettingsContributions()

    const after = listContributions('purdex')
      .filter((c) => c.moduleId === '_builtin.legacy-section')
      .map((c) => c.id)
    expect(after).toContain('_builtin.legacy-section.appearance')
  })

  it('I1 guard remains intact — registerBuiltinModules does not throw', () => {
    expect(() => registerBuiltinModules()).not.toThrow()
  })

  it('getSettingsSections() legacy view stays consistent with new registry', () => {
    registerBuiltinModules()
    const legacyView = getSettingsSections().map((s) => s.id)
    // Legacy view is the filtered `_builtin.legacy-section.*` entries.
    // After the F3 follow-up `module-config` is back (tracked by #574 for
    // removal alongside globalConfig deprecation).  Reserved `workspace`
    // stays removed. PR-2 (spec §4.3) promoted `sync` to a structural
    // module so it no longer appears in the legacy view.
    for (const id of ['appearance', 'terminal', 'interface', 'module-config']) {
      expect(legacyView).toContain(id)
    }
    expect(legacyView).not.toContain('workspace')
    expect(legacyView).not.toContain('sync')
    // Post-HSR: legacy view no longer carries editor-buffers (migrated to
    // editor module's HSR settings — R1-3).
    expect(legacyView).not.toContain('editor-buffers')
  })
})

describe('ModuleDefinition.globalConfig / workspaceConfig deprecation (PR-5)', () => {
  let warnSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    clearAll()
    resetDeprecationWarningsForTest()
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
  })

  afterEach(() => {
    warnSpy.mockRestore()
    clearAll()
  })

  it('warns when a non-files module uses globalConfig', () => {
    registerModule({
      id: 'fakemod',
      name: 'Fake',
      globalConfig: [{ key: 'x', type: 'string', label: 'x' }],
    })
    dispatchSettingsContributions()
    const msgs = (warnSpy.mock.calls as unknown[][]).map((c) => String(c[0]))
    expect(msgs.some((m: string) => m.includes('fakemod') && m.includes('deprecated'))).toBe(true)
  })

  it('warns when a non-files module uses workspaceConfig', () => {
    registerModule({
      id: 'fakews',
      name: 'Fake WS',
      workspaceConfig: [{ key: 'x', type: 'string', label: 'x' }],
    })
    dispatchSettingsContributions()
    const msgs = (warnSpy.mock.calls as unknown[][]).map((c) => String(c[0]))
    expect(msgs.some((m: string) => m.includes('fakews') && m.includes('deprecated'))).toBe(true)
  })

  it('does NOT emit any deprecation warning for the real Files bootstrap', () => {
    registerBuiltinModules()
    const msgs = (warnSpy.mock.calls as unknown[][]).map((c) => String(c[0]))
    expect(msgs.some((m) => m.includes('files') && m.includes('deprecated'))).toBe(false)
  })

  it('does NOT warn for modules using new `settings` field', () => {
    registerModule({
      id: 'newmod',
      name: 'New',
      settings: [
        { localId: 'x', scope: 'purdex', order: 0, labelKey: 'x', component: FakeComponent },
      ],
    })
    dispatchSettingsContributions()
    const msgs = (warnSpy.mock.calls as unknown[][]).map((c) => String(c[0]))
    expect(msgs.some((m: string) => m.includes('newmod') && m.includes('deprecated'))).toBe(false)
  })

  it('de-dupes: repeated dispatch for the same module/scope warns only once', () => {
    registerModule({
      id: 'dedupe',
      name: 'Dedupe',
      globalConfig: [{ key: 'x', type: 'string', label: 'x' }],
    })
    dispatchSettingsContributions()
    dispatchSettingsContributions()
    dispatchSettingsContributions()
    const hits = (warnSpy.mock.calls as unknown[][]).filter((c) => String(c[0]).includes('dedupe')).length
    expect(hits).toBe(1)
  })

  it('HMR reset clears the dedupe set so warnings re-emit after reload (R2 codex)', () => {
    registerModule({
      id: 'hmrmod',
      name: 'HMR Mod',
      globalConfig: [{ key: 'x', type: 'string', label: 'x' }],
    })
    dispatchSettingsContributions()
    const firstHits = (warnSpy.mock.calls as unknown[][]).filter(
      (c) => String(c[0]).includes('hmrmod') && String(c[0]).includes('deprecated'),
    ).length
    expect(firstHits).toBe(1)

    // Simulate HMR dispose + re-register from the rebuilt module graph
    resetSettingsContributionsForHmr()
    registerModule({
      id: 'hmrmod',
      name: 'HMR Mod',
      globalConfig: [{ key: 'x', type: 'string', label: 'x' }],
    })
    dispatchSettingsContributions()
    const afterHmrHits = (warnSpy.mock.calls as unknown[][]).filter(
      (c) => String(c[0]).includes('hmrmod') && String(c[0]).includes('deprecated'),
    ).length
    expect(afterHmrHits).toBe(2)
  })

  it('defers the warning until after a successful dispatch (R1 codex)', () => {
    // Module with I1 violation: both globalConfig AND settings[scope='purdex']
    registerModule({
      id: 'retrymod',
      name: 'Retry',
      globalConfig: [{ key: 'x', type: 'string', label: 'x' }],
      settings: [
        { localId: 'x', scope: 'purdex', order: 0, labelKey: 'x', component: FakeComponent },
      ],
    })
    expect(() => dispatchSettingsContributions()).toThrow()
    const beforeHits = (warnSpy.mock.calls as unknown[][]).filter(
      (c) => String(c[0]).includes('retrymod') && String(c[0]).includes('deprecated'),
    ).length
    // Prior to the fix, the warn ran BEFORE validation and burnt the dedupe
    // key even on failed dispatches — the retry would then observe no warn.
    // With the fix, the failed dispatch emits nothing.
    expect(beforeHits).toBe(0)

    // Author fixes the conflict and retries. The retry is the first
    // successful dispatch, so it should emit the deprecation warning.
    registerModule({
      id: 'retrymod',
      name: 'Retry',
      globalConfig: [{ key: 'x', type: 'string', label: 'x' }],
    })
    dispatchSettingsContributions()
    const afterHits = (warnSpy.mock.calls as unknown[][]).filter(
      (c) => String(c[0]).includes('retrymod') && String(c[0]).includes('deprecated'),
    ).length
    expect(afterHits).toBe(1)
  })
})

describe('Files module — SR-2 fix (disable filter via settings contribution)', () => {
  beforeEach(() => {
    clearAll()
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  })

  afterEach(() => {
    clearAll()
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  })

  it('Files registers with disableable: true + descriptionKey + no workspaceConfig', () => {
    registerBuiltinModules()
    const filesMod = getModule('files')!
    expect(filesMod.disableable).toBe(true)
    expect(filesMod.descriptionKey).toBe('modules.files.description')
    expect(filesMod.workspaceConfig).toBeUndefined()
  })

  it('Files contributes a workspace-scope settings entry with correct localId/order', () => {
    registerBuiltinModules()
    const list = listContributions('workspace')
    const filesEntry = list.find((c) => c.id === 'files.workspace-files')
    expect(filesEntry).toBeDefined()
    expect(filesEntry?.scope).toBe('workspace')
    expect(filesEntry?.order).toBe(SETTINGS_ORDER.WORKSPACE_FILES)
    expect(filesEntry?.labelKey).toBe('settings.section.files_workspace')
    expect(filesEntry?.moduleId).toBe('files')
  })

  // CRITICAL: same-test before/after compare to avoid false-green from
  // "Files never had a workspace contribution to begin with" (codex R1 P0).
  it('reload-after-disable: Files contribution present when enabled, absent when disabled before bootstrap', () => {
    // Step 1 — Files enabled (default) → Files contribution present
    registerBuiltinModules()
    const enabledList = listContributions('workspace')
    expect(enabledList.find((c) => c.id === 'files.workspace-files')).toBeDefined()

    // Step 2 — reset state and bootstrap with Files persisted-disabled
    clearAll()
    useModuleEnabledStore.setState({ enabled: { files: false }, baseline: null })
    registerBuiltinModules()
    const disabledList = listContributions('workspace')
    expect(disabledList.find((c) => c.id === 'files.workspace-files')).toBeUndefined()
  })
})

// ---------------------------------------------------------------------------
// Commit 1: Editor HSR migration — the built-in Editor module's settings
// now owns 3 HSR contributions (editor / workspace-home-path /
// host-home-path) and the legacy `editor-buffers` registerSettingsSection
// is gone.  Exercised both through the module registry surface and through
// the two locale JSONs to prevent silent key drift.
// ---------------------------------------------------------------------------

describe('Commit 1: Editor HSR migration', () => {
  beforeEach(() => {
    clearAll()
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    clearAll()
  })

  it('R1-1: editor module declares exactly 3 settings entries after PR-2 consolidation', () => {
    // Spec §4.2 (PR-2): the previously separate `link-detect` and
    // `open-behavior` purdex contributions were collapsed into inline
    // segments inside `EditorPurdexSettingsSection`. The editor module
    // now exposes one purdex entry (`editor`) plus the existing
    // workspace-home-path / host-home-path scopes (HSR).
    registerBuiltinModules()
    const editor = getModules().find((m) => m.id === 'editor')
    expect(editor).toBeDefined()
    expect(editor?.settings?.length).toBe(3)
    const localIds = editor?.settings?.map((s) => s.localId).sort()
    expect(localIds).toEqual([
      'editor',
      'host-home-path',
      'workspace-home-path',
    ])
  })

  it('R1-2: editor.editor contribution is purdex-scope', () => {
    registerBuiltinModules()
    const entry = getContribution('editor.editor')
    expect(entry).toBeDefined()
    expect(entry?.scope).toBe('purdex')
  })

  it('R1-3: legacy editor-buffers contribution is gone from every scope', () => {
    registerBuiltinModules()
    expect(getSettingsSections().find((s) => s.id === 'editor-buffers')).toBeUndefined()
    const purdex = listContributions('purdex')
    expect(purdex.find((c) => c.localId === 'editor-buffers')).toBeUndefined()
    expect(purdex.find((c) => c.id.endsWith('.editor-buffers'))).toBeUndefined()
  })

  it('R1-4: both locales carry the new settings.section.editor key', () => {
    const en = enLocale as Record<string, string>
    const zh = zhLocale as Record<string, string>
    expect(en['settings.section.editor']).toBe('Editor')
    expect(zh['settings.section.editor']).toBe('編輯器')
    // The old `editor_buffers` key must be gone in both.
    expect(en['settings.section.editor_buffers']).toBeUndefined()
    expect(zh['settings.section.editor_buffers']).toBeUndefined()
  })

  it('R1-5: editor.editor contribution is module-owned (carries the puzzle-piece marker)', () => {
    registerBuiltinModules()
    const entry = getContribution('editor.editor')
    expect(entry).toBeDefined()
    expect(isModuleOwnedContribution(entry!)).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// Commit 2: EditorBuffersPane + NewTab entry (spec §4.5 / §4.7 / §4.9.3).
// Editor module now owns:
//   - A new `editor-buffers` pane kind (registered via `panes: [...]`).
//   - A new NewTab provider `editor-buffers` with `moduleId: 'editor'`.
//   - The existing `editor` NewTab provider also sets `moduleId: 'editor'`
//     so the Switchboard-driven filter catches both at once.
// ---------------------------------------------------------------------------

describe('Commit 2: EditorBuffersPane + NewTab entry', () => {
  beforeEach(() => {
    clearAll()
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    clearAll()
  })

  it('N2-1: editor-buffers NewTab provider is registered with moduleId=editor', () => {
    registerBuiltinModules()
    const provider = getNewTabProviders().find((p) => p.id === 'editor-buffers')
    expect(provider).toBeDefined()
    expect(provider?.moduleId).toBe('editor')
    expect(provider?.icon).toBe('Stack')
  })

  it('N2-2: editor-buffers provider order sits immediately after the editor provider', () => {
    registerBuiltinModules()
    const providers = getNewTabProviders()
    const editor = providers.find((p) => p.id === 'editor')
    const buffers = providers.find((p) => p.id === 'editor-buffers')
    expect(editor).toBeDefined()
    expect(buffers).toBeDefined()
    expect(buffers!.order).toBeGreaterThan(editor!.order)
  })

  it('editor NewTab provider is tagged with moduleId=editor (so A2-4 filter applies)', () => {
    registerBuiltinModules()
    const editor = getNewTabProviders().find((p) => p.id === 'editor')
    expect(editor?.moduleId).toBe('editor')
  })

  it('editor module declares the editor-buffers pane kind', () => {
    registerBuiltinModules()
    const editorMod = getModules().find((m) => m.id === 'editor')
    expect(editorMod?.panes?.find((p) => p.kind === 'editor-buffers')).toBeDefined()
  })
})

// ---------------------------------------------------------------------------
// Settings sidebar alignment (spec §3 I1 / §6 T3-T7 + T9 locale guard).
// Every disableable module must declare at least one purdex-scope settings
// contribution so the Settings sidebar mirrors the Modules Switchboard.
// ---------------------------------------------------------------------------

describe('Settings sidebar alignment (spec §3 I1)', () => {
  beforeEach(() => {
    clearAll()
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
    clearAll()
  })

  it('T3 / I1: every disableable module declares at least one purdex-scope settings contribution', () => {
    registerBuiltinModules()
    const violations = getModules()
      .filter((m) => m.disableable === true)
      .filter((m) => !(m.settings ?? []).some((s) => s.scope === 'purdex'))
      .map((m) => m.id)
    expect(violations).toEqual([])
  })

  it('T4: browser registers a purdex placeholder with settings.section.browser label', () => {
    registerBuiltinModules()
    const browser = getModule('browser')
    const purdex = browser?.settings?.find((s) => s.scope === 'purdex')
    expect(purdex).toBeDefined()
    expect(purdex?.labelKey).toBe('settings.section.browser')
    expect(purdex?.component).toBe(PlaceholderSettingsSection)
  })

  it('T5: files registers a purdex placeholder with settings.section.files label', () => {
    registerBuiltinModules()
    const files = getModule('files')
    const purdex = files?.settings?.find((s) => s.scope === 'purdex')
    expect(purdex).toBeDefined()
    expect(purdex?.labelKey).toBe('settings.section.files')
    expect(purdex?.component).toBe(PlaceholderSettingsSection)
  })

  it('T6: memory-monitor purdex labelKey switched to settings.section.monitor', () => {
    registerBuiltinModules()
    const m = getModule('memory-monitor')
    const purdex = m?.settings?.find((s) => s.scope === 'purdex')
    expect(purdex?.labelKey).toBe('settings.section.monitor')
  })

  it('T7: quick-commands purdex labelKey switched to settings.section.commands', () => {
    registerBuiltinModules()
    const m = getModule('quick-commands')
    const purdex = m?.settings?.find((s) => s.scope === 'purdex')
    expect(purdex?.labelKey).toBe('settings.section.commands')
  })

  it('T9 / F6: locale JSON has new short-label keys + placeholder string in en + zh-TW', () => {
    const required = [
      'settings.section.browser',
      'settings.section.commands',
      'settings.section.files',
      'settings.section.monitor',
      'settings.module.no_purdex_settings',
    ] as const
    for (const k of required) {
      expect((enLocale as Record<string, string>)[k]).toBeTruthy()
      expect((zhLocale as Record<string, string>)[k]).toBeTruthy()
    }
  })

  it('T2b / §I2: switchboard order matches disableable purdex contributions ASC by min order', () => {
    registerBuiltinModules()

    // Expected: every disableable module's first purdex-scope contribution
    // order, sorted ASC by that order — exactly mirrors the Settings sidebar
    // relative position for the disableable subset (Sync excluded; not
    // disableable). Computed independently of the Switchboard sort logic
    // so the two derivations cross-check each other.
    const expected = listContributions('purdex')
      .filter((c) => {
        const m = getModule(c.moduleId)
        return m?.disableable === true
      })
      .sort((a, b) => a.order - b.order)
      .reduce<string[]>((acc, c) => {
        if (!acc.includes(c.moduleId)) acc.push(c.moduleId)
        return acc
      }, [])

    const actual = sortDisableableModulesForSwitchboard(getModules()).map((m) => m.id)
    expect(actual).toEqual(expected)
  })

  // -- Round-2 adversarial review fixes ------------------------------------

  it('R2-F2 / I1 enforcement: dispatch throws when a disableable module has no purdex contribution', () => {
    // Authoring error: someone marks a module disableable but forgets the
    // sidebar entry. Must fail loudly at dispatch time, not silently leave
    // the module visible only in the Switchboard.
    registerModule({
      id: 'naked-disableable',
      name: 'Naked Disableable',
      disableable: true,
      // no settings: [...] entry
    })
    // Defaults a fresh disableable module to enabled (per useModuleEnabledStore
    // initial-snapshot semantics) so the dispatch path exercises the I1 check.
    expect(() => dispatchSettingsContributions()).toThrow(
      /naked-disableable.*purdex.*§I1/,
    )
  })

  it('R2-F2 / I1 enforcement: dispatch passes when a disableable module has a workspace + purdex contribution (Files shape)', () => {
    registerModule({
      id: 'files-shape',
      name: 'Files Shape',
      disableable: true,
      settings: [
        { localId: 'ws', scope: 'workspace', order: 0, labelKey: 'a', component: FakeComponent },
        { localId: 'pdx', scope: 'purdex', order: 99, labelKey: 'b', component: FakeComponent },
      ],
    })
    expect(() => dispatchSettingsContributions()).not.toThrow()
  })

  it('R2-F1 / shared comparator: equal-order tie-break by moduleId on both surfaces (sidebar + switchboard)', () => {
    // Two disableable modules sharing the same purdex order. The sidebar
    // sorts contributions and the switchboard sorts modules; without a
    // shared tie-breaker the relative order diverges. Both surfaces must
    // agree because user-visible navigation is the same conceptual list.
    registerModule({
      id: 'zeta',
      name: 'Zeta', // intentionally last alphabetically by display name
      disableable: true,
      settings: [
        { localId: 'zeta', scope: 'purdex', order: 50, labelKey: 'zeta', component: FakeComponent },
      ],
    })
    registerModule({
      id: 'alpha',
      name: 'Alpha',
      disableable: true,
      settings: [
        { localId: 'alpha', scope: 'purdex', order: 50, labelKey: 'alpha', component: FakeComponent },
      ],
    })
    dispatchSettingsContributions()

    // Sidebar derivation — `listContributions('purdex')` is the canonical
    // source of truth for sidebar order (deterministic comparator lives in
    // settings-contribution-registry.ts).
    const sidebarRelativeIds = listContributions('purdex')
      .filter((c) => {
        const m = getModule(c.moduleId)
        return m?.disableable === true
      })
      .map((c) => c.moduleId)

    // Switchboard derivation — same logic as
    // sortDisableableModulesForSwitchboard().
    const switchboardIds = sortDisableableModulesForSwitchboard(getModules()).map((m) => m.id)

    // Both surfaces must put alpha before zeta (moduleId tie-break),
    // ignoring the display name "Zeta"/"Alpha" — moduleId is canonical.
    expect(sidebarRelativeIds[0]).toBe('alpha')
    expect(sidebarRelativeIds[1]).toBe('zeta')
    expect(switchboardIds.indexOf('alpha')).toBeLessThan(switchboardIds.indexOf('zeta'))
    // And they must AGREE (same relative order on both surfaces).
    expect(switchboardIds.filter((id) => id === 'alpha' || id === 'zeta'))
      .toEqual(['alpha', 'zeta'])
  })

  it('R3-P2 / centralized comparator: listContributions returns deterministic order so SettingsPage default-mount agrees with sidebar', () => {
    // Round-3 finding R3-P2 (codex review-moq3...): SettingsSidebar applied
    // a local tie-break, but GlobalSettingsPage in SettingsPage.tsx mounts
    // `sections[0]` from `listContributions('purdex')` directly — without
    // the same tie-break the auto-mounted default page can be a different
    // contribution than the sidebar's first visible row when two purdex
    // entries share the same `order`. Fix: deterministic comparator now
    // lives inside `listContributions`, so every consumer sees the same
    // (order, moduleId, localId) sequence.
    registerModule({
      id: 'beta',
      name: 'Beta',
      settings: [
        { localId: 'b1', scope: 'purdex', order: 99, labelKey: 'b1', component: FakeComponent },
      ],
    })
    registerModule({
      id: 'alphax',
      name: 'AlphaX',
      settings: [
        { localId: 'a1', scope: 'purdex', order: 99, labelKey: 'a1', component: FakeComponent },
      ],
    })
    dispatchSettingsContributions()

    // First call (sidebar) and second call (SettingsPage default-mount) — both
    // arrays must yield the same `localId` ordering for the same-order pair.
    const callA = listContributions('purdex').map((c) => c.localId)
    const callB = listContributions('purdex').map((c) => c.localId)
    expect(callA).toEqual(callB)
    // moduleId tie-break: 'alphax' < 'beta'.
    expect(callA.indexOf('a1')).toBeLessThan(callA.indexOf('b1'))
  })
})
