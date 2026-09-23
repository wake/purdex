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
import { useNewTabLayoutStore } from '../../stores/useNewTabLayoutStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../../stores/useLocalProfilesStore'
import { deleteHostCascade } from '../host-lifecycle'
import { getTheme, unregisterTheme } from '../theme-registry'
import { registerBuiltinThemes } from '../register-themes'
import { getLocale, unregisterLocale } from '../locale-registry'
import { STORAGE_KEYS } from '../storage'
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import { hashSection } from './hash'
import { masterWorkspaceIds as masterWorkspaceIdsOrNull } from './master-world'
import { buildHostsSection, buildSettingsSection, buildTabsSection, buildWorkspacesSection } from './sections'
import type { HostsPayload, SettingsPayload, TabsPayload, WorkspacesPayload } from './types'
import { INVALID_REASONS, applySectionToStores, markHostRemovedPanes, readSettingsSources } from './apply-to-stores'
import { identityOfSync, syncIdOfSync } from './host-identity'

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

  it('a null payload is invalid for hosts, settings and workspaces, and writes nothing', async () => {
    for (const key of ['hosts', 'settings', 'workspaces'] as const) {
      let outcome: unknown
      const writes = await countWrites(async () => {
        outcome = await applySectionToStores(key, null, ctx)
      })
      expect(outcome, key).toMatchObject({ ok: false, reason: 'invalid', code: 'deleted' })
      expect(writes, key).toBe(0)
    }
  })

  it('every invalid outcome carries a code from the closed list, and INVALID_REASONS is that list', async () => {
    expect([...INVALID_REASONS].sort()).toEqual([
      'changes-master-host', 'deleted', 'duplicate-host-alias', 'duplicate-host-identity', 'host-identity-conflict', 'malformed', 'no-host', 'rejected-settings',
      'removes-master-host', 'unknown-section',
    ])
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
    expect(outcome).toMatchObject({ ok: false, reason: 'invalid', code: 'removes-master-host' })
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
    expect(outcome).toMatchObject({ ok: false, reason: 'invalid', code: 'changes-master-host' })
    expect(useHostStore.getState().hosts).toBe(before)
  })

  it('refuses a payload that leaves zero hosts', async () => {
    const outcome = await applySectionToStores('hosts', { hosts: {}, hostOrder: [] }, ctx)
    expect(outcome).toMatchObject({ ok: false, reason: 'invalid', code: 'no-host' })
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

  describe('a store write fails AFTER the cascade ran', () => {
    const realEnsure = useNexHostStore.getState().ensure
    let ensure: ReturnType<typeof vi.fn<(hostId: string) => Promise<void>>>

    beforeEach(() => {
      ensure = vi.fn<(hostId: string) => Promise<void>>(() => Promise.resolve())
      useNexHostStore.setState({ ensure })
    })
    afterEach(() => useNexHostStore.setState({ ensure: realEnsure }))

    const hostSlice = () => {
      const h = useHostStore.getState()
      return JSON.parse(JSON.stringify({ hosts: h.hosts, hostOrder: h.hostOrder, activeHostId: h.activeHostId, devHostId: h.devHostId, runtime: h.runtime }))
    }

    /** The three stores the cascade clears and its own undo does NOT bring back — by reference, so "equal" means "the very same entries". */
    const threeStores = () => ({
      executions: useExecutionStore.getState().executions,
      list: useExecutionListStore.getState().byHost,
      nex: useNexHostStore.getState().byHost,
    })

    /** A richer H2: a second execution holding a lease, so the snapshot has more than one entry and more than default fields. */
    function seedRich(): void {
      seedHostWorld()
      useExecutionStore.getState().applyEvents(H2, 'exc_2', [{ seq: 7, execution_id: 'exc_2', kind: 'assistant', payload: { type: 'assistant' }, created_at: 0 }] as never)
      useExecutionStore.getState().setLease(H2, 'exc_2', { leaseId: 'ls_1', expiresAt: 9_999_999_999_999 })
    }

    /** The n-th `useHostStore.setState` made by the apply throws (1 = staging, 2 = the final write, 3 = publish). */
    function failHostWrite(n: number, message = 'host write failed'): void {
      const real = useHostStore.setState
      let calls = 0
      vi.spyOn(useHostStore, 'setState').mockImplementation((...args) => {
        if (++calls === n) throw new Error(message)
        real(...(args as Parameters<typeof real>))
      })
    }

    it('the host slice is back — the removed host\'s runtime row included — and so is what the cascade\'s own undo restores', async () => {
      seedRich()
      const before = hostSlice()
      failHostWrite(2)
      await expect(applySectionToStores('hosts', hostsPayloadOf([host(M)]), ctx)).rejects.toThrow(/^host write failed$/)
      vi.mocked(useHostStore.setState).mockRestore()
      expect(hostSlice()).toEqual(before)
      expect(useHostStore.getState().runtime[H2]).toEqual({ status: 'connected' })
      expect(useSessionStore.getState().sessions[H2]).toHaveLength(1)
      expect(useRebuildStore.getState().lockedBy).toBeNull()
    })

    it('execution / execution-list / nex-host: the removed host\'s entries are back, the very same ones, and the other host\'s were never touched', async () => {
      seedRich()
      const before = threeStores()
      expect(Object.keys(before.executions).sort()).toEqual([`${H2}:exc_1`, `${H2}:exc_2`, `${M}:exc_1`].sort())
      failHostWrite(2)
      await expect(applySectionToStores('hosts', hostsPayloadOf([host(M)]), ctx)).rejects.toThrow(/^host write failed$/)
      vi.mocked(useHostStore.setState).mockRestore()

      const after = threeStores()
      expect(after.executions).toEqual(before.executions)
      expect(after.list).toEqual(before.list)
      expect(after.nex).toEqual(before.nex)
      for (const key of Object.keys(before.executions)) expect(after.executions[key], key).toBe(before.executions[key])
      for (const h of [M, H2]) {
        expect(after.list[h], h).toBe(before.list[h])
        expect(after.nex[h], h).toBe(before.nex[h])
      }
      expect(after.executions[`${H2}:exc_2`].lease).toEqual({ leaseId: 'ls_1', expiresAt: 9_999_999_999_999 })
      expect(ensure).not.toHaveBeenCalled() // restored as it was: nothing to re-fetch
    })

    it('an entry that appeared for the removed host DURING the cascade (a lease hook writing `lease: null`) does not survive the restore', async () => {
      seedRich()
      const before = threeStores()
      // what a mounted useExecutionLease does, synchronously, the moment its host leaves the store
      const unsub = useHostStore.subscribe((next, prev) => {
        if (prev.hosts[H2] && !next.hosts[H2]) {
          useExecutionStore.getState().setLease(H2, 'exc_2', null)
          useExecutionStore.getState().setLease(H2, 'exc_ghost', null)
        }
      })
      failHostWrite(2)
      await expect(applySectionToStores('hosts', hostsPayloadOf([host(M)]), ctx)).rejects.toThrow('host write failed')
      unsub()
      vi.mocked(useHostStore.setState).mockRestore()
      expect(threeStores().executions).toEqual(before.executions)
    })

    // Why `runtime[H]` may be restored verbatim (`connected` included): the
    // connection layer tears a host's WS down from a React effect, and no effect
    // can run inside a synchronous block. From the staging write to the end of the
    // rollback there must therefore be NO await — this pins it on the last
    // fallible step (publish).
    it('from the cascade to the end of the rollback nothing is awaited: everything is back before the promise is even looked at', () => {
      seedRich()
      const before = hostSlice()
      const three = threeStores()
      failHostWrite(3)
      const pending = applySectionToStores('hosts', hostsPayloadOf([host(M)]), ctx)
      vi.mocked(useHostStore.setState).mockRestore()
      expect(hostSlice()).toEqual(before) // synchronously
      expect(threeStores()).toEqual(three)
      return expect(pending).rejects.toThrow('host write failed')
    })

    it('a restore that throws is not swallowed: the original error survives and says the rollback is incomplete', async () => {
      seedRich()
      const before = hostSlice()
      failHostWrite(2)
      const realNexSet = useNexHostStore.setState
      vi.spyOn(useNexHostStore, 'setState').mockImplementation((...args) => {
        const patch = args[0] as { byHost?: unknown }
        if (typeof patch === 'function') throw new Error('nex restore blew up') // the rollback's functional merge; afterEach's plain patch passes
        realNexSet(...(args as Parameters<typeof realNexSet>))
      })
      const failure = await applySectionToStores('hosts', hostsPayloadOf([host(M)]), ctx).then(() => null, (e: Error) => e)
      vi.mocked(useHostStore.setState).mockRestore()
      vi.mocked(useNexHostStore.setState).mockRestore()
      expect(failure?.message).toMatch(/^host write failed/)
      expect(failure?.message).toMatch(/rollback incomplete/)
      expect(failure?.message).toMatch(/nex restore blew up/)
      expect(hostSlice()).toEqual(before)
      expect(Object.keys(useExecutionStore.getState().executions)).toContain(`${H2}:exc_2`) // the others were still restored
    })
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
    expect(outcome).toEqual({ ok: true, hash: await hashSection(payload) })
    expect(outcome).toEqual({ ok: true, hash: await hashSection(buildSettingsSection(readSettingsSources(), masterWorkspaceIds())) })
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
    expect(outcome).toEqual({ ok: true, hash: await hashSection(filtered) })
    expect(outcome).not.toEqual({ ok: true, hash: await hashSection(payload) })
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
      expect(outcome).toEqual({ ok: true, hash: await hashSection(payload) })
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
    expect(outcome).toEqual({ ok: true, hash: await hashSection(upcast) })
    expect(outcome).not.toEqual({ ok: true, hash: await hashSection(legacy) })
  })

  it('editor preferences are device-local: not a settings source, and a payload carrying them is invalid', async () => {
    expect(Object.keys(readSettingsSources())).toHaveLength(8)
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
    expect(outcome).not.toEqual({ ok: true, hash: await hashSection(dropped) }) // honestly dirty
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

  it('a later store throws → the eight stores, both registries, <html> theme/lang and `t` are all as before', async () => {
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
    expect(outcome).toEqual({ ok: true, hash: await hashSection(payload) }) // converged: the builder leaves it out again
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
    useLocalProfilesStore.setState({ slaves: { [SLAVE]: { id: SLAVE, name: 'Slave', createdAt: 1, world: null } }, slaveOrder: [SLAVE], activeProfileId: SLAVE, parkedMaster: parked, worldEpoch: 1 })
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
    expect(outcome).toEqual({ ok: true, hash: await hashSection(payload) })
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
    expect(outcome).toEqual({ ok: true, hash: await hashSection(buildTabsSection(parked.workspaces.find((w) => w.id === 'wa')!, parked.tabs)) })
    expect(screen()).toBe(before)
    expect(JSON.stringify(parked)).not.toContain('SLAVE-ONLY')
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
    expect(outcome).toEqual({ ok: true, hash: await hashSection(payload) })
  })

  it('hosts: a removed host is marked host-removed in the PARKED master too (and on screen), through the app\'s own cascade', async () => {
    seedTabWorld()
    useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, b1: tab('b1', tmuxLeaf('p-b1', H2)) } })
    parkMasterShowSlaveFromCurrent()
    useTabStore.setState({ tabs: { 'SLAVE-ONLY-t1': tab('SLAVE-ONLY-t1', tmuxLeaf('SLAVE-ONLY-p1', H2)) } })
    const payload: HostsPayload = JSON.parse(JSON.stringify(buildHostsSection({ hosts: { [M]: host(M) }, hostOrder: [M] } as never)))

    expect((await applySectionToStores('hosts', payload, ctx)).ok).toBe(true)

    const terminated = (layout: PaneLayout): unknown => (layout.type === 'leaf' && layout.pane.content.kind === 'tmux-session' ? layout.pane.content.terminated : 'n/a')
    const parked = useLocalProfilesStore.getState().parkedMaster!
    expect(terminated(parked.tabs.b1.layout)).toBe('host-removed')
    expect(terminated(parked.tabs.a2.layout)).toBeUndefined()
    expect(terminated(useTabStore.getState().tabs['SLAVE-ONLY-t1'].layout)).toBe('host-removed')
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
  /** The same daemon under ANOTHER device's local id, as that device's canonical build sends it. */
  const canonicalFromA = (over: Partial<HostConfig> = {}, extra: HostConfig[] = []): HostsPayload =>
    buildHostsSection({ hosts: Object.fromEntries([host('aaaaaa', { daemonId: DAEMON, ...over }), ...extra].map((h) => [h.id, h])), hostOrder: ['aaaaaa', ...extra.map((h) => h.id)] })

  beforeEach(() => {
    // this device: its master IS that daemon, under its own id; plus a host only it has
    useHostStore.setState({ hosts: { [M]: host(M, { daemonId: DAEMON }), onlyb: host('onlyb', { ip: '10.0.0.9', order: 1 }) }, hostOrder: [M, 'onlyb'], activeHostId: M, devHostId: null, runtime: {} })
  })

  it('hosts: the canonical row updates the master IN PLACE (its local id kept), the host only this device has is removed, and the hash is of the wire build', async () => {
    const payload = canonicalFromA({ name: 'mlab by A' })
    const outcome = await applySectionToStores('hosts', payload, ctx)
    const s = useHostStore.getState()
    expect(Object.keys(s.hosts)).toEqual([M])
    expect(s.hosts[M]).toMatchObject({ id: M, name: 'mlab by A', daemonId: DAEMON, syncAliases: ['aaaaaa'] })
    expect(s.hostOrder).toEqual([M])
    // the wire build: A's row plus THIS device's own id as an alias (its ordinal-2-era key) — one push, then agreed
    const rebuilt = buildHostsSection(s)
    expect((rebuilt.hosts[WIRE] as HostConfig & { aliases?: string[] }).aliases).toEqual(['aaaaaa', M])
    expect(outcome).toEqual({ ok: true, hash: await hashSection(rebuilt) })
    expect(outcome).not.toEqual({ ok: true, hash: await hashSection(payload) })
  })

  it('hosts: a canonical row nobody matches is created under a NEW random local id (never its sync id); its aliases land in syncAliases', async () => {
    const other = { ...host('xxxxxx', { ip: '10.0.0.7', daemonId: OTHER, order: 1 }), syncAliases: ['legacy1'] }
    const payload = canonicalFromA({}, [other])
    const outcome = await applySectionToStores('hosts', payload, ctx)
    const s = useHostStore.getState()
    const created = s.hostOrder[1]
    expect(created).toMatch(/^[0-9a-z]{6}$/)
    expect(created).not.toBe('xxxxxx')
    expect(s.hosts[created]).toMatchObject({ daemonId: OTHER, syncAliases: ['legacy1', 'xxxxxx'] })
    expect(outcome).toEqual({ ok: true, hash: await hashSection(buildHostsSection(s)) })
  })

  it('hosts: an ORDINAL-2 row (A\'s local id as key, with daemonId) is matched by daemonId; its key becomes an alias; the rebuilt hash is canonical (one push)', async () => {
    const legacy: HostsPayload = { hosts: { aaaaaa: host('aaaaaa', { daemonId: DAEMON }) }, hostOrder: ['aaaaaa'] }
    const outcome = await applySectionToStores('hosts', legacy, ctx)
    const s = useHostStore.getState()
    expect(Object.keys(s.hosts)).toEqual([M])
    expect(s.hosts[M].syncAliases).toEqual(['aaaaaa'])
    const rebuilt = buildHostsSection(s)
    expect(Object.keys(rebuilt.hosts)).toEqual([WIRE])
    expect((rebuilt.hosts[WIRE] as HostConfig & { aliases?: string[] }).aliases).toEqual(['aaaaaa', M])
    expect(outcome).toEqual({ ok: true, hash: await hashSection(rebuilt) })
    expect(outcome).not.toEqual({ ok: true, hash: await hashSection(legacy) })
  })

  it('master by IDENTITY: a payload without the master daemon\'s row → removes-master-host; its row at another ip → changes-master-host; nothing written', async () => {
    const before = useHostStore.getState().hosts
    const without = buildHostsSection({ hosts: { zz: host('zz', { daemonId: OTHER }) }, hostOrder: ['zz'] })
    expect(await applySectionToStores('hosts', without, ctx)).toMatchObject({ ok: false, reason: 'invalid', code: 'removes-master-host' })
    expect(await applySectionToStores('hosts', canonicalFromA({ ip: '10.9.9.9' }), ctx)).toMatchObject({ ok: false, reason: 'invalid', code: 'changes-master-host' })
    expect(await applySectionToStores('hosts', canonicalFromA({ token: 'other' }), ctx)).toMatchObject({ ok: false, reason: 'invalid', code: 'changes-master-host' })
    expect(useHostStore.getState().hosts).toBe(before)
  })

  // A2 (PR #1365): an alias listed by two rows (or equal to another row's key) would make every legacy id that
  // goes through it ambiguous — tabs / presets resolved to nothing, panes branded host-removed. Refused whole.
  it.each([
    ['two canonical rows list the same alias', (p: HostsPayload) => {
      const other = syncIdOfSync(OTHER)
      ;(p.hosts[other] as HostConfig & { aliases?: string[] }).aliases = ['shared', 'zzzzzz']
      ;(p.hosts[WIRE] as HostConfig & { aliases?: string[] }).aliases = ['aaaaaa', 'shared']
    }],
    ['an alias is another row\'s (legacy) key', (p: HostsPayload) => {
      p.hosts.legacy1 = { ...host('legacy1', { ip: '10.0.0.8', order: 2 }) }
      p.hostOrder.push('legacy1')
      ;(p.hosts[WIRE] as HostConfig & { aliases?: string[] }).aliases = ['aaaaaa', 'legacy1']
    }],
  ])('duplicate-host-alias: %s → invalid before anything is applied; no store written', async (_name, edit) => {
    seedTabWorld()
    const payload = JSON.parse(JSON.stringify(canonicalFromA({}, [host('xxxxxx', { ip: '10.0.0.7', daemonId: OTHER, order: 1 })]))) as HostsPayload
    edit(payload)
    let outcome: unknown
    const writes = await countWrites(async () => {
      outcome = await applySectionToStores('hosts', payload, ctx)
    })
    expect(outcome).toMatchObject({ ok: false, reason: 'invalid', code: 'duplicate-host-alias' })
    expect(writes).toBe(0) // hosts, tabs, settings: all as they were
  })

  it('duplicate-host-identity: two rows for one daemon; host-identity-conflict: two LOCAL hosts claim the row\'s daemon — nothing written', async () => {
    const two: HostsPayload = { hosts: { a1: host('a1', { daemonId: DAEMON }), a2: host('a2', { daemonId: DAEMON }) }, hostOrder: ['a1', 'a2'] }
    expect(await applySectionToStores('hosts', two, ctx)).toMatchObject({ ok: false, reason: 'invalid', code: 'duplicate-host-identity' })
    useHostStore.setState({ hosts: { [M]: host(M, { daemonId: DAEMON }), twin: host('twin', { daemonId: DAEMON }) }, hostOrder: [M, 'twin'] })
    const before = useHostStore.getState().hosts
    expect(await applySectionToStores('hosts', canonicalFromA(), ctx)).toMatchObject({ ok: false, reason: 'invalid', code: 'host-identity-conflict' })
    expect(useHostStore.getState().hosts).toBe(before)
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
    expect(outcome).toEqual({ ok: true, hash: await hashSection(wire) })
  })

  it('settings: host-settings keys and preset columns resolve to local ids; a column of a host not here is left out (the hash says so)', async () => {
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
    expect(useNewTabLayoutStore.getState().presets['3col'].columns).toEqual([[`sessions:${M}`], [], []])
    expect(useNewTabLayoutStore.getState().presets['1col'].columns).toEqual([[`sessions:${M}`]])
    expect(outcome).not.toEqual({ ok: true, hash: await hashSection(payload) }) // the unknown column: pushed back without it, once
    const identity = identityOfSync(useHostStore.getState().hosts)
    expect(outcome).toEqual({ ok: true, hash: await hashSection(buildSettingsSection(readSettingsSources(), masterWorkspaceIds(), identity)) })
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

  it('tabs and settings refuse to land while the host identity is in conflict (two local hosts, one daemon)', async () => {
    useHostStore.setState({ hosts: { [M]: host(M, { daemonId: DAEMON }), twin: host('twin', { daemonId: DAEMON }) }, hostOrder: [M, 'twin'] })
    seedTabWorld()
    const tabsBefore = useTabStore.getState().tabs
    expect(await applySectionToStores('tabs.wa', buildTabsSection(ws('wa', ['a1']), { a1: tab('a1') }), ctx)).toMatchObject({ ok: false, reason: 'invalid', code: 'host-identity-conflict' })
    expect(useTabStore.getState().tabs).toBe(tabsBefore)
    expect(await applySectionToStores('settings', { 'purdex-layout': { tabPosition: 'bottom' } }, ctx)).toMatchObject({ ok: false, reason: 'invalid', code: 'host-identity-conflict' })
  })
})
