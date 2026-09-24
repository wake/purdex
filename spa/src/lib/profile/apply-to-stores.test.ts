// spa/src/lib/profile/apply-to-stores.test.ts
//
// Two halves. "Premises" pins, against the REAL stores and the real zustand
// persist middleware, what `applySectionToStores` relies on: a `setState` has
// reached localStorage by the next line, and `persist.rehydrate()` reads it back
// through the store's own `merge` / `onRehydrateStorage` — synchronously, without
// dropping non-persisted state. If one of these goes red after a zustand or store
// change, the apply path is no longer sound and needs an adapter for that store.
// The second half tests the function itself.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import type { HostConfig } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useEditorSettingsStore } from '../../stores/useEditorSettingsStore'
import { useThemeStore } from '../../stores/useThemeStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
import { useNewTabLayoutStore } from '../../stores/useNewTabLayoutStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../../stores/useLocalProfilesStore'
import { deleteHostCascade } from '../host-lifecycle'

// A spy that runs the real cascade: an apply that must not cascade is checked by its calls.
vi.mock('../host-lifecycle', async (importOriginal) => {
  const real = await importOriginal<typeof import('../host-lifecycle')>()
  return { ...real, deleteHostCascade: vi.fn(real.deleteHostCascade) }
})
import { renderHook } from '@testing-library/react'
import { HOST_RERESOLVE_LOCK_OWNER, HOST_RERESOLVE_RETRY_MS, __resetHostReresolveForTest, requestHostReresolve } from '../host-reresolve'
import { useNewTabBootstrap } from '../../hooks/useNewTabBootstrap'
import { clearNewTabRegistry, registerNewTabProviderSource } from '../new-tab-registry'
import { createHostSessionProviderSource } from '../session-new-tab-providers'
import { createHeadlessProviderSource } from '../headless-new-tab-providers'
import { getTheme, unregisterTheme } from '../theme-registry'
import { registerBuiltinThemes } from '../register-themes'
import { getLocale, unregisterLocale } from '../locale-registry'
import { STORAGE_KEYS } from '../storage'
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import { hashSection } from './hash'
import { masterWorkspaceIds as masterWorkspaceIdsOrNull } from './master-world'
import { buildHostsSection, buildSettingsSection, buildTabsSection, buildWorkspacesSection, stripSizes } from './sections'
import type { HostsPayload, SettingsPayload, TabsPayload, WorkspacesPayload } from './types'
import { INVALID_REASONS, applySectionToStores, readSettingsSources } from './apply-to-stores'
import { identityOfSync, syncIdOfSync } from './host-identity'
import { isRefShownNow, setHostShown } from '../shown-hosts'

// === fixtures ===

/** Every test in this file that asks has the master world settled. */
function masterWorkspaceIds(): ReadonlySet<string> {
  const ids = masterWorkspaceIdsOrNull()
  if (ids === null) throw new Error('the master world is unsettled')
  return ids
}

const M = 'host-master'
const H2 = 'host-two'

function host(id: string, over: Partial<HostConfig> = {}): HostConfig {
  return { id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, ...over }
}

function tmuxLeaf(paneId: string, hostId: string): PaneLayout {
  return { type: 'leaf', pane: { id: paneId, content: { kind: 'tmux-session', hostId, sessionCode: `c-${paneId}`, mode: 'terminal', cachedName: paneId, tmuxInstance: 'inst' } } }
}

function tab(id: string, layout: PaneLayout = tmuxLeaf(`p-${id}`, M)): Tab {
  return { id, pinned: false, locked: false, createdAt: 1, layout }
}

function ws(id: string, tabs: string[], activeTabId: string | null = tabs[0] ?? null): Workspace {
  return { id, name: id.toUpperCase(), tabs, activeTabId }
}

const persistedOf = (key: string): Record<string, unknown> => (JSON.parse(localStorage.getItem(key) ?? '{}') as { state: Record<string, unknown> }).state

const UI_DEFAULTS = useUISettingsStore.getInitialState()
const LAYOUT_DEFAULTS = useLayoutStore.getInitialState()
const EDITOR_DEFAULTS = useEditorSettingsStore.getInitialState()

function resetStores(): void {
  useHostStore.setState({ hosts: { [M]: host(M), [H2]: host(H2, { ip: '10.0.0.2', order: 1 }) }, hostOrder: [M, H2], activeHostId: H2, devHostId: H2, runtime: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useUISettingsStore.setState({ terminalRenderer: UI_DEFAULTS.terminalRenderer, keepAliveCount: UI_DEFAULTS.keepAliveCount, terminalSettingsVersion: 0, dynamicTabName: UI_DEFAULTS.dynamicTabName })
  useLayoutStore.setState({ tabPosition: LAYOUT_DEFAULTS.tabPosition, activityBarWidth: LAYOUT_DEFAULTS.activityBarWidth })
  useEditorSettingsStore.setState({ fontSize: EDITOR_DEFAULTS.fontSize, tabSize: EDITOR_DEFAULTS.tabSize })
  useThemeStore.setState({ activeThemeId: 'dark', customThemes: {} })
  useI18nStore.getState().setLocale('en')
  useI18nStore.setState({ customLocales: {} })
  useWorkspaceSettingsStore.setState({ workspaces: {} })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, agentTypes: {}, models: {} })
  useExecutionStore.setState({ executions: {} })
  useExecutionListStore.setState({ byHost: {} })
  useNexHostStore.setState({ byHost: {} })
  useHostSettingsStore.setState({ hosts: {} })
  useHostLookStore.setState({ looks: {} })
  useShownHostsStore.setState({ ids: [] })
  // Reset too: since every apply ends with a re-resolve pass, a column one test left behind would move in the next.
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
}

beforeAll(() => registerBuiltinThemes())

beforeEach(() => {
  resetStores()
})

afterEach(() => {
  vi.restoreAllMocks()
  unregisterTheme('custom-1')
  unregisterLocale('custom-loc')
})

// === Premises ===

describe('premises: setState → persist.rehydrate() is a sound way to land a patch', () => {
  it('(a) a setState has reached localStorage by the next line', () => {
    useUISettingsStore.setState({ dynamicTabName: !UI_DEFAULTS.dynamicTabName })
    expect(persistedOf(STORAGE_KEYS.UI_SETTINGS).dynamicTabName).toBe(!UI_DEFAULTS.dynamicTabName)
    useHostStore.setState({ hostOrder: [H2, M] })
    expect(persistedOf(STORAGE_KEYS.HOSTS).hostOrder).toEqual([H2, M])
    useEditorSettingsStore.setState({ tabSize: 8 })
    expect(persistedOf(STORAGE_KEYS.EDITOR_SETTINGS).tabSize).toBe(8)
  })

  it('(b) i18n: rehydrate rebuilds `t` in the applied language and keeps the applied activeLocaleId', async () => {
    expect(useI18nStore.getState().t('common.cancel')).toBe('Cancel')
    useI18nStore.setState({ activeLocaleId: 'zh-TW' })
    expect(useI18nStore.getState().t('common.cancel')).toBe('Cancel') // the bare patch leaves the old translator
    await useI18nStore.persist.rehydrate()
    expect(useI18nStore.getState().activeLocaleId).toBe('zh-TW') // not re-detected from navigator.languages
    expect(useI18nStore.getState().t('common.cancel')).toBe('取消')
    expect(document.documentElement.lang).toBe('zh-TW')
  })

  it('(b) theme: rehydrate moves the DOM attribute and registers an arriving custom theme', async () => {
    const custom = { id: 'custom-1', name: 'Mine', tokens: getTheme('dark')!.tokens, builtin: false }
    useThemeStore.setState({ activeThemeId: 'custom-1', customThemes: { 'custom-1': custom } })
    expect(getTheme('custom-1')).toBeUndefined()
    await useThemeStore.persist.rehydrate()
    expect(document.documentElement.dataset.theme).toBe('custom-1')
    expect(getTheme('custom-1')).toEqual(custom)
    expect(useThemeStore.getState().activeThemeId).toBe('custom-1')
  })

  it('(b) layout: rehydrate heals tabPosition=left + narrow activity bar to wide', async () => {
    useLayoutStore.setState({ activityBarWidth: 'narrow' })
    useLayoutStore.setState({ tabPosition: 'left' })
    expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
    await useLayoutStore.persist.rehydrate()
    expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
    expect(useLayoutStore.getState().tabPosition).toBe('left')
  })

  it('(b) ui-settings: a same-version rehydrate sanitises and does not run migrate', async () => {
    const options = useUISettingsStore.persist.getOptions()
    const migrate = vi.fn(options.migrate!)
    useUISettingsStore.persist.setOptions({ migrate })
    try {
      useUISettingsStore.setState({ terminalRenderer: 'webgl', keepAliveCount: 9 })
      await useUISettingsStore.persist.rehydrate()
      expect(useUISettingsStore.getState().keepAliveCount).toBe(6) // clampKeepAlive
      expect(migrate).not.toHaveBeenCalled()
    } finally {
      useUISettingsStore.persist.setOptions({ migrate: options.migrate })
    }
  })

  it('(b) hosts: rehydrate runs sanitizeHostConfig over an arriving host', async () => {
    useHostStore.setState({ hosts: { [M]: host(M, { icon: 'NotAnIcon<script>' }) }, hostOrder: [M] })
    await useHostStore.persist.rehydrate()
    expect(useHostStore.getState().hosts[M].icon).toBeUndefined()
    expect(useHostStore.getState().hosts[M].ip).toBe('10.0.0.1')
  })

  // Mechanism only: `purdex-editor-settings` is device-local and no longer part of
  // the profile, so NO projected store is in this situation today. Kept because it
  // is the one store that never registers with syncManager — proof that
  // `persist.rehydrate()` does not depend on that registration.
  it('(b) mechanism: a store NOT registered with syncManager (editor-settings, not projected) rehydrates through merge all the same', async () => {
    useEditorSettingsStore.setState({ fontSize: 9999 })
    await useEditorSettingsStore.persist.rehydrate()
    expect(useEditorSettingsStore.getState().fontSize).toBeLessThan(9999)
    expect(useEditorSettingsStore.getState().fontSize).toBeGreaterThan(EDITOR_DEFAULTS.fontSize - 1)
  })

  it('(c) rehydrate keeps non-persisted state: runtime, visitHistory, actions', async () => {
    useHostStore.getState().setRuntime(M, { status: 'connected' })
    const setRuntime = useHostStore.getState().setRuntime
    await useHostStore.persist.rehydrate()
    expect(useHostStore.getState().runtime[M]).toEqual({ status: 'connected' })
    expect(useHostStore.getState().setRuntime).toBe(setRuntime)

    useTabStore.setState({ tabs: { t1: tab('t1') }, tabOrder: ['t1'], visitHistory: ['t1'] })
    const closeTab = useTabStore.getState().closeTab
    await useTabStore.persist.rehydrate()
    expect(useTabStore.getState().visitHistory).toEqual(['t1'])
    expect(useTabStore.getState().closeTab).toBe(closeTab)

    await useI18nStore.persist.rehydrate()
    expect(typeof useI18nStore.getState().setLocale).toBe('function')
    await useWorkspaceStore.persist.rehydrate()
    expect(typeof useWorkspaceStore.getState().removeWorkspace).toBe('function')
  })

  it('(d) rehydrate completes synchronously: no point at which another writer can interleave', () => {
    useI18nStore.setState({ activeLocaleId: 'zh-TW' })
    void useI18nStore.persist.rehydrate() // NOT awaited
    expect(useI18nStore.getState().t('common.cancel')).toBe('取消')
    expect(useI18nStore.persist.hasHydrated()).toBe(true)
  })

  it('(d) a setState squeezed between the patch and the rehydrate survives it', async () => {
    useUISettingsStore.setState({ dynamicTabName: !UI_DEFAULTS.dynamicTabName })
    useUISettingsStore.setState({ terminalRevealDelay: 777 }) // "the user", same store
    await useUISettingsStore.persist.rehydrate()
    expect(useUISettingsStore.getState().terminalRevealDelay).toBe(777)
    expect(useUISettingsStore.getState().dynamicTabName).toBe(!UI_DEFAULTS.dynamicTabName)
  })
})

// === applySectionToStores ===

const ctx = { masterHostId: M }

const hostsPayloadOf = (hosts: HostConfig[]): HostsPayload => buildHostsSection({ hosts: Object.fromEntries(hosts.map((h) => [h.id, h])), hostOrder: hosts.map((h) => h.id) })

/** Counts every store write made while `run` executes. */
async function countWrites(run: () => Promise<unknown>): Promise<number> {
  let writes = 0
  const stores = [useSessionStore, useAgentStore, useExecutionStore, useExecutionListStore, useNexHostStore, useHostSettingsStore, useHostStore, useTabStore, useWorkspaceStore, useUISettingsStore, useEditorSettingsStore, useThemeStore, useI18nStore, useLayoutStore, useWorkspaceSettingsStore, useLocalProfilesStore]
  const unsubs = stores.map((s) => s.subscribe(() => writes++))
  try {
    await run()
  } finally {
    unsubs.forEach((u) => u())
  }
  return writes
}

function seedTabWorld(): void {
  const split: PaneLayout = { type: 'split', id: 's1', direction: 'h', children: [tmuxLeaf('pa', M), tmuxLeaf('pb', M)], sizes: [70, 30] }
  useTabStore.setState({
    tabs: { a1: tab('a1', split), a2: tab('a2'), b1: tab('b1'), solo: tab('solo') },
    tabOrder: ['solo', 'a1', 'a2', 'b1'],
    activeTabId: 'a2',
    visitHistory: ['b1', 'a1', 'a2'],
  })
  useWorkspaceStore.setState({ workspaces: [ws('wa', ['a1', 'a2'], 'a2'), ws('wb', ['b1'])], activeWorkspaceId: 'wb' })
}

describe('applySectionToStores — guards', () => {
  it('writes no store at all when the well-formedness guard refuses the payload', async () => {
    seedTabWorld()
    const bad: Array<[Parameters<typeof applySectionToStores>[0], unknown]> = [
      ['settings', { 'purdex-unknown': { a: 1 } }],
      ['workspaces', { order: ['wa'], workspaces: {} }],
      ['tabs.wa', { order: ['zz'], tabs: {} }],
    ]
    for (const [key, payload] of bad) {
      let outcome: unknown
      const writes = await countWrites(async () => {
        outcome = await applySectionToStores(key, payload, ctx)
      })
      expect(outcome, key).toMatchObject({ ok: false, reason: 'invalid', code: 'malformed' })
      expect(writes, key).toBe(0)
    }
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('an unknown section key is invalid', async () => {
    expect(await applySectionToStores('bogus' as never, {}, ctx)).toMatchObject({ ok: false, reason: 'invalid', code: 'unknown-section' })
    // a `tabs.` key whose workspace id does not parse is no section either (applyTabsSection's own guard is unreachable)
    expect(await applySectionToStores('tabs.' as never, { order: [], tabs: {} }, ctx)).toMatchObject({ ok: false, reason: 'invalid', code: 'unknown-section' })
  })

  it('a null payload is invalid for settings and workspaces, and writes nothing', async () => {
    for (const key of ['settings', 'workspaces'] as const) {
      let outcome: unknown
      const writes = await countWrites(async () => {
        outcome = await applySectionToStores(key, null, ctx)
      })
      expect(outcome, key).toMatchObject({ ok: false, reason: 'invalid', code: 'deleted' })
      expect(writes, key).toBe(0)
    }
  })

  it('every invalid outcome carries a code from the closed list, and INVALID_REASONS is that list', async () => {
    // host ownership H3a-3: the five codes of the `hosts` apply went with it
    expect([...INVALID_REASONS].sort()).toEqual(['deleted', 'host-identity-conflict', 'malformed', 'rejected-settings', 'unknown-section'])
  })
})

// Host ownership H3a-3 (spec §5.1): the host list is per device. A `hosts` payload — the row an older client still
// writes to the SOT — is never applied: no host added, renamed or removed, no cascade, no lock, no store written.
describe('applySectionToStores — hosts is not synced (host ownership H3)', () => {
  it('a valid payload that removes, renames and adds a host is refused as unknown-section and changes NOTHING', async () => {
    seedTabWorld()
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, b1: tab('b1', tmuxLeaf('p-b1', H2)) } })
    const t = useTabStore.getState()
    const w = useWorkspaceStore.getState()
    const parked: ParkedWorld = { tabs: t.tabs, workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId, activeTabId: t.activeTabId }
    const slaveWorld: ParkedWorld = { tabs: { s1: tab('s1', tmuxLeaf('p-s1', H2)) }, workspaces: [ws('sw', ['s1'])], activeWorkspaceId: 'sw', activeTabId: 's1' }
    useLocalProfilesStore.setState({ slaves: { sl: { id: 'sl', name: 'Slave', createdAt: 1, shownHostIds: [], world: slaveWorld } }, slaveOrder: ['sl'], parkedMaster: parked })
    useHostSettingsStore.setState({ hosts: { [H2]: { files: { root: '/srv' } } } as never })
    useHostLookStore.setState({ looks: { [H2]: { color: '#123456' } } as never })
    // the master renamed, H2 removed, a new host added: a payload the pre-H3 apply would have landed in full
    const payload = JSON.parse(JSON.stringify(hostsPayloadOf([host(M, { name: 'renamed' }), host('newone', { ip: '10.0.0.3', order: 1 })]))) as HostsPayload
    const before = {
      hosts: useHostStore.getState(), tabs: useTabStore.getState(), workspaces: useWorkspaceStore.getState(), profiles: useLocalProfilesStore.getState(),
      hostSettings: useHostSettingsStore.getState(), looks: useHostLookStore.getState(), shown: useShownHostsStore.getState(),
    }
    vi.mocked(deleteHostCascade).mockClear()
    const real = useRebuildStore.getState().acquireOperationLock
    const lockAsks = vi.fn(real)
    useRebuildStore.setState({ acquireOperationLock: lockAsks })
    let outcome: unknown
    let writes = -1
    try {
      writes = await countWrites(async () => {
        outcome = await applySectionToStores('hosts', payload, ctx)
      })
    } finally {
      useRebuildStore.setState({ acquireOperationLock: real })
    }

    expect(outcome).toMatchObject({ ok: false, reason: 'invalid', code: 'unknown-section' })
    expect(useHostStore.getState()).toBe(before.hosts)
    expect(useTabStore.getState()).toBe(before.tabs)
    expect(useWorkspaceStore.getState()).toBe(before.workspaces)
    expect(useLocalProfilesStore.getState()).toBe(before.profiles) // the parked master and every slave world
    expect(useHostSettingsStore.getState()).toBe(before.hostSettings)
    expect(useHostLookStore.getState()).toBe(before.looks)
    expect(useShownHostsStore.getState()).toBe(before.shown)
    expect(writes).toBe(0)
    expect(deleteHostCascade).not.toHaveBeenCalled()
    expect(lockAsks).not.toHaveBeenCalled()
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it.each([
    ['null (deleted)', null],
    ['malformed', { hosts: { [M]: { ...host(M), extra: 1 } }, hostOrder: [M] }],
    ['empty', { hosts: {}, hostOrder: [] }],
  ])('%s: the same answer — the payload is not looked at', async (_name, payload) => {
    const before = useHostStore.getState()
    expect(await applySectionToStores('hosts', payload, ctx)).toMatchObject({ ok: false, reason: 'invalid', code: 'unknown-section' })
    expect(useHostStore.getState()).toBe(before)
  })
})

describe('applySectionToStores — settings', () => {
  const settingsNow = (): SettingsPayload => JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds()))) as SettingsPayload

  it('patches only the stores that differ, through their hooks, and returns the rebuilt hash', async () => {
    const payload = settingsNow()
    payload['purdex-i18n'] = { ...payload['purdex-i18n'], activeLocaleId: 'zh-TW' }
    payload['purdex-themes'] = { ...payload['purdex-themes'], activeThemeId: 'nord' }
    const touched: string[] = []
    const unsubs = [useEditorSettingsStore.subscribe(() => touched.push('editor')), useLayoutStore.subscribe(() => touched.push('layout'))]

    const outcome = await applySectionToStores('settings', payload, ctx)
    unsubs.forEach((u) => u())

    expect(useI18nStore.getState().t('common.cancel')).toBe('取消')
    expect(document.documentElement.dataset.theme).toBe('nord')
    expect(touched).toEqual([])
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(buildSettingsSection(readSettingsSources(), masterWorkspaceIds())) })
  })

  it('workspace-scoped settings: the payload decides the master workspaces only — a foreign local entry survives, a foreign incoming one is not written', async () => {
    useWorkspaceStore.setState({ workspaces: [ws('m1', []), ws('m2', []), ws('bad id!', [])], activeWorkspaceId: 'm1' })
    const foreign = { files: { root: '__NOT_THE_MASTERS__' } }
    useWorkspaceSettingsStore.setState({ workspaces: { m1: { files: { root: '/old' } }, m2: { files: { root: '/dropped' } }, slave: foreign, 'bad id!': foreign } })
    const payload = settingsNow()
    expect(payload['purdex-workspace-settings']).toEqual({ workspaces: { m1: { files: { root: '/old' } }, m2: { files: { root: '/dropped' } } } })
    payload['purdex-workspace-settings'] = { workspaces: { m1: { files: { root: '/new' } }, orphan: { files: { root: '/orphan' } } } }

    const outcome = await applySectionToStores('settings', payload, ctx)

    expect(useWorkspaceSettingsStore.getState().workspaces).toEqual({ m1: { files: { root: '/new' } }, slave: foreign, 'bad id!': foreign })
    expect(persistedOf(STORAGE_KEYS.WORKSPACE_SETTINGS).workspaces).toEqual({ m1: { files: { root: '/new' } }, slave: foreign, 'bad id!': foreign })
    // the rebuilt hash is the FILTERED payload's: the orphan makes this section honestly dirty
    const filtered = { ...payload, 'purdex-workspace-settings': { workspaces: { m1: { files: { root: '/new' } } } } }
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(filtered) })
    expect(outcome).not.toMatchObject({ ok: true, hash: await hashSection(payload) })
  })

  describe('the master\'s workspace set moves WHILE the apply is awaiting (a `workspaces` apply, a user edit)', () => {
    /** Master workspaces [m1]; a scoped entry for `m3`, which is not the master's YET. The payload is the SOT's, where it is. */
    function seed(): { payload: SettingsPayload; before: unknown } {
      useWorkspaceStore.setState({ workspaces: [ws('m1', []), ws('m3', [])], activeWorkspaceId: 'm1' })
      useWorkspaceSettingsStore.setState({ workspaces: { m1: { files: { root: '/sot-m1' } }, m3: { files: { root: '/sot-m3' } } } })
      const payload = settingsNow()
      payload['purdex-ui-settings'] = { ...payload['purdex-ui-settings'], dynamicTabName: !UI_DEFAULTS.dynamicTabName }
      // …and locally it is still [m1], with an OLD value under m3.
      useWorkspaceStore.setState({ workspaces: [ws('m1', [])], activeWorkspaceId: 'm1' })
      useWorkspaceSettingsStore.setState({ workspaces: { m1: { files: { root: '/old-m1' } }, m3: { files: { root: '/old-m3' } } } })
      return { payload, before: JSON.parse(JSON.stringify(readSettingsSources())) }
    }

    /** The `workspaces` apply lands during the first rehydrate this apply awaits. */
    function workspacesArriveMidApply(): void {
      for (const store of [useUISettingsStore, useWorkspaceSettingsStore]) {
        const real = store.persist.rehydrate.bind(store.persist)
        let done = false
        vi.spyOn(store.persist, 'rehydrate').mockImplementation(async () => {
          if (!done && useWorkspaceStore.getState().workspaces.length === 1) {
            done = true
            useWorkspaceStore.setState({ workspaces: [ws('m1', []), ws('m3', [])] })
          }
          await real()
        })
      }
    }

    it('the apply gives up — `busy` — and everything it wrote is put back; it never answers with a hash over the NEW set and the OLD values', async () => {
      const { payload, before } = seed()
      workspacesArriveMidApply()

      const outcome = await applySectionToStores('settings', payload, ctx)
      vi.restoreAllMocks()

      expect(outcome).toEqual({ ok: false, reason: 'busy' })
      expect(JSON.parse(JSON.stringify(readSettingsSources()))).toEqual(before)
      expect(persistedOf(STORAGE_KEYS.UI_SETTINGS).dynamicTabName).toBe(UI_DEFAULTS.dynamicTabName)
      expect(persistedOf(STORAGE_KEYS.WORKSPACE_SETTINGS).workspaces).toEqual({ m1: { files: { root: '/old-m1' } }, m3: { files: { root: '/old-m3' } } })
    })

    it('…and the retry, scoped by the set as it now is, lands the SOT\'s value for the new workspace: clean', async () => {
      const { payload } = seed()
      workspacesArriveMidApply()
      await applySectionToStores('settings', payload, ctx)
      vi.restoreAllMocks()

      const outcome = await applySectionToStores('settings', payload, ctx)
      expect(useWorkspaceSettingsStore.getState().workspaces).toEqual({ m1: { files: { root: '/sot-m1' } }, m3: { files: { root: '/sot-m3' } } })
      expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
    })

    it('the world going UNSETTLED mid-apply is the same: nobody can say what the set is', async () => {
      const { payload, before } = seed()
      const real = useUISettingsStore.persist.rehydrate.bind(useUISettingsStore.persist)
      vi.spyOn(useUISettingsStore.persist, 'rehydrate').mockImplementation(async () => {
        useTabStore.setState({ worldEpoch: useTabStore.getState().worldEpoch + 1 })
        await real()
      })
      const outcome = await applySectionToStores('settings', payload, ctx)
      vi.restoreAllMocks()
      useTabStore.setState({ worldEpoch: useWorkspaceStore.getState().worldEpoch })
      expect(outcome).toEqual({ ok: false, reason: 'busy' })
      expect(JSON.parse(JSON.stringify(readSettingsSources()))).toEqual(before)
    })

    it('a set that is the same ids again (another array, another order) is no reason to give up', async () => {
      const { payload } = seed()
      const real = useUISettingsStore.persist.rehydrate.bind(useUISettingsStore.persist)
      vi.spyOn(useUISettingsStore.persist, 'rehydrate').mockImplementation(async () => {
        useWorkspaceStore.setState({ workspaces: [ws('m1', [])] })
        await real()
      })
      expect(await applySectionToStores('settings', payload, ctx)).toMatchObject({ ok: true })
      vi.restoreAllMocks()
    })
  })

  it('an in-place heal reaches subscribers and localStorage', async () => {
    useLayoutStore.setState({ activityBarWidth: 'narrow' })
    const payload = settingsNow()
    payload['purdex-layout'] = { tabPosition: 'left' }
    const seen: string[] = []
    const unsub = useLayoutStore.subscribe((s) => seen.push(s.activityBarWidth))
    await applySectionToStores('settings', payload, ctx)
    unsub()
    expect(seen.at(-1)).toBe('wide')
    expect(persistedOf(STORAGE_KEYS.LAYOUT).activityBarWidth).toBe('wide')
  })

  // P3e: settings ordinal 3 → 4. Every door into this function (pull, attach
  // pull, keep-sot, restoreLocal of a persisted stash) may carry an ordinal-3
  // payload whose newtab field is still `profiles`.
  it('an ordinal-3 payload (newtab `profiles`) is upcast: the layout lands under presets, never wiped; device-local fields kept', async () => {
    useNewTabLayoutStore.setState({ ...useNewTabLayoutStore.getInitialState(), knownIds: ['a', 'b'], activeEditingPreset: '2col' })
    const layout = {
      '3col': { enabled: true, columns: [['b'], ['a'], []] },
      '2col': { enabled: true, columns: [['a'], ['b']] },
      '1col': { enabled: true, columns: [['b', 'a']] },
    }
    const current = settingsNow()
    const legacy = { ...current, 'purdex-newtab-layout': { profiles: layout } } as unknown as SettingsPayload
    const outcome = await applySectionToStores('settings', legacy, ctx)

    expect(outcome).toMatchObject({ ok: true })
    expect(useNewTabLayoutStore.getState().presets).toEqual(layout)
    expect(useNewTabLayoutStore.getState().knownIds).toEqual(['a', 'b'])
    expect(useNewTabLayoutStore.getState().activeEditingPreset).toBe('2col')
    expect(persistedOf(STORAGE_KEYS.NEW_TAB_LAYOUT).presets).toEqual(layout)
    // the rebuilt hash is the ordinal-4 shape's: it differs from the row's, so the executor pushes the upgrade
    const upcast = { ...current, 'purdex-newtab-layout': { presets: layout } }
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(upcast) })
    expect(outcome).not.toMatchObject({ ok: true, hash: await hashSection(legacy) })
  })

  it('editor preferences are device-local: not a settings source, and a payload carrying them is invalid', async () => {
    expect(Object.keys(readSettingsSources())).toHaveLength(10)
    expect(readSettingsSources()).not.toHaveProperty('purdex-editor-settings')
    const payload = { ...settingsNow(), 'purdex-editor-settings': { fontSize: 20 } }
    let outcome: unknown
    const writes = await countWrites(async () => {
      outcome = await applySectionToStores('settings', payload, ctx)
    })
    expect(outcome).toMatchObject({ ok: false, reason: 'invalid', code: 'malformed' })
    expect(writes).toBe(0)
    expect(useEditorSettingsStore.getState().fontSize).toBe(EDITOR_DEFAULTS.fontSize)
  })

  it('rejected fields → invalid with the list, nothing written', async () => {
    const payload = settingsNow()
    payload['purdex-layout'] = { tabPosition: {} }
    payload['purdex-i18n'] = { ...payload['purdex-i18n'], activeLocaleId: 'zh-TW' }
    let outcome: unknown
    const writes = await countWrites(async () => {
      outcome = await applySectionToStores('settings', payload, ctx)
    })
    expect(outcome).toEqual({ ok: false, reason: 'invalid', code: 'rejected-settings', detail: expect.stringContaining('purdex-layout.tabPosition') })
    expect(writes).toBe(0)
  })

  it('bumps terminalSettingsVersion when terminalRenderer changes, and only then', async () => {
    const other = UI_DEFAULTS.terminalRenderer === 'webgl' ? 'dom' : 'webgl'
    const payload = settingsNow()
    payload['purdex-ui-settings'] = { ...payload['purdex-ui-settings'], dynamicTabName: !UI_DEFAULTS.dynamicTabName }
    await applySectionToStores('settings', payload, ctx)
    expect(useUISettingsStore.getState().dynamicTabName).toBe(!UI_DEFAULTS.dynamicTabName)
    expect(useUISettingsStore.getState().terminalSettingsVersion).toBe(0)

    payload['purdex-ui-settings'] = { ...payload['purdex-ui-settings'], terminalRenderer: other }
    await applySectionToStores('settings', payload, ctx)
    expect(useUISettingsStore.getState().terminalRenderer).toBe(other)
    expect(useUISettingsStore.getState().terminalSettingsVersion).toBe(1)
  })

  it('a custom theme the payload drops leaves the registry, and an active id naming it falls back', async () => {
    const custom = { id: 'custom-1', name: 'Mine', tokens: getTheme('dark')!.tokens, builtin: false }
    const withCustom = settingsNow()
    withCustom['purdex-themes'] = { activeThemeId: 'custom-1', customThemes: { 'custom-1': custom } }
    await applySectionToStores('settings', withCustom, ctx)
    expect(getTheme('custom-1')).toBeDefined()

    const dropped = settingsNow()
    dropped['purdex-themes'] = { activeThemeId: 'custom-1', customThemes: {} }
    const outcome = await applySectionToStores('settings', dropped, ctx)
    expect(getTheme('custom-1')).toBeUndefined()
    expect(useThemeStore.getState().activeThemeId).toBe('dark')
    expect(outcome).not.toMatchObject({ ok: true, hash: await hashSection(dropped) }) // honestly dirty
  })
})

// host ownership H2c-1 (spec §4.1): the look store's keys are wire ids IN the store — an apply writes them verbatim,
// whatever this device's hosts are, and replaces the record whole.
describe('applySectionToStores — settings: host looks', () => {
  const DAEMON = 'mini-lab:278cbm'
  const WIRE = syncIdOfSync(DAEMON)
  const RED = { console: { main: { color: '#ef4444', alpha: 100 } } }
  const settingsNow = (): SettingsPayload => JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds(), identityOfSync(useHostStore.getState().hosts)))) as SettingsPayload

  beforeEach(() => {
    useHostStore.setState({ hosts: { [M]: host(M, { daemonId: DAEMON }), [H2]: host(H2, { ip: '10.0.0.2', order: 1 }) }, hostOrder: [M, H2] })
  })

  it('a payload naming a host here AND one no host here claims → both land byte-for-byte; the hash is the payload\'s (nothing to push)', async () => {
    const looks = {
      [WIRE]: { name: 'mlab', colors: RED, icon: 'Laptop', iconWeight: 'bold' },
      d1_unknown: { name: 'far away', color: '#00ff00' },
      [H2]: { name: 'no daemon yet' },
    }
    const payload = { ...settingsNow(), 'purdex-host-looks': { looks } } as SettingsPayload

    const outcome = await applySectionToStores('settings', payload, ctx)

    expect(JSON.stringify(useHostLookStore.getState().looks)).toBe(JSON.stringify(looks))
    expect(persistedOf(STORAGE_KEYS.HOST_LOOKS).looks).toEqual(looks)
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
  })

  it('replace semantics: an entry the payload lacks is dropped here', async () => {
    useHostLookStore.setState({ looks: { [WIRE]: { name: 'old' }, d1_gone: { name: 'gone' } } })
    const payload = { ...settingsNow(), 'purdex-host-looks': { looks: { [WIRE]: { name: 'new' } } } } as SettingsPayload
    expect(await applySectionToStores('settings', payload, ctx)).toMatchObject({ ok: true, hash: await hashSection(payload) })
    expect(useHostLookStore.getState().looks).toEqual({ [WIRE]: { name: 'new' } })
  })

  it('an entry the store sanitises lands cleaned, and the section is honestly dirty', async () => {
    const payload = { ...settingsNow(), 'purdex-host-looks': { looks: { d1_x: { name: 'x', icon: 'NotAnIcon' } } } } as SettingsPayload
    const outcome = await applySectionToStores('settings', payload, ctx)
    expect(useHostLookStore.getState().looks).toEqual({ d1_x: { name: 'x' } })
    expect(outcome).not.toMatchObject({ ok: true, hash: await hashSection(payload) })
  })

  it('an ordinal-5 payload (no look store) leaves the look store untouched; the rebuild carries it, so the hash differs (pushed once)', async () => {
    useHostLookStore.setState({ looks: { [WIRE]: { name: 'mine' } } })
    const before = useHostLookStore.getState()
    const legacy = settingsNow()
    delete legacy['purdex-host-looks']
    const outcome = await applySectionToStores('settings', legacy, ctx)
    expect(useHostLookStore.getState()).toBe(before)
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection({ ...legacy, 'purdex-host-looks': { looks: { [WIRE]: { name: 'mine' } } } }) })
    expect(outcome).not.toMatchObject({ ok: true, hash: await hashSection(legacy) })
  })
})

// host ownership H2d-1 (spec §4.1, plan §0.6): the shown-hosts ids are wire ids IN the store — an apply writes them
// verbatim, whatever this device's hosts are; `{ ids }` always travels (the empty list included), so every [] ↔ list
// transition is a same-shape patch (never `rejected-settings`).
describe('applySectionToStores — settings: shown hosts', () => {
  const DAEMON = 'mini-lab:278cbm'
  const WIRE = syncIdOfSync(DAEMON)
  const settingsNow = (): SettingsPayload => JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds(), identityOfSync(useHostStore.getState().hosts)))) as SettingsPayload
  const ids = () => useShownHostsStore.getState().ids

  beforeEach(() => {
    useHostStore.setState({ hosts: { [M]: host(M, { daemonId: DAEMON }), [H2]: host(H2, { ip: '10.0.0.2', order: 1 }) }, hostOrder: [M, H2] })
  })

  it('the store is always in the build, the empty default included', () => {
    expect(settingsNow()['purdex-shown-hosts']).toEqual({ ids: [] })
  })

  it('{ ids: [d1_unknown, d1_a] } lands byte-for-byte and round-trips apply → build; the hash is the payload\'s (nothing to push)', async () => {
    const payload = { ...settingsNow(), 'purdex-shown-hosts': { ids: ['d1_unknown', 'd1_a'] } } as SettingsPayload
    const outcome = await applySectionToStores('settings', payload, ctx)
    expect(ids()).toEqual(['d1_unknown', 'd1_a'])
    expect(persistedOf(STORAGE_KEYS.SHOWN_HOSTS)).toEqual({ ids: ['d1_unknown', 'd1_a'] })
    expect(JSON.stringify(settingsNow()['purdex-shown-hosts'])).toBe(JSON.stringify(payload['purdex-shown-hosts']))
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
  })

  it('ids naming a host here (wire and local form) and one no host here claims land verbatim — no local↔wire mapping', async () => {
    const payload = { ...settingsNow(), 'purdex-shown-hosts': { ids: [WIRE, 'd1_unknown', H2] } } as SettingsPayload
    expect(await applySectionToStores('settings', payload, ctx)).toMatchObject({ ok: true, hash: await hashSection(payload) })
    expect(ids()).toEqual([WIRE, 'd1_unknown', H2])
  })

  it.each([
    ['[] → a list', [] as string[], [WIRE, 'd1_unknown']],
    ['a list → []', [WIRE, 'd1_unknown'], [] as string[]],
  ])('%s applies (no rejected-settings)', async (_label, local, incoming) => {
    useShownHostsStore.setState({ ids: local })
    const payload = { ...settingsNow(), 'purdex-shown-hosts': { ids: incoming } } as SettingsPayload
    const outcome = await applySectionToStores('settings', payload, ctx)
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
    expect(ids()).toEqual(incoming)
  })

  it('applying a shown-hosts change closes nothing: the tab, workspace and local-profiles stores stay the same objects (§0.21)', async () => {
    const tabs = useTabStore.getState()
    const workspaces = useWorkspaceStore.getState()
    const profiles = useLocalProfilesStore.getState()
    useShownHostsStore.setState({ ids: [WIRE, H2] })
    const payload = { ...settingsNow(), 'purdex-shown-hosts': { ids: [H2] } } as SettingsPayload
    expect(await applySectionToStores('settings', payload, ctx)).toMatchObject({ ok: true })
    expect(ids()).toEqual([H2])
    expect(useTabStore.getState()).toBe(tabs)
    expect(useWorkspaceStore.getState()).toBe(workspaces)
    expect(useLocalProfilesStore.getState()).toBe(profiles)
  })

  // The #1421 attacker finding, as a regression: two devices with different host lists share one synced list. A's
  // hide must carry every id it does not know through; B, applying it, keeps its own host shown. Hosts added later are
  // hidden on each device and write nothing.
  it('two clients: A hides a → payload [d1_b, d1_c] (d1_c kept although A lacks c); on B c stays shown, a is hidden; later adds write nothing', async () => {
    const d = (n: string) => `${n}-lab:${n.repeat(6).slice(0, 6)}`
    const hostsOf = (names: string[]) => ({
      [M]: host(M, { daemonId: DAEMON }),
      ...Object.fromEntries(names.map((n, i) => [n, host(n, { ip: `10.0.1.${i + 1}`, order: i + 1, daemonId: d(n) })])),
    })
    const [A_HOSTS, B_HOSTS] = [hostsOf(['a', 'b']), hostsOf(['a', 'b', 'c'])]
    const SYNCED = ['a', 'b', 'c'].map((n) => syncIdOfSync(d(n)))

    // device A
    useHostStore.setState({ hosts: A_HOSTS, hostOrder: [M, 'a', 'b'] })
    useShownHostsStore.setState({ ids: SYNCED })
    setHostShown('a', false)
    const fromA = settingsNow()
    expect(fromA['purdex-shown-hosts']).toEqual({ ids: [SYNCED[1], SYNCED[2]] })

    // device B
    useHostStore.setState({ hosts: B_HOSTS, hostOrder: [M, 'a', 'b', 'c'] })
    useShownHostsStore.setState({ ids: SYNCED })
    expect(await applySectionToStores('settings', fromA, ctx)).toMatchObject({ ok: true, hash: await hashSection(fromA) })
    expect(isRefShownNow('c')).toBe(true)
    expect(isRefShownNow('b')).toBe(true)
    expect(isRefShownNow('a')).toBe(false)

    // a host added later on B is hidden there; nothing is written, the payload rebuilds byte-identical
    const onB = JSON.stringify(settingsNow())
    const e = useHostStore.getState().addHost({ name: 'e', ip: '10.0.2.1', port: 7860 })
    expect(isRefShownNow(e)).toBe(false)
    expect(JSON.stringify(settingsNow())).toBe(onB)

    // back on A (its store as it left it): a host d added is hidden; the payload rebuilds byte-identical
    useHostStore.setState({ hosts: A_HOSTS, hostOrder: [M, 'a', 'b'] })
    useShownHostsStore.setState({ ids: fromA['purdex-shown-hosts']!.ids as string[] })
    const onA = JSON.stringify(settingsNow())
    const dId = useHostStore.getState().addHost({ name: 'd', ip: '10.0.3.1', port: 7860 })
    expect(isRefShownNow(dId)).toBe(false)
    expect(JSON.stringify(settingsNow())).toBe(onA)
  })

  it('an ordinal-6 payload (no shown-hosts store) leaves the store untouched; the rebuild carries it, so the hash differs (pushed once)', async () => {
    useShownHostsStore.getState().show(WIRE)
    const before = useShownHostsStore.getState()
    const legacy = settingsNow()
    delete legacy['purdex-shown-hosts']
    const outcome = await applySectionToStores('settings', legacy, ctx)
    expect(useShownHostsStore.getState()).toBe(before)
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection({ ...legacy, 'purdex-shown-hosts': { ids: [WIRE] } }) })
  })
})

describe('applySectionToStores — settings: a failed apply rolls back registries, DOM and translator too', () => {
  const customTheme = () => ({ id: 'custom-1', name: 'Mine', tokens: getTheme('dark')!.tokens, builtin: false })
  const customLocale = { id: 'custom-loc', name: 'Dansk', translations: { 'common.cancel': 'Annuller' }, builtin: false }
  const current = (): SettingsPayload => JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds()))) as SettingsPayload

  /** This device runs a custom theme and a custom locale; the payload drops both and moves the tab bar. */
  async function seedCustom(): Promise<SettingsPayload> {
    const seeded = current()
    seeded['purdex-themes'] = { activeThemeId: 'custom-1', customThemes: { 'custom-1': customTheme() } }
    seeded['purdex-i18n'] = { activeLocaleId: 'custom-loc', customLocales: { 'custom-loc': customLocale } }
    expect(await applySectionToStores('settings', seeded, ctx)).toMatchObject({ ok: true })
    expect(useI18nStore.getState().t('common.cancel')).toBe('Annuller')
    expect(document.documentElement.dataset.theme).toBe('custom-1')
    const incoming = current()
    incoming['purdex-themes'] = { activeThemeId: 'nord', customThemes: {} }
    incoming['purdex-i18n'] = { activeLocaleId: 'zh-TW', customLocales: {} }
    incoming['purdex-layout'] = { tabPosition: 'left' }
    return incoming
  }

  const observed = () => ({
    settings: current(),
    theme: getTheme('custom-1'),
    locale: getLocale('custom-loc'),
    domTheme: document.documentElement.dataset.theme,
    domLang: document.documentElement.lang,
    cancel: useI18nStore.getState().t('common.cancel'),
  })

  it('a later store throws → the nine stores, both registries, <html> theme/lang and `t` are all as before', async () => {
    const incoming = await seedCustom()
    const before = observed()
    expect(before).toMatchObject({ domTheme: 'custom-1', domLang: 'custom-loc', cancel: 'Annuller' })
    vi.spyOn(useLayoutStore, 'setState').mockImplementationOnce(() => {
      // by now themes and i18n HAVE been applied — the rollback has real work to do
      expect(getTheme('custom-1')).toBeUndefined()
      expect(useI18nStore.getState().t('common.cancel')).toBe('取消')
      throw new Error('layout write failed')
    })
    const failure = await applySectionToStores('settings', incoming, ctx).then(() => null, (e: Error) => e)
    expect(failure?.message).toBe('layout write failed') // a complete rollback adds nothing to the error
    expect(observed()).toEqual(before)
  })

  it('the rollback\'s own persist fails → memory and registries are still restored, and the error says the rollback is incomplete', async () => {
    const incoming = await seedCustom()
    const before = observed()
    const realSetItem = Storage.prototype.setItem
    let armed = false
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === STORAGE_KEYS.LAYOUT) armed = true
      if (armed) throw new DOMException('quota', 'QuotaExceededError')
      realSetItem.call(this, key, value)
    })
    const failure = await applySectionToStores('settings', incoming, ctx).then(() => null, (e: Error) => e)
    expect(failure?.message).toMatch(/quota/)
    expect(failure?.message).toMatch(/rollback incomplete/)
    expect(failure?.message).toMatch(/purdex-themes/)
    const after = observed()
    expect(after.settings).toEqual(before.settings) // memory
    expect(after.theme).toEqual(before.theme) // re-registered explicitly: no rehydrate ran for this store
    expect(after.locale).toEqual(before.locale)
  })
})

describe('applySectionToStores — workspaces', () => {
  it('renames / reorders / adds, keeping each workspace\'s tabs, activeTabId and the active workspace', async () => {
    seedTabWorld()
    const payload: WorkspacesPayload = { order: ['wb', 'wa', 'wc'], workspaces: { wa: { name: 'Alpha' }, wb: { name: 'WB' }, wc: { name: 'New' } } }
    const outcome = await applySectionToStores('workspaces', payload, ctx)
    const s = useWorkspaceStore.getState()
    expect(s.workspaces.map((w) => w.id)).toEqual(['wb', 'wa', 'wc'])
    expect(s.workspaces[1]).toMatchObject({ name: 'Alpha', tabs: ['a1', 'a2'], activeTabId: 'a2' })
    expect(s.workspaces[2]).toMatchObject({ tabs: [], activeTabId: null })
    expect(s.activeWorkspaceId).toBe('wb')
    expect(useTabStore.getState().tabOrder).toEqual(['b1', 'a1', 'a2', 'solo'])
    expect(useTabStore.getState().activeTabId).toBe('a2')
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(buildWorkspacesSection(s.workspaces)) })
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('a removed workspace takes its tabs, its visit history, its scoped settings — and the global active tab moves on', async () => {
    seedTabWorld()
    useWorkspaceSettingsStore.setState({ workspaces: { wa: { files: { x: 1 } }, wb: { files: { x: 2 } } } } as never)
    const payload: WorkspacesPayload = { order: ['wb'], workspaces: { wb: { name: 'WB' } } }
    await applySectionToStores('workspaces', payload, ctx)
    const t = useTabStore.getState()
    expect(Object.keys(t.tabs).sort()).toEqual(['b1', 'solo'])
    expect(t.tabOrder).toEqual(['b1', 'solo'])
    expect(t.visitHistory).toEqual(['b1'])
    expect(t.activeTabId).toBe('b1') // 'a2' is gone → the active workspace's active tab
    expect(Object.keys(useWorkspaceSettingsStore.getState().workspaces)).toEqual(['wb'])
  })

  it('a workspace whose id cannot sync is device-local: an apply keeps it, its tabs, its history and its scoped settings', async () => {
    seedTabWorld()
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, x1: tab('x1') }, tabOrder: [...useTabStore.getState().tabOrder, 'x1'], activeTabId: 'x1', visitHistory: ['x1', 'b1'] })
    useWorkspaceStore.setState({ workspaces: [ws('bad id!', ['x1']), ...useWorkspaceStore.getState().workspaces], activeWorkspaceId: 'bad id!' })
    useWorkspaceSettingsStore.setState({ workspaces: { 'bad id!': { files: { x: 1 } } } } as never)
    const payload: WorkspacesPayload = { order: ['wb'], workspaces: { wb: { name: 'WB' } } } // what another client, which never saw 'bad id!', holds

    const outcome = await applySectionToStores('workspaces', payload, ctx)

    const w = useWorkspaceStore.getState()
    expect(w.workspaces.map((x) => x.id)).toEqual(['wb', 'bad id!'])
    expect(w.workspaces[1]).toMatchObject({ tabs: ['x1'], activeTabId: 'x1' })
    expect(w.activeWorkspaceId).toBe('bad id!')
    const t = useTabStore.getState()
    expect(Object.keys(t.tabs).sort()).toEqual(['b1', 'solo', 'x1'])
    expect(t.activeTabId).toBe('x1')
    expect(t.visitHistory).toEqual(['x1', 'b1'])
    expect(Object.keys(useWorkspaceSettingsStore.getState().workspaces)).toEqual(['bad id!'])
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) }) // converged: the builder leaves it out again
  })

  it('the global active tab becomes null when neither it nor the active workspace\'s tab survives', async () => {
    seedTabWorld()
    useWorkspaceStore.setState({ activeWorkspaceId: 'wa' })
    await applySectionToStores('workspaces', { order: [], workspaces: {} }, ctx)
    expect(useTabStore.getState().activeTabId).toBeNull()
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(['solo'])
  })

  it('busy: the operation lock is held → nothing is written, nothing is released', async () => {
    seedTabWorld()
    const grant = useRebuildStore.getState().acquireOperationLock('someone-else')
    for (const [key, payload] of [['workspaces', { order: [], workspaces: {} }], ['tabs.wa', { order: [], tabs: {} }]] as const) {
      let outcome: unknown
      const writes = await countWrites(async () => {
        outcome = await applySectionToStores(key, payload, ctx)
      })
      expect(outcome, key).toEqual({ ok: false, reason: 'busy' })
      expect(writes, key).toBe(0)
    }
    expect(useRebuildStore.getState().lockGrant).toBe(grant)
  })

  it('rolls BOTH stores back when the second write throws', async () => {
    seedTabWorld()
    const tabBefore = { ...useTabStore.getState() }
    const wsBefore = { ...useWorkspaceStore.getState() }
    const real = useWorkspaceStore.setState
    vi.spyOn(useWorkspaceStore, 'setState').mockImplementationOnce((...args) => {
      real(...(args as Parameters<typeof real>)) // the write lands, THEN the failure — both stores are dirty
      throw new Error('quota')
    })
    await expect(applySectionToStores('workspaces', { order: ['wb'], workspaces: { wb: { name: 'X' } } }, ctx)).rejects.toThrow('quota')
    const t = useTabStore.getState()
    expect({ tabs: t.tabs, tabOrder: t.tabOrder, activeTabId: t.activeTabId, visitHistory: t.visitHistory }).toEqual({
      tabs: tabBefore.tabs, tabOrder: tabBefore.tabOrder, activeTabId: tabBefore.activeTabId, visitHistory: tabBefore.visitHistory,
    })
    expect(useWorkspaceStore.getState().workspaces).toEqual(wsBefore.workspaces)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('wb')
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })
})

describe('applySectionToStores — workspaces: the scoped-settings cleanup is part of the same rollback', () => {
  const scoped = { wa: { files: { x: 1 } }, wb: { files: { x: 2 } }, keep: { files: { x: 3 } } }
  const removeBoth: WorkspacesPayload = { order: ['keep'], workspaces: { keep: { name: 'Keep' } } }

  function seed(): void {
    seedTabWorld()
    useWorkspaceStore.setState({ workspaces: [...useWorkspaceStore.getState().workspaces, ws('keep', [])] })
    useWorkspaceSettingsStore.setState({ workspaces: scoped } as never)
  }

  function memory(): unknown {
    const t = useTabStore.getState()
    const w = useWorkspaceStore.getState()
    return {
      tab: { tabs: t.tabs, tabOrder: t.tabOrder, activeTabId: t.activeTabId, visitHistory: t.visitHistory },
      ws: { workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId },
      scoped: useWorkspaceSettingsStore.getState().workspaces,
    }
  }

  it('first clear lands, second throws → all THREE stores are back where they were', async () => {
    seed()
    const before = memory()
    const real = useWorkspaceSettingsStore.getState().clearWorkspace
    let calls = 0
    useWorkspaceSettingsStore.setState({
      clearWorkspace: (id: string) => {
        if (++calls === 2) throw new Error('second clear failed')
        real(id)
      },
    })
    try {
      await expect(applySectionToStores('workspaces', removeBoth, ctx)).rejects.toThrow('second clear failed')
    } finally {
      useWorkspaceSettingsStore.setState({ clearWorkspace: real })
    }
    expect(calls).toBe(2)
    expect(memory()).toEqual(before)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('the clear\'s own persist throws (state already changed in memory) → all three stores are back', async () => {
    seed()
    const before = memory()
    const realSetItem = Storage.prototype.setItem
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
      if (key === STORAGE_KEYS.WORKSPACE_SETTINGS) throw new DOMException('quota', 'QuotaExceededError')
      realSetItem.call(this, key, value)
    })
    await expect(applySectionToStores('workspaces', removeBoth, ctx)).rejects.toThrow('quota')
    expect(memory()).toEqual(before)
  })
})

describe('applySectionToStores — tabs.<id>', () => {
  function incomingFor(wsTabs: Tab[]): TabsPayload {
    return buildTabsSection(ws('wa', wsTabs.map((t) => t.id)), Object.fromEntries(wsTabs.map((t) => [t.id, t])))
  }

  it('replaces one workspace\'s tabs; split sizes, focus and the other workspaces are left alone', async () => {
    seedTabWorld()
    const a1 = useTabStore.getState().tabs.a1
    const b1 = useTabStore.getState().tabs.b1
    const payload = incomingFor([tab('a3'), { ...a1, pinned: true }])
    const outcome = await applySectionToStores('tabs.wa', payload, ctx)
    const t = useTabStore.getState()
    const w = useWorkspaceStore.getState()
    expect(w.workspaces[0]).toMatchObject({ id: 'wa', tabs: ['a3', 'a1'], activeTabId: 'a3' }) // 'a2' was active and is gone
    expect(w.activeWorkspaceId).toBe('wb')
    expect(t.tabs.a1.pinned).toBe(true)
    expect((t.tabs.a1.layout as { sizes: number[] }).sizes).toEqual([70, 30])
    expect(t.tabs.b1).toBe(b1)
    expect(t.tabs.a2).toBeUndefined()
    expect(t.tabOrder).toEqual(['a3', 'a1', 'b1', 'solo'])
    expect(t.visitHistory).toEqual(['b1', 'a1'])
    expect(t.activeTabId).toBe('b1') // 'a2' gone → active workspace (wb)'s active tab
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(buildTabsSection(w.workspaces[0], t.tabs)) })
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('keeps the global active tab while it survives', async () => {
    seedTabWorld()
    await applySectionToStores('tabs.wa', incomingFor([tab('a2'), tab('a9')]), ctx)
    expect(useTabStore.getState().activeTabId).toBe('a2')
  })

  // tabs-local-only §3.6: the commit re-points the global active tab through `repointActiveTab`.
  it('a global active tab that is a kept device-local tab stays active; a removed synced one moves as before', async () => {
    seedTabWorld()
    const settings = tab('sL', { type: 'leaf', pane: { id: 'p-sL', content: { kind: 'settings', scope: 'global' } } })
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, sL: settings }, activeTabId: 'sL' })
    useWorkspaceStore.setState({ workspaces: [ws('wa', ['a1', 'sL', 'a2'], 'a2'), ws('wb', ['b1'])], activeWorkspaceId: 'wa' })
    await applySectionToStores('tabs.wa', incomingFor([tab('a1')]), ctx)
    expect(useTabStore.getState().activeTabId).toBe('sL')
    expect(useTabStore.getState().tabs.sL).toBe(settings)
    expect(useWorkspaceStore.getState().workspaces[0]).toMatchObject({ tabs: ['a1', 'sL'], activeTabId: 'a1' })

    // a2 (synced) was on screen and is removed → the active workspace's pointer, as today
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, a2: tab('a2') }, activeTabId: 'a2' })
    useWorkspaceStore.setState({ workspaces: [ws('wa', ['a1', 'sL', 'a2'], 'sL'), ws('wb', ['b1'])], activeWorkspaceId: 'wa' })
    await applySectionToStores('tabs.wa', incomingFor([tab('a1')]), ctx)
    expect(useTabStore.getState().activeTabId).toBe('sL')
    expect(Object.hasOwn(useTabStore.getState().tabs, 'a2')).toBe(false)
  })

  it('unrendered: a workspace unknown locally → ok with a null hash, nothing written', async () => {
    seedTabWorld()
    let outcome: unknown
    const writes = await countWrites(async () => {
      outcome = await applySectionToStores('tabs.nowhere', incomingFor([tab('z1')]), ctx)
    })
    expect(outcome).toEqual({ ok: true, hash: null })
    expect(writes).toBe(0)
  })

  // Host ownership §3.1.1 / §3.2: a pane naming a host this device lacks is kept verbatim — never marked — so the
  // stores hold exactly what arrived and nothing is pushed back.
  it('keeps an arriving pane of a host unknown here byte-for-byte, unmarked, and reports the incoming payload\'s hash', async () => {
    seedTabWorld()
    const unknown = syncIdOfSync('air-lab:0unkn0')
    const split: PaneLayout = { type: 'split', id: 's9', direction: 'v', children: [tmuxLeaf('gone-pane', unknown), tmuxLeaf('ok-pane', M)], sizes: [50, 50] }
    const payload = incomingFor([tab('a5', split)])
    const outcome = await applySectionToStores('tabs.wa', payload, ctx)
    const layout = useTabStore.getState().tabs.a5.layout as Extract<PaneLayout, { type: 'split' }>
    const wired = (payload.tabs.a5.layout as Extract<PaneLayout, { type: 'split' }>).children[0] as Extract<PaneLayout, { type: 'leaf' }>
    expect((layout.children[0] as Extract<PaneLayout, { type: 'leaf' }>).pane).toEqual(wired.pane)
    expect(JSON.stringify(useTabStore.getState().tabs.a5)).not.toContain('terminated')
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
  })

  describe('an ordinal-2 payload listing device-local tabs (tabs-local-only §3.5)', () => {
    const settingsLeaf = (id: string): PaneLayout => ({ type: 'leaf', pane: { id, content: { kind: 'settings', scope: 'global' } } })
    /** What an ordinal-2 client built: its own Settings tab `sX` in the order and the record. */
    function legacyWith(sX: string): TabsPayload {
      // Built by hand: this build's `buildTabsSection` leaves the Settings tab out.
      const p = JSON.parse(JSON.stringify(incomingFor([useTabStore.getState().tabs.a1]))) as TabsPayload
      const settings = tab(sX, settingsLeaf(`p-${sX}`))
      return { order: [...p.order, sX], tabs: { ...p.tabs, [sX]: { ...settings, layout: stripSizes(settings.layout) } } }
    }

    it('one this device lacks is not created; the outcome says the rewrite is designed and hands back the canonical payload', async () => {
      seedTabWorld()
      const legacy = legacyWith('sX')
      expect(legacy.order).toEqual(['a1', 'sX'])
      const outcome = await applySectionToStores('tabs.wa', legacy, ctx)
      expect(Object.hasOwn(useTabStore.getState().tabs, 'sX')).toBe(false)
      expect(useWorkspaceStore.getState().workspaces[0].tabs).toEqual(['a1'])
      const canonical = { order: ['a1'], tabs: { a1: legacy.tabs.a1 } }
      expect(outcome).toMatchObject({ ok: true, hash: await hashSection(canonical), rewrite: 'device-local-tabs' })
      expect((outcome as { payload: TabsPayload }).payload.order).toEqual(['a1'])
    })

    it('one this device has is device-local here: kept, listed, untouched', async () => {
      seedTabWorld()
      const mine = tab('sX', settingsLeaf('p-mine'))
      useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, sX: mine } })
      useWorkspaceStore.setState({ workspaces: [ws('wa', ['a1', 'sX', 'a2'], 'a2'), ws('wb', ['b1'])] })
      const outcome = await applySectionToStores('tabs.wa', legacyWith('sX'), ctx)
      expect(useTabStore.getState().tabs.sX).toBe(mine)
      expect(useWorkspaceStore.getState().workspaces[0].tabs).toEqual(['a1', 'sX'])
      expect(outcome).toMatchObject({ ok: true, rewrite: 'device-local-tabs' })
    })

    it('a canonical payload carries no rewrite', async () => {
      seedTabWorld()
      const outcome = await applySectionToStores('tabs.wa', incomingFor([tab('a1')]), ctx)
      expect(outcome).not.toHaveProperty('rewrite')
    })
  })

  it('null payload: a workspace still known has its tabs emptied; an unknown one is a no-op', async () => {
    seedTabWorld()
    const outcome = await applySectionToStores('tabs.wa', null, ctx)
    expect(useWorkspaceStore.getState().workspaces[0]).toMatchObject({ id: 'wa', tabs: [], activeTabId: null })
    expect(Object.keys(useTabStore.getState().tabs).sort()).toEqual(['b1', 'solo'])
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection({ order: [], tabs: {} }) })

    let second: unknown
    const writes = await countWrites(async () => {
      second = await applySectionToStores('tabs.nowhere', null, ctx)
    })
    expect(second).toEqual({ ok: true, hash: null })
    expect(writes).toBe(0)
  })
})

// === a local profile is on screen: the master's world is parked, and that is where an apply lands ===

describe('applySectionToStores — a local profile (slave) is on screen', () => {
  const SLAVE = 'slave-1'

  /** `seedTabWorld()`'s world becomes the PARKED master; the screen shows a slave's world (sentinel: `SLAVE-ONLY`). */
  function parkMasterShowSlave(): ParkedWorld {
    seedTabWorld()
    return parkMasterShowSlaveFromCurrent()
  }

  /** Whatever the live stores hold becomes the parked master. */
  function parkMasterShowSlaveFromCurrent(): ParkedWorld {
    const t = useTabStore.getState()
    const w = useWorkspaceStore.getState()
    const parked: ParkedWorld = { tabs: t.tabs, workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId, activeTabId: t.activeTabId }
    useLocalProfilesStore.setState({ slaves: { [SLAVE]: { id: SLAVE, name: 'Slave', createdAt: 1, shownHostIds: [], world: null } }, slaveOrder: [SLAVE], activeProfileId: SLAVE, parkedMaster: parked, worldEpoch: 1 })
    const st = { ...tab('SLAVE-ONLY-t1', tmuxLeaf('SLAVE-ONLY-p1', M)) }
    useTabStore.setState({ tabs: { [st.id]: st }, tabOrder: [st.id], activeTabId: st.id, visitHistory: [st.id], worldId: SLAVE, worldEpoch: 1 })
    useWorkspaceStore.setState({ workspaces: [ws('SLAVE-ONLY-ws', [st.id])], activeWorkspaceId: 'SLAVE-ONLY-ws', worldId: SLAVE, worldEpoch: 1 })
    return parked
  }

  const screen = (): string => JSON.stringify([persistedOf(STORAGE_KEYS.TABS), persistedOf(STORAGE_KEYS.WORKSPACES)])

  it('workspaces: the parked master takes it, the scoped settings of a removed workspace are cleared, the screen is byte-for-byte what it was', async () => {
    parkMasterShowSlave()
    useWorkspaceSettingsStore.setState({ workspaces: { wa: { files: { x: 1 } }, wb: { files: { x: 2 } }, 'SLAVE-ONLY-ws': { files: { x: 3 } } } } as never)
    const liveTabs = useTabStore.getState()
    const liveWs = useWorkspaceStore.getState()
    const before = screen()
    const payload: WorkspacesPayload = { order: ['wb'], workspaces: { wb: { name: 'Renamed' } } }

    const outcome = await applySectionToStores('workspaces', payload, ctx)

    const parked = useLocalProfilesStore.getState().parkedMaster!
    expect(parked.workspaces).toEqual([{ ...ws('wb', ['b1']), name: 'Renamed' }])
    expect(Object.keys(parked.tabs).sort()).toEqual(['b1', 'solo']) // wa's tabs went with it
    expect(parked.activeTabId).toBe('b1') // 'a2' is gone → re-pointed by the rule the live path uses
    expect(Object.keys(useWorkspaceSettingsStore.getState().workspaces).sort()).toEqual(['SLAVE-ONLY-ws', 'wb'])
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
    expect(useTabStore.getState()).toBe(liveTabs)
    expect(useWorkspaceStore.getState()).toBe(liveWs)
    expect(screen()).toBe(before)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('tabs.<id>: the parked master takes it and the hash is the parked world\'s; the screen does not move', async () => {
    const parkedBefore = parkMasterShowSlave()
    const before = screen()
    const payload = JSON.parse(JSON.stringify(buildTabsSection({ ...ws('wa', ['a2']) , activeTabId: 'a2' }, parkedBefore.tabs))) as TabsPayload

    const outcome = await applySectionToStores('tabs.wa', payload, ctx)

    const parked = useLocalProfilesStore.getState().parkedMaster!
    expect(parked.workspaces.find((w) => w.id === 'wa')?.tabs).toEqual(['a2'])
    expect(Object.hasOwn(parked.tabs, 'a1')).toBe(false)
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(buildTabsSection(parked.workspaces.find((w) => w.id === 'wa')!, parked.tabs)) })
    expect(screen()).toBe(before)
    expect(JSON.stringify(parked)).not.toContain('SLAVE-ONLY')
  })

  it('tabs.<id> naming a host unknown here marks nothing in the PARKED master (host ownership §3.2)', async () => {
    parkMasterShowSlave()
    const unknown = syncIdOfSync('air-lab:0unkn0')
    const payload = JSON.parse(JSON.stringify(buildTabsSection(ws('wa', ['a5']), { a5: tab('a5', tmuxLeaf('gone-pane', unknown)) }))) as TabsPayload
    const outcome = await applySectionToStores('tabs.wa', payload, ctx)
    const parked = useLocalProfilesStore.getState().parkedMaster!
    expect(parked.tabs.a5.layout).toEqual(payload.tabs.a5.layout)
    expect(JSON.stringify(parked.tabs.a5)).not.toContain('terminated')
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
  })

  // tabs-local-only §3.6 on the parked path: `writeMasterWorld` re-points the parked world's active tab by the same rule.
  it('tabs.<id> into the PARKED master: its active tab, a kept device-local tab, stays; a removed synced one moves', async () => {
    seedTabWorld()
    const settings = tab('sL', { type: 'leaf', pane: { id: 'p-sL', content: { kind: 'settings', scope: 'global' } } })
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, sL: settings }, activeTabId: 'sL' })
    useWorkspaceStore.setState({ workspaces: [ws('wa', ['a1', 'sL', 'a2'], 'a2'), ws('wb', ['b1'])], activeWorkspaceId: 'wa' })
    parkMasterShowSlaveFromCurrent()
    const before = screen()
    const payload = JSON.parse(JSON.stringify(buildTabsSection(ws('wa', ['a1']), { a1: tab('a1') }))) as TabsPayload

    await applySectionToStores('tabs.wa', payload, ctx)
    let parked = useLocalProfilesStore.getState().parkedMaster!
    expect(parked.activeTabId).toBe('sL')
    expect(parked.tabs.sL).toEqual(settings)
    expect(parked.workspaces.find((w) => w.id === 'wa')?.tabs).toEqual(['a1', 'sL'])
    expect(screen()).toBe(before)

    useLocalProfilesStore.setState({ parkedMaster: { ...parked, tabs: { ...parked.tabs, a2: tab('a2') }, workspaces: [ws('wa', ['a1', 'a2', 'sL'], 'sL'), ws('wb', ['b1'])], activeTabId: 'a2' } })
    await applySectionToStores('tabs.wa', payload, ctx)
    parked = useLocalProfilesStore.getState().parkedMaster!
    expect(parked.activeTabId).toBe('sL') // a2 is gone → the active workspace (wa)'s pointer
    expect(Object.hasOwn(parked.tabs, 'a2')).toBe(false)
  })

  it('tabs.<id> of a workspace only the SLAVE has is unrendered — the screen is not where the master looks', async () => {
    parkMasterShowSlave()
    const before = screen()
    expect(await applySectionToStores('tabs.SLAVE-ONLY-ws', { order: [], tabs: {} }, ctx)).toEqual({ ok: true, hash: null })
    expect(screen()).toBe(before)
  })

  it('settings: scoped by the PARKED master\'s workspaces — the slave\'s own entries are kept, and never hashed', async () => {
    parkMasterShowSlave()
    useWorkspaceSettingsStore.setState({ workspaces: { 'SLAVE-ONLY-ws': { files: { x: 3 } } } } as never)
    const payload = JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds()))) as SettingsPayload
    ;(payload['purdex-workspace-settings'] as { workspaces: Record<string, unknown> }).workspaces = { wa: { files: { x: 9 } } }

    const outcome = await applySectionToStores('settings', payload, ctx)

    expect(useWorkspaceSettingsStore.getState().workspaces).toEqual({ wa: { files: { x: 9 } }, 'SLAVE-ONLY-ws': { files: { x: 3 } } })
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
  })

  it.each(['workspaces', 'tabs.wa', 'settings'] as const)('%s while the master world is UNSETTLED: busy, nothing written, the lock released', async (key) => {
    parkMasterShowSlave()
    useWorkspaceStore.setState({ worldEpoch: 0 }) // the workspace store's rehydrate has not arrived
    const payloads = { workspaces: { order: [], workspaces: {} }, 'tabs.wa': { order: [], tabs: {} }, settings: {} }
    let outcome: unknown
    const writes = await countWrites(async () => {
      outcome = await applySectionToStores(key, payloads[key], ctx)
    })
    expect(outcome).toEqual({ ok: false, reason: 'busy' })
    expect(writes).toBe(0)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })
})

// === host-sync-identity PR 2: the apply speaks WIRE ids ===

describe('applySectionToStores — wire host ids (host-sync-identity §6, §11)', () => {
  const DAEMON = 'mini-lab:278cbm'
  const WIRE = syncIdOfSync(DAEMON)
  const OTHER = 'other-lab:abc123'

  beforeEach(() => {
    // this device: its master IS that daemon, under its own id; plus a host only it has
    useHostStore.setState({ hosts: { [M]: host(M, { daemonId: DAEMON }), onlyb: host('onlyb', { ip: '10.0.0.9', order: 1 }) }, hostOrder: [M, 'onlyb'], activeHostId: M, devHostId: null, runtime: {} })
  })

  it('tabs: a sync id and an alias resolve to the local host; nothing is marked host-removed; the hash is of the wire build', async () => {
    useHostStore.setState({ hosts: { [M]: host(M, { daemonId: DAEMON, syncAliases: ['aaaaaa'] }) }, hostOrder: [M] })
    seedTabWorld()
    const split: PaneLayout = { type: 'split', id: 's9', direction: 'v', children: [tmuxLeaf('canon', WIRE), tmuxLeaf('legacy', 'aaaaaa')], sizes: [50, 50] }
    const payload = buildTabsSection(ws('wa', ['a5']), { a5: tab('a5', split) })
    const outcome = await applySectionToStores('tabs.wa', payload, ctx)
    const layout = useTabStore.getState().tabs.a5.layout as Extract<PaneLayout, { type: 'split' }>
    for (const child of layout.children) expect((child as Extract<PaneLayout, { type: 'leaf' }>).pane.content).toMatchObject({ hostId: M })
    expect(JSON.stringify(layout)).not.toContain('host-removed')
    // the canonical form of what the stores hold: both panes now name the master's sync id
    const wire = buildTabsSection(ws('wa', ['a5']), { a5: tab('a5', { ...split, children: [tmuxLeaf('canon', WIRE), tmuxLeaf('legacy', WIRE)] }) })
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(wire) })
  })

  it('settings: host-settings keys and preset columns resolve to local ids; a column of a host not here is KEPT verbatim (host ownership §3.2)', async () => {
    const other = syncIdOfSync(OTHER)
    const presets = {
      '3col': { enabled: true, columns: [[`sessions:${WIRE}`], [`headless:${other}`], []] },
      '2col': { enabled: true, columns: [[], []] },
      '1col': { enabled: true, columns: [[`sessions:${WIRE}`]] },
    }
    const payload: SettingsPayload = { 'purdex-host-settings': { hosts: { [WIRE]: { mod: { k: 1 } } } }, 'purdex-newtab-layout': { presets } }
    const outcome = await applySectionToStores('settings', payload, ctx)
    expect(outcome).toMatchObject({ ok: true })
    expect(useHostSettingsStore.getState().hosts).toEqual({ [M]: { mod: { k: 1 } } })
    expect(useNewTabLayoutStore.getState().presets['3col'].columns).toEqual([[`sessions:${M}`], [`headless:${other}`], []])
    expect(useNewTabLayoutStore.getState().presets['1col'].columns).toEqual([[`sessions:${M}`]])
    const identity = identityOfSync(useHostStore.getState().hosts)
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(buildSettingsSection(readSettingsSources(), masterWorkspaceIds(), identity)) })
  })

  // Host ownership §3.2: every reference to a host this device lacks is stored verbatim and built verbatim, so a
  // payload carrying them comes back byte-for-byte — its hash is the incoming one and nothing is pushed.
  it('settings: an unknown column and an unknown host-settings key round-trip apply → stores → build byte-for-byte', async () => {
    const unknown = syncIdOfSync(OTHER)
    const now = JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds(), identityOfSync(useHostStore.getState().hosts)))) as SettingsPayload
    const layout = now['purdex-newtab-layout'] as { presets: Record<string, { columns: string[][] }> }
    for (const preset of Object.values(layout.presets)) preset.columns[0] = [...preset.columns[0], `sessions:${unknown}`, `headless:${unknown}`]
    const payload: SettingsPayload = {
      ...now,
      'purdex-host-settings': { hosts: { [WIRE]: { mod: { k: 1 } }, [unknown]: { editor: { homePath: '/srv' } } } },
    }
    const outcome = await applySectionToStores('settings', payload, ctx)
    expect(useHostSettingsStore.getState().hosts).toEqual({ [M]: { mod: { k: 1 } }, [unknown]: { editor: { homePath: '/srv' } } })
    for (const preset of Object.values(useNewTabLayoutStore.getState().presets)) {
      expect(preset.columns[0]).toEqual(expect.arrayContaining([`sessions:${unknown}`, `headless:${unknown}`]))
    }
    const identity = identityOfSync(useHostStore.getState().hosts)
    expect(buildSettingsSection(readSettingsSources(), masterWorkspaceIds(), identity)).toEqual(payload)
    expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
  })

  it('settings: an unknown column survives the New Tab bootstrap too — apply → bootstrap → build is byte-for-byte', async () => {
    const unknown = syncIdOfSync(OTHER)
    clearNewTabRegistry()
    registerNewTabProviderSource(createHostSessionProviderSource())
    registerNewTabProviderSource(createHeadlessProviderSource())
    try {
      expect(useHostStore.persist.hasHydrated()).toBe(true)
      renderHook(() => useNewTabBootstrap()).unmount() // the local host's own blocks placed: the steady state
      const now = JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds(), identityOfSync(useHostStore.getState().hosts)))) as SettingsPayload
      const layout = now['purdex-newtab-layout'] as { presets: Record<string, { columns: string[][] }> }
      for (const preset of Object.values(layout.presets)) preset.columns[0] = [...preset.columns[0], `sessions:${unknown}`, `headless:${unknown}`]
      const payload: SettingsPayload = { ...now, 'purdex-host-settings': { hosts: { [unknown]: { editor: { homePath: '/srv' } } } } }
      expect(await applySectionToStores('settings', payload, ctx)).toMatchObject({ ok: true, hash: await hashSection(payload) })

      renderHook(() => useNewTabBootstrap()).unmount()
      expect(buildSettingsSection(readSettingsSources(), masterWorkspaceIds(), identityOfSync(useHostStore.getState().hosts))).toEqual(payload)
    } finally {
      clearNewTabRegistry()
    }
  })

  // Host ownership plan §0.2 / H1b T4: every apply, however it settles, requests a re-resolve pass — a rollback may
  // have put back a wire id the pass had resolved, and the pass is idempotent and cheap.
  describe('an apply requests a re-resolve pass when it settles', () => {
    it.each([
      ['a resolved outcome', () => buildWorkspacesSection(useWorkspaceStore.getState().workspaces)],
      ['an invalid outcome', () => ({ junk: true })],
    ] as const)('%s → a store holding a resolvable wire id ends on the local id', async (_label, payloadOf) => {
      useHostSettingsStore.setState({ hosts: { [WIRE]: { editor: { homePath: '/w' } } } })
      await applySectionToStores('workspaces', payloadOf(), ctx)
      expect(useHostSettingsStore.getState().hosts).toEqual({ [M]: { editor: { homePath: '/w' } } })
    })

    it('a busy outcome → the pass is requested too (and retried once the lock is free)', async () => {
      vi.useFakeTimers()
      try {
        useHostSettingsStore.setState({ hosts: { [WIRE]: { editor: { homePath: '/w' } } } })
        const grant = useRebuildStore.getState().acquireOperationLock('someone-else')
        expect(await applySectionToStores('workspaces', buildWorkspacesSection(useWorkspaceStore.getState().workspaces), ctx)).toEqual({ ok: false, reason: 'busy' })
        expect(Object.keys(useHostSettingsStore.getState().hosts)).toEqual([WIRE])
        useRebuildStore.getState().releaseOperationLock(grant)
        await vi.advanceTimersByTimeAsync(HOST_RERESOLVE_RETRY_MS)
        expect(Object.keys(useHostSettingsStore.getState().hosts)).toEqual([M])
      } finally {
        __resetHostReresolveForTest()
        vi.useRealTimers()
      }
    })

    // PR #1406 attacker high #3: the pass runs after the apply has settled, never inside its `finally` — a pass that
    // throws must not replace the apply's own outcome or error.
    describe('the pass itself throws', () => {
      function passThrows(): void {
        // On the store API (restorable), not on a state object — zustand copies a state's own props into the next one.
        const realGetState = useRebuildStore.getState
        vi.spyOn(useRebuildStore, 'getState').mockImplementation(() => {
          const st = realGetState()
          return {
            ...st,
            acquireOperationLock: (owner, parent) => {
              if (owner === HOST_RERESOLVE_LOCK_OWNER) throw new Error('pass exploded')
              return st.acquireOperationLock(owner, parent)
            },
          }
        })
        vi.spyOn(console, 'error').mockImplementation(() => {})
      }

      it('a resolved outcome is returned as it was', async () => {
        useHostSettingsStore.setState({ hosts: { [WIRE]: { editor: { homePath: '/w' } } } }) // something to move
        passThrows()
        const payload = buildWorkspacesSection(useWorkspaceStore.getState().workspaces)
        await expect(applySectionToStores('workspaces', payload, ctx)).resolves.toMatchObject({ ok: true, hash: await hashSection(payload) })
        await new Promise((r) => setTimeout(r, 0)) // the scheduled pass has run — and thrown, caught
      })

      it('the apply\'s own error is the one the caller gets', async () => {
        useHostSettingsStore.setState({ hosts: { [WIRE]: { editor: { homePath: '/old' } } } })
        const now = JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds(), identityOfSync(useHostStore.getState().hosts)))) as SettingsPayload
        const payload: SettingsPayload = { ...now, 'purdex-layout': { ...(now['purdex-layout'] as object), tabPosition: 'left' } }
        passThrows()
        vi.spyOn(useLayoutStore, 'setState').mockImplementationOnce(() => { throw new Error('layout write failed') })
        await expect(applySectionToStores('settings', payload, ctx)).rejects.toThrow(/^layout write failed$/)
        await new Promise((r) => setTimeout(r, 0))
      })
    })

    it('a settings apply that THROWS after its rollback restored a wire id still ends with it resolved', async () => {
      // Another window's write landed `WIRE` here and the pass has not run yet; the apply rewrites the key, a later
      // store fails, and the rollback puts `WIRE` back — whatever pass ran between the awaits is undone by it.
      useHostSettingsStore.setState({ hosts: { [WIRE]: { editor: { homePath: '/old' } } } })
      const now = JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds(), identityOfSync(useHostStore.getState().hosts)))) as SettingsPayload
      const payload: SettingsPayload = {
        ...now,
        'purdex-host-settings': { hosts: { [WIRE]: { editor: { homePath: '/new' } } } },
        'purdex-layout': { ...(now['purdex-layout'] as object), tabPosition: 'left' },
      }
      const realRehydrate = useHostSettingsStore.persist.rehydrate
      vi.spyOn(useHostSettingsStore.persist, 'rehydrate').mockImplementationOnce(async () => {
        await realRehydrate()
        requestHostReresolve() // a pass interleaved between the apply's awaits
      })
      vi.spyOn(useLayoutStore, 'setState').mockImplementationOnce(() => { throw new Error('layout write failed') })
      await expect(applySectionToStores('settings', payload, ctx)).rejects.toThrow('layout write failed')
      vi.restoreAllMocks()
      await vi.waitFor(() => expect(useHostSettingsStore.getState().hosts).toEqual({ [M]: { editor: { homePath: '/old' } } }))
    })
  })

  // R1 (PR #1365): the settings apply awaits a rehydrate per store. A host-store change in that gap means the part
  // already written was resolved through a resolver that no longer holds — rolled back, `busy`, retried.
  describe('the host identity moves WHILE the settings apply is awaiting', () => {
    const OTHER_WIRE = syncIdOfSync(OTHER)
    function seed(): { payload: SettingsPayload; before: unknown; persisted: unknown } {
      useHostStore.setState({ hosts: { [M]: host(M, { daemonId: DAEMON }), h2: host('h2', { ip: '10.0.0.2', order: 1 }) }, hostOrder: [M, 'h2'] })
      const now = JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds()))) as SettingsPayload
      const payload: SettingsPayload = {
        ...now,
        'purdex-ui-settings': { ...now['purdex-ui-settings'], dynamicTabName: !UI_DEFAULTS.dynamicTabName },
        'purdex-host-settings': { hosts: { [WIRE]: { mod: { k: 1 } }, [OTHER_WIRE]: { mod: { k: 2 } } } },
        'purdex-newtab-layout': { presets: { '3col': { enabled: true, columns: [[`sessions:${WIRE}`], [`sessions:${OTHER_WIRE}`], []] }, '2col': { enabled: true, columns: [[], []] }, '1col': { enabled: true, columns: [[]] } } },
      }
      return { payload, before: JSON.parse(JSON.stringify(readSettingsSources())) as unknown, persisted: persistedOf(STORAGE_KEYS.HOST_SETTINGS) }
    }
    /** `change` runs once, inside the first rehydrate any settings store awaits. */
    function midApply(change: () => void): void {
      let done = false
      for (const store of [useUISettingsStore, useHostSettingsStore, useNewTabLayoutStore]) {
        const real = store.persist.rehydrate.bind(store.persist)
        vi.spyOn(store.persist, 'rehydrate').mockImplementation(async () => {
          if (!done) {
            done = true
            change()
          }
          await real()
        })
      }
    }

    it.each([
      ['h2 learns its daemonId', () => useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h2: { ...useHostStore.getState().hosts.h2, daemonId: OTHER } } })],
      ['a host is added', () => useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h3: host('h3', { ip: '10.0.0.3', daemonId: 'third:x' }) }, hostOrder: [...useHostStore.getState().hostOrder, 'h3'] })],
      ['a conflict appears (h2 claims the master\'s daemon)', () => useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h2: { ...useHostStore.getState().hosts.h2, daemonId: DAEMON } } })],
    ])('%s → `busy`, every store byte as before; the retry lands', async (_name, change) => {
      const { payload, before, persisted } = seed()
      midApply(change)
      const outcome = await applySectionToStores('settings', payload, ctx)
      vi.restoreAllMocks()
      expect(outcome).toEqual({ ok: false, reason: 'busy' })
      expect(JSON.parse(JSON.stringify(readSettingsSources()))).toEqual(before)
      expect(persistedOf(STORAGE_KEYS.HOST_SETTINGS)).toEqual(persisted)
      expect(persistedOf(STORAGE_KEYS.UI_SETTINGS).dynamicTabName).toBe(UI_DEFAULTS.dynamicTabName)

      const retry = await applySectionToStores('settings', payload, ctx)
      if (identityOfSync(useHostStore.getState().hosts).conflict !== null) {
        expect(retry).toMatchObject({ ok: false, reason: 'invalid', code: 'host-identity-conflict' }) // still refused while it lasts
        useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h2: host('h2', { ip: '10.0.0.2', order: 1 }) } })
        expect(await applySectionToStores('settings', payload, ctx)).toMatchObject({ ok: true })
      } else {
        expect(retry).toMatchObject({ ok: true })
        expect(useUISettingsStore.getState().dynamicTabName).toBe(!UI_DEFAULTS.dynamicTabName)
      }
    })

    it('a host-store change that moves nothing the resolver reads (a rename) is no reason to give up', async () => {
      const { payload } = seed()
      midApply(() => useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h2: { ...useHostStore.getState().hosts.h2, name: 'renamed' } } }))
      expect(await applySectionToStores('settings', payload, ctx)).toMatchObject({ ok: true })
      vi.restoreAllMocks()
    })
  })

  // Follow-up to R1: the tabs apply resolves wire ids under the operation lock, with the write — a host-store change
  // while the lock is being taken cannot leave the panes resolved through the hosts as they were before it.
  it('tabs: a daemonId learned while the operation lock is taken is the one the panes resolve through', async () => {
    useHostStore.setState({ hosts: { [M]: host(M, { daemonId: DAEMON }), h2: host('h2', { ip: '10.0.0.2', order: 1 }) }, hostOrder: [M, 'h2'] })
    seedTabWorld()
    const payload = buildTabsSection(ws('wa', ['a5']), { a5: tab('a5', tmuxLeaf('p5', syncIdOfSync(OTHER))) })
    const real = useRebuildStore.getState().acquireOperationLock
    useRebuildStore.setState({
      acquireOperationLock: (...args: Parameters<typeof real>) => {
        const { hosts } = useHostStore.getState()
        useHostStore.setState({ hosts: { ...hosts, h2: { ...hosts.h2, daemonId: OTHER } } }) // learned at that moment
        return real(...args)
      },
    })
    try {
      const outcome = await applySectionToStores('tabs.wa', payload, ctx)
      const content = (useTabStore.getState().tabs.a5.layout as Extract<PaneLayout, { type: 'leaf' }>).pane.content
      expect(content).toMatchObject({ hostId: 'h2' })
      expect(content).not.toHaveProperty('terminated')
      expect(outcome).toMatchObject({ ok: true, hash: await hashSection(payload) })
    } finally {
      useRebuildStore.setState({ acquireOperationLock: real })
    }
  })

  it('tabs and settings refuse to land while the host identity is in conflict (two local hosts, one daemon)', async () => {
    useHostStore.setState({ hosts: { [M]: host(M, { daemonId: DAEMON }), twin: host('twin', { daemonId: DAEMON }) }, hostOrder: [M, 'twin'] })
    seedTabWorld()
    const tabsBefore = useTabStore.getState().tabs
    expect(await applySectionToStores('tabs.wa', buildTabsSection(ws('wa', ['a1']), { a1: tab('a1') }), ctx)).toMatchObject({ ok: false, reason: 'invalid', code: 'host-identity-conflict' })
    expect(useTabStore.getState().tabs).toBe(tabsBefore)
    expect(await applySectionToStores('settings', { 'purdex-layout': { tabPosition: 'bottom' } }, ctx)).toMatchObject({ ok: false, reason: 'invalid', code: 'host-identity-conflict' })
  })
})

// #1369: the outcome carries the very payload its `hash` was taken of, so the executor can stash it and push the
// write-back without waiting for the collector (which reports a hash once — a pruned repeat never comes again).
describe('applySectionToStores — an ok outcome hands back the payload it hashed (#1369)', () => {
  async function expectOwnPayload(outcome: unknown, built: unknown): Promise<void> {
    const o = outcome as { ok: true; hash: string | null; payload?: unknown }
    expect(o.ok).toBe(true)
    expect(o.payload).toEqual(built)
    expect(o.hash).toBe(await hashSection(o.payload))
  }

  it('settings: the rebuilt section', async () => {
    const payload = JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), masterWorkspaceIds()))) as SettingsPayload
    payload['purdex-layout'] = { ...payload['purdex-layout'], tabPosition: 'bottom' }
    const outcome = await applySectionToStores('settings', payload, ctx)
    await expectOwnPayload(outcome, buildSettingsSection(readSettingsSources(), masterWorkspaceIds()))
  })

  it('workspaces: the rebuilt section', async () => {
    seedTabWorld()
    const outcome = await applySectionToStores('workspaces', { order: ['wb', 'wa'], workspaces: { wa: { name: 'Alpha' }, wb: { name: 'WB' } } }, ctx)
    await expectOwnPayload(outcome, buildWorkspacesSection(useWorkspaceStore.getState().workspaces))
  })

  it('tabs.<id>: the rebuilt section', async () => {
    seedTabWorld()
    const outcome = await applySectionToStores('tabs.wa', buildTabsSection(ws('wa', ['a3']), { a3: tab('a3') }), ctx)
    const wa = useWorkspaceStore.getState().workspaces.find((w) => w.id === 'wa')!
    await expectOwnPayload(outcome, buildTabsSection(wa, useTabStore.getState().tabs))
  })

  it('a null hash carries no payload', async () => {
    seedTabWorld()
    const outcome = await applySectionToStores('tabs.nowhere', buildTabsSection(ws('nowhere', ['z1']), { z1: tab('z1') }), ctx)
    expect(outcome).toEqual({ ok: true, hash: null })
    expect(outcome).not.toHaveProperty('payload')
  })
})
