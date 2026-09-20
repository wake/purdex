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
import type { HostListCache } from '../../stores/useExecutionListStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import type { NexHostEntry } from '../../stores/useNexHostStore'
import { useHostSettingsStore } from '../../stores/useHostSettingsStore'
import { deleteHostCascade } from '../host-lifecycle'
import { getTheme, unregisterTheme } from '../theme-registry'
import { registerBuiltinThemes } from '../register-themes'
import { unregisterLocale } from '../locale-registry'
import { STORAGE_KEYS } from '../storage'
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import { hashSection } from './hash'
import { buildHostsSection, buildSettingsSection, buildTabsSection, buildWorkspacesSection } from './sections'
import type { HostsPayload, SettingsPayload, TabsPayload, WorkspacesPayload } from './types'
import { applySectionToStores, markHostRemovedPanes, readSettingsSources } from './apply-to-stores'

// === fixtures ===

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
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
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
  const stores = [useSessionStore, useAgentStore, useExecutionStore, useExecutionListStore, useNexHostStore, useHostSettingsStore, useHostStore, useTabStore, useWorkspaceStore, useUISettingsStore, useEditorSettingsStore, useThemeStore, useI18nStore, useLayoutStore, useWorkspaceSettingsStore]
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
      ['hosts', { hosts: { [M]: { ...host(M), extra: 1 } }, hostOrder: [M] }],
      ['settings', { 'purdex-unknown': { a: 1 } }],
      ['workspaces', { order: ['wa'], workspaces: {} }],
      ['tabs.wa', { order: ['zz'], tabs: {} }],
    ]
    for (const [key, payload] of bad) {
      let outcome: unknown
      const writes = await countWrites(async () => {
        outcome = await applySectionToStores(key, payload, ctx)
      })
      expect(outcome, key).toMatchObject({ ok: false, reason: 'invalid' })
      expect(writes, key).toBe(0)
    }
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('an unknown section key is invalid', async () => {
    expect(await applySectionToStores('bogus' as never, {}, ctx)).toMatchObject({ ok: false, reason: 'invalid' })
  })

  it('a null payload is invalid for hosts, settings and workspaces, and writes nothing', async () => {
    for (const key of ['hosts', 'settings', 'workspaces'] as const) {
      let outcome: unknown
      const writes = await countWrites(async () => {
        outcome = await applySectionToStores(key, null, ctx)
      })
      expect(outcome, key).toMatchObject({ ok: false, reason: 'invalid' })
      expect(writes, key).toBe(0)
    }
  })
})

describe('applySectionToStores — hosts', () => {
  it('replaces hosts + order, keeps device-local focus and the runtime of surviving hosts, drops removed runtime', async () => {
    const h3 = host('host-three', { ip: '10.0.0.3', order: 1 })
    useHostStore.getState().setRuntime(M, { status: 'connected' })
    useHostStore.getState().setRuntime(H2, { status: 'connected' })
    useHostStore.setState({ activeHostId: M, devHostId: H2 })
    const payload = hostsPayloadOf([host(M, { name: 'renamed' }), h3])

    const outcome = await applySectionToStores('hosts', payload, ctx)

    const s = useHostStore.getState()
    expect(Object.keys(s.hosts)).toEqual([M, 'host-three'])
    expect(s.hosts[M].name).toBe('renamed')
    expect(s.hostOrder).toEqual([M, 'host-three'])
    expect(s.activeHostId).toBe(M)
    expect(s.devHostId).toBeNull() // its host is gone
    expect(s.runtime).toEqual({ [M]: { status: 'connected' } })
    expect(outcome).toEqual({ ok: true, hash: await hashSection(buildHostsSection(s)) })
    expect(outcome).toEqual({ ok: true, hash: await hashSection(payload) })
    expect(persistedOf(STORAGE_KEYS.HOSTS).hostOrder).toEqual([M, 'host-three'])
  })

  it('refuses a payload that removes the master host', async () => {
    const before = useHostStore.getState().hosts
    const outcome = await applySectionToStores('hosts', hostsPayloadOf([host(H2)]), ctx)
    expect(outcome).toMatchObject({ ok: false, reason: 'invalid' })
    expect(useHostStore.getState().hosts).toBe(before)
  })

  it.each([
    ['ip', { ip: '10.9.9.9' }],
    ['port', { port: 1 }],
    ['token', { token: 'other' }],
    ['token cleared', { token: null }],
  ])("refuses a payload that changes the master host's %s", async (_name, over) => {
    const before = useHostStore.getState().hosts
    const outcome = await applySectionToStores('hosts', hostsPayloadOf([host(M, over as Partial<HostConfig>), host(H2)]), ctx)
    expect(outcome).toMatchObject({ ok: false, reason: 'invalid' })
    expect(useHostStore.getState().hosts).toBe(before)
  })

  it('refuses a payload that leaves zero hosts', async () => {
    const outcome = await applySectionToStores('hosts', { hosts: {}, hostOrder: [] }, ctx)
    expect(outcome).toMatchObject({ ok: false, reason: 'invalid' })
    expect(Object.keys(useHostStore.getState().hosts)).toHaveLength(2)
  })

  it('returns the hash of what the store holds, not the SOT hash, when the sanitiser changed the payload', async () => {
    const payload = hostsPayloadOf([host(M, { icon: 'NotARealIcon' }), host(H2)])
    const outcome = await applySectionToStores('hosts', payload, ctx)
    expect(useHostStore.getState().hosts[M].icon).toBeUndefined()
    expect(outcome).toEqual({ ok: true, hash: await hashSection(buildHostsSection(useHostStore.getState())) })
    expect(outcome).not.toEqual({ ok: true, hash: await hashSection(payload) })
    // and the sanitised value is what got persisted
    expect((persistedOf(STORAGE_KEYS.HOSTS).hosts as Record<string, HostConfig>)[M].icon).toBeUndefined()
  })
})

describe('applySectionToStores — hosts: removing a host is the app\'s own host removal', () => {
  /** Both hosts own data in every per-host store, and there are live panes on each. */
  function seedHostWorld(): void {
    const split: PaneLayout = { type: 'split', id: 's1', direction: 'h', children: [tmuxLeaf('on-h2', H2), tmuxLeaf('on-m', M)], sizes: [60, 40] }
    useTabStore.setState({ tabs: { t1: tab('t1', split), t2: tab('t2', tmuxLeaf('p-t2', H2)), t3: tab('t3') }, tabOrder: ['t1', 't2', 't3'], activeTabId: 't2', visitHistory: ['t1'] })
    useWorkspaceStore.setState({ workspaces: [ws('wa', ['t1', 't2', 't3'], 't2')], activeWorkspaceId: 'wa' })
    for (const h of [M, H2]) {
      useHostStore.getState().setRuntime(h, { status: 'connected' })
      useSessionStore.getState().replaceHost(h, [{ code: 'dev001', name: 'Dev', mode: 'terminal', cwd: '~' }] as never)
      useAgentStore.getState().handleNormalizedEvent(h, 'dev001', { agent_type: 'cc', status: 'idle', subagents: [], raw_event_name: 'Stop', broadcast_ts: 1 } as never)
      useExecutionStore.getState().applyEvents(h, 'exc_1', [{ seq: 1, execution_id: 'exc_1', kind: 'assistant', payload: { type: 'assistant' }, created_at: 0 }] as never)
    }
    const nexEntry: NexHostEntry = { info: null, capabilities: null, phase: 'unavailable', error: 'x', fetchedAt: 1, generation: 1, fingerprint: '' }
    const listCache: HostListCache = { items: [], phase: 'ready', error: null, lastSeq: 4, refreshRevision: 2 }
    useNexHostStore.setState({ byHost: { [M]: nexEntry, [H2]: nexEntry } })
    useExecutionListStore.setState({ byHost: { [M]: listCache, [H2]: listCache } })
    useHostSettingsStore.setState({ hosts: { [M]: { editor: { homePath: '/m' } }, [H2]: { editor: { homePath: '/h2' } } } } as never)
    useHostStore.setState({ activeHostId: H2, devHostId: H2 })
  }

  /** Everything a host removal is allowed to touch, as plain data. */
  function world(): unknown {
    const h = useHostStore.getState()
    const t = useTabStore.getState()
    const w = useWorkspaceStore.getState()
    const a = useAgentStore.getState()
    return JSON.parse(JSON.stringify({
      hosts: { hosts: h.hosts, hostOrder: h.hostOrder, activeHostId: h.activeHostId, devHostId: h.devHostId, runtime: h.runtime },
      tabs: { tabs: t.tabs, tabOrder: t.tabOrder, activeTabId: t.activeTabId, visitHistory: t.visitHistory },
      workspaces: { workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId },
      sessions: useSessionStore.getState().sessions,
      agent: { lastEvents: a.lastEvents, statuses: a.statuses, unread: a.unread, models: a.models, agentTypes: a.agentTypes },
      executions: Object.keys(useExecutionStore.getState().executions),
      executionList: useExecutionListStore.getState().byHost,
      nex: useNexHostStore.getState().byHost,
      hostSettings: useHostSettingsStore.getState().hosts,
    }))
  }

  it('ends in the same state as deleting that host by hand (keep-tabs mode)', async () => {
    seedHostWorld()
    const before = world()
    deleteHostCascade(H2, false)
    const byHand = world()
    expect(byHand).not.toEqual(before)

    resetStores()
    seedHostWorld()
    expect(world()).toEqual(before) // same starting point
    const outcome = await applySectionToStores('hosts', hostsPayloadOf([host(M)]), ctx)

    expect(outcome).toMatchObject({ ok: true })
    expect(world()).toEqual(byHand)
    // …and the fixture is not vacuous
    const w = byHand as { sessions: object; executions: string[]; hostSettings: object; nex: object; executionList: object; hosts: { activeHostId: string } }
    expect(Object.keys(w.sessions)).toEqual([M])
    expect(w.executions).toEqual([`${M}:exc_1`])
    expect(Object.keys(w.hostSettings)).toEqual([M])
    expect(Object.keys(w.nex)).toEqual([M])
    expect(Object.keys(w.executionList)).toEqual([M])
    expect(w.hosts.activeHostId).toBe(M)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('marks the panes already here that sit on the removed host, and only those', async () => {
    seedHostWorld()
    await applySectionToStores('hosts', hostsPayloadOf([host(M)]), ctx)
    const { tabs } = useTabStore.getState()
    const split = tabs.t1.layout as Extract<PaneLayout, { type: 'split' }>
    expect(split.children[0]).toMatchObject({ pane: { content: { hostId: H2, terminated: 'host-removed' } } })
    expect((split.children[1] as Extract<PaneLayout, { type: 'leaf' }>).pane.content).not.toHaveProperty('terminated')
    expect(split.sizes).toEqual([60, 40])
    expect(tabs.t2.layout).toMatchObject({ pane: { content: { terminated: 'host-removed' } } })
    expect((tabs.t3.layout as Extract<PaneLayout, { type: 'leaf' }>).pane.content).not.toHaveProperty('terminated')
  })

  it('busy: removing a host needs the operation lock — held elsewhere, NOTHING is written', async () => {
    seedHostWorld()
    const before = world()
    const grant = useRebuildStore.getState().acquireOperationLock('someone-else')
    let outcome: unknown
    const writes = await countWrites(async () => {
      outcome = await applySectionToStores('hosts', hostsPayloadOf([host(M, { name: 'renamed' })]), ctx)
    })
    expect(outcome).toEqual({ ok: false, reason: 'busy' })
    expect(writes).toBe(0)
    expect(world()).toEqual(before)
    expect(useRebuildStore.getState().lockGrant).toBe(grant)
  })

  it('an apply that removes no host does not take the lock: it lands even while the lock is held', async () => {
    seedHostWorld()
    const grant = useRebuildStore.getState().acquireOperationLock('someone-else')
    const h3 = host('host-three', { ip: '10.0.0.3', order: 2 })
    const outcome = await applySectionToStores('hosts', hostsPayloadOf([host(M, { name: 'renamed' }), host(H2, { ip: '10.0.0.2', order: 1 }), h3]), ctx)
    expect(outcome).toMatchObject({ ok: true })
    expect(useHostStore.getState().hosts[M].name).toBe('renamed')
    expect(Object.keys(useHostStore.getState().hosts)).toEqual([M, H2, 'host-three'])
    expect(useRebuildStore.getState().lockGrant).toBe(grant)
  })

  it('removes the last local hosts too when the master is new here (removeHost\'s one-host veto does not apply to a replace)', async () => {
    useHostStore.setState({ hosts: { [H2]: host(H2) }, hostOrder: [H2], activeHostId: H2, devHostId: null })
    useSessionStore.getState().replaceHost(H2, [{ code: 'dev001', name: 'Dev', mode: 'terminal', cwd: '~' }] as never)
    const outcome = await applySectionToStores('hosts', hostsPayloadOf([host(M)]), ctx)
    expect(outcome).toMatchObject({ ok: true })
    expect(Object.keys(useHostStore.getState().hosts)).toEqual([M])
    expect(useSessionStore.getState().sessions[H2]).toBeUndefined()
  })
})

describe('applySectionToStores — settings', () => {
  const settingsNow = (): SettingsPayload => JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources()))) as SettingsPayload

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
    expect(outcome).toEqual({ ok: true, hash: await hashSection(payload) })
    expect(outcome).toEqual({ ok: true, hash: await hashSection(buildSettingsSection(readSettingsSources())) })
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

  it('editor preferences are device-local: not a settings source, and a payload carrying them is invalid', async () => {
    expect(Object.keys(readSettingsSources())).toHaveLength(8)
    expect(readSettingsSources()).not.toHaveProperty('purdex-editor-settings')
    const payload = { ...settingsNow(), 'purdex-editor-settings': { fontSize: 20 } }
    let outcome: unknown
    const writes = await countWrites(async () => {
      outcome = await applySectionToStores('settings', payload, ctx)
    })
    expect(outcome).toMatchObject({ ok: false, reason: 'invalid' })
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
    expect(outcome).toEqual({ ok: false, reason: 'invalid', detail: expect.stringContaining('purdex-layout.tabPosition') })
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
    expect(outcome).not.toEqual({ ok: true, hash: await hashSection(dropped) }) // honestly dirty
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
    expect(outcome).toEqual({ ok: true, hash: await hashSection(payload) })
    expect(outcome).toEqual({ ok: true, hash: await hashSection(buildWorkspacesSection(s.workspaces)) })
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
    expect(outcome).toEqual({ ok: true, hash: await hashSection(payload) })
    expect(outcome).toEqual({ ok: true, hash: await hashSection(buildTabsSection(w.workspaces[0], t.tabs)) })
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('keeps the global active tab while it survives', async () => {
    seedTabWorld()
    await applySectionToStores('tabs.wa', incomingFor([tab('a2'), tab('a9')]), ctx)
    expect(useTabStore.getState().activeTabId).toBe('a2')
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

  it('marks an arriving pane of a host unknown here as host-removed, and reports the honest hash', async () => {
    seedTabWorld()
    const split: PaneLayout = { type: 'split', id: 's9', direction: 'v', children: [tmuxLeaf('gone-pane', 'host-gone'), tmuxLeaf('ok-pane', M)], sizes: [50, 50] }
    const payload = incomingFor([tab('a5', split)])
    const outcome = await applySectionToStores('tabs.wa', payload, ctx)
    const layout = useTabStore.getState().tabs.a5.layout as Extract<PaneLayout, { type: 'split' }>
    expect(layout.children[0]).toMatchObject({ pane: { content: { hostId: 'host-gone', terminated: 'host-removed' } } })
    expect((layout.children[1] as Extract<PaneLayout, { type: 'leaf' }>).pane.content).not.toHaveProperty('terminated')
    expect(outcome).not.toEqual({ ok: true, hash: await hashSection(payload) })
  })

  it('null payload: a workspace still known has its tabs emptied; an unknown one is a no-op', async () => {
    seedTabWorld()
    const outcome = await applySectionToStores('tabs.wa', null, ctx)
    expect(useWorkspaceStore.getState().workspaces[0]).toMatchObject({ id: 'wa', tabs: [], activeTabId: null })
    expect(Object.keys(useTabStore.getState().tabs).sort()).toEqual(['b1', 'solo'])
    expect(outcome).toEqual({ ok: true, hash: await hashSection({ order: [], tabs: {} }) })

    let second: unknown
    const writes = await countWrites(async () => {
      second = await applySectionToStores('tabs.nowhere', null, ctx)
    })
    expect(second).toEqual({ ok: true, hash: null })
    expect(writes).toBe(0)
  })
})

describe('markHostRemovedPanes', () => {
  it('returns the same object when every host is known, and never overwrites an existing reason', () => {
    const live = tmuxLeaf('p1', M)
    expect(markHostRemovedPanes(live, new Set([M]))).toBe(live)
    const dead: PaneLayout = { type: 'leaf', pane: { id: 'p2', content: { kind: 'tmux-session', hostId: 'x', sessionCode: 'c', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i', terminated: 'session-closed' } } }
    expect(markHostRemovedPanes(dead, new Set())).toBe(dead)
    const other: PaneLayout = { type: 'leaf', pane: { id: 'p3', content: { kind: 'new-tab' } as never } }
    expect(markHostRemovedPanes(other, new Set())).toBe(other)
  })
})
