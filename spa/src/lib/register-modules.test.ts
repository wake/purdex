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
    // Always-on: appearance / terminal / interface / sync / module-config.
    // Electron / dev-environment / tmux-agent-monitor are gated by
    // PlatformCapabilities / import.meta.env.DEV.
    // `editor-buffers` was removed when the Editor module migrated to HSR —
    // see R1-3 below.
    for (const id of ['appearance', 'terminal', 'interface', 'sync', 'module-config']) {
      expect(legacyIds).toContain(id)
    }
    expect(legacyIds.length).toBeGreaterThanOrEqual(5)
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
    const contribs = listContributions('purdex')
    const entry = contribs.find((c) => c.localId === 'module-config')
    expect(entry).toBeDefined()
    expect(entry?.order).toBe(8)
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
    // stays removed.
    for (const id of ['appearance', 'terminal', 'interface', 'sync', 'module-config']) {
      expect(legacyView).toContain(id)
    }
    expect(legacyView).not.toContain('workspace')
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

  it('does NOT warn for files module (exempted during transition)', () => {
    registerModule({
      id: 'files',
      name: 'Files',
      workspaceConfig: [{ key: 'projectPath', type: 'string', label: '專案路徑' }],
    })
    dispatchSettingsContributions()
    const msgs = (warnSpy.mock.calls as unknown[][]).map((c) => String(c[0]))
    expect(msgs.some((m: string) => m.includes('files') && m.includes('deprecated'))).toBe(false)
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

  it('R1-1: editor module declares exactly 4 settings entries (3 HSR + link-detect)', () => {
    // P3 added a 4th entry under purdex scope (settings.editor.link_detect)
    // that migrated from the Terminal section. The Editor module remains
    // the owner of file-path link detection toggles.
    registerBuiltinModules()
    const editor = getModules().find((m) => m.id === 'editor')
    expect(editor).toBeDefined()
    expect(editor?.settings?.length).toBe(4)
    const localIds = editor?.settings?.map((s) => s.localId).sort()
    expect(localIds).toEqual(['editor', 'host-home-path', 'link-detect', 'workspace-home-path'])
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
