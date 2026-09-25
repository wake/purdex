// spa/src/lib/host-reresolve.integration.test.ts — the re-resolve pass against the REAL collector builders and
// hashes (host ownership spec §3.3 no-push invariant, plan H1b T6, §0.11, §0.13): what the pass rewrites must not
// change what this device would push, section by section.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import type { HostConfig } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../stores/useLocalProfilesStore'
import { useHostSettingsStore } from '../stores/useHostSettingsStore'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { useHostLookStore } from '../stores/useHostLookStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useNewTabBootstrap } from '../hooks/useNewTabBootstrap'
import { clearNewTabRegistry, registerNewTabProviderSource } from './new-tab-registry'
import { createHostSessionProviderSource } from './session-new-tab-providers'
import { createHeadlessProviderSource } from './headless-new-tab-providers'
import { buildSectionPayload } from './profile/collector'
import { hashSection } from './profile/hash'
import { syncIdOfSync } from './profile/host-identity'
import { applySectionToStores } from './profile/apply-to-stores'
import { __resetMasterWorldForTest } from './profile/master-world'
import type { ProfileSectionKey, SettingsPayload } from './profile/types'
import type { PaneContent, PaneLayout, Tab, Workspace } from '../types/tab'
import { __resetHostReresolveForTest, runHostReresolve, startHostReresolve } from './host-reresolve'

const MLAB = 'mlab-daemon:111111'
const DAEMON = 'air-lab:26cccc'
const W = syncIdOfSync(DAEMON)
const M = 'hm0001' // this device's master host (mlab)
const WM = syncIdOfSync(MLAB) // M's wire id: its look lives under it (H2c-2)
const X = 'hx0001' // the host that arrives
const SLAVE = 'slave-1'
/** Every section the collector builds here — not `hosts`: it is retired (host ownership H3a-2). */
const KEYS: ProfileSectionKey[] = ['workspaces', 'settings', 'tabs.wa']

const host = (id: string, over: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, ...over })
const leaf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
const tmux = (hostId: string): PaneContent => ({ kind: 'tmux-session', hostId, sessionCode: 'c1', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i' })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 1, layout })
const ws = (id: string, tabs: string[]): Workspace => ({ id, name: id, tabs, activeTabId: tabs[0] ?? null })

/** The master world: workspace `wa` with a tmux pane, a daemon editor and an execution on `W`, and one on `M`. */
function masterWorld(): ParkedWorld {
  const tabs = {
    a1: tab('a1', { type: 'split', id: 's', direction: 'h', sizes: [50, 50], children: [leaf('p1', tmux(W)), leaf('p2', tmux(M))] }),
    a2: tab('a2', leaf('p3', { kind: 'editor', source: { type: 'daemon', hostId: W }, filePath: '/a' })),
    a3: tab('a3', leaf('p4', { kind: 'execution', executionId: 'e1', host: W })),
  }
  return { tabs, workspaces: [ws('wa', ['a1', 'a2', 'a3'])], activeWorkspaceId: 'wa', activeTabId: 'a1' }
}

function seedSettings(): void {
  useHostSettingsStore.setState({ hosts: { [W]: { editor: { homePath: '/x' } }, [M]: { editor: { homePath: '/m' } } } })
  useNewTabLayoutStore.setState({
    presets: {
      '3col': { enabled: true, columns: [[`sessions:${M}`], [`sessions:${W}`], [`headless:${W}`]] },
      '2col': { enabled: false, columns: [[`sessions:${W}`], []] },
      '1col': { enabled: true, columns: [[`sessions:${M}`, `sessions:${W}`, `headless:${W}`]] },
    },
    knownIds: [`sessions:${M}`, `sessions:${W}`, `headless:${W}`],
  })
}

function masterOnScreen(): void {
  const w = masterWorld()
  useTabStore.setState({ tabs: w.tabs, tabOrder: Object.keys(w.tabs), activeTabId: w.activeTabId, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
}

/** A slave on screen (its own pane on `W`); the master's world parked. */
function slaveOnScreen(): void {
  const st = tab('st1', leaf('sp', tmux(W)))
  useLocalProfilesStore.setState({ slaves: { [SLAVE]: { id: SLAVE, name: 'Slave', createdAt: 1, shownHostIds: [], world: null } }, slaveOrder: [SLAVE], activeProfileId: SLAVE, parkedMaster: masterWorld(), worldEpoch: 1 })
  useTabStore.setState({ tabs: { st1: st }, tabOrder: ['st1'], activeTabId: 'st1', visitHistory: [], worldId: SLAVE, worldEpoch: 1 })
  useWorkspaceStore.setState({ workspaces: [ws('SLAVE-ws', ['st1'])], activeWorkspaceId: 'SLAVE-ws', worldId: SLAVE, worldEpoch: 1 })
}

async function hashes(): Promise<Record<string, string>> {
  const out: Record<string, string> = {}
  for (const key of KEYS) {
    const built = buildSectionPayload(key)
    if (built === null || built.payload === null) throw new Error(`fixture: ${key} not built`)
    out[key] = await hashSection(built.payload)
  }
  return out
}

function hostIdsEverywhere(): string {
  return JSON.stringify([useTabStore.getState().tabs, useLocalProfilesStore.getState().parkedMaster, useLocalProfilesStore.getState().slaves, useHostSettingsStore.getState().hosts, useNewTabLayoutStore.getState()])
}

const addX = (over: Partial<HostConfig> = {}) =>
  useHostStore.setState((s) => ({ hosts: { ...s.hosts, [X]: host(X, { ip: '10.0.0.4', daemonId: DAEMON, order: 1, ...over }) }, hostOrder: [...s.hostOrder, X] }))

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  __resetHostReresolveForTest()
  clearNewTabRegistry()
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useHostStore.setState({ hosts: { [M]: host(M, { daemonId: MLAB }) }, hostOrder: [M], activeHostId: M, runtime: {} })
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
  useHostSettingsStore.setState({ hosts: {} })
  useHostLookStore.setState({ looks: {} })
  useShownHostsStore.setState({ ids: [] })
})

afterEach(() => {
  __resetHostReresolveForTest()
  clearNewTabRegistry()
  __resetMasterWorldForTest()
  localStorage.clear()
})

describe('(a) the pass leaves every section hash exactly as it was — nothing is pushed', () => {
  it.each([
    ['the master on screen', masterOnScreen],
    ['a slave on screen, the master parked', slaveOnScreen],
  ])('%s', async (_label, place) => {
    place()
    seedSettings()
    // Looks are keyed by wire id in the store itself; every host here has its daemonId and its look under its `d1_…`
    // (no local-id entry — H2c-2's re-key has nothing to move): the pass leaves them alone, and the settings section
    // that carries them hashes as before
    useHostLookStore.setState({ looks: { [W]: { name: 'air26' }, [WM]: { name: 'mlab' } } })
    addX() // the host arrives BEFORE the "before" snapshot: `hosts` moves because of the add, never the pass
    const looks = useHostLookStore.getState().looks
    const before = await hashes()
    expect((buildSectionPayload('settings')!.payload as SettingsPayload)['purdex-host-looks']).toEqual({ looks })
    expect(runHostReresolve()).toBe('done')
    expect(hostIdsEverywhere()).not.toContain(W) // every ref moved, on screen and parked
    expect(useHostLookStore.getState().looks).toBe(looks)
    expect(await hashes()).toEqual(before)
  })
})

describe("(a') both forms of one host (plan §0.11)", () => {
  // H1b acceptance, scenario 2: where a preset holds a host's block under its local id AND its wire id, the WIRE form
  // wins (it is what the SOT already has; the local one is the bootstrap's stopgap) — in the build and in the pass
  // alike, as the sync id wins for host settings. So the build is the SOT's bytes before, during and after the pass.
  it('host settings: the sync-id entry wins; New Tab: the wire-form column wins, wherever it sits — no hash moves', async () => {
    masterOnScreen()
    addX()
    useHostSettingsStore.setState({ hosts: { [X]: { editor: { homePath: '/local' } }, [W]: { editor: { homePath: '/wire' } } } })
    useNewTabLayoutStore.setState({
      presets: {
        '3col': { enabled: false, columns: [[`sessions:${W}`], [`headless:${X}`], [`sessions:${X}`, `headless:${W}`]] },
        '2col': { enabled: false, columns: [[], [`sessions:${X}`]] },
        '1col': { enabled: true, columns: [[`sessions:${X}`, 'browser', `sessions:${W}`]] },
      },
      knownIds: [`sessions:${X}`, `sessions:${W}`],
    })
    const settingsBefore = buildSectionPayload('settings')!.payload as SettingsPayload
    expect((settingsBefore['purdex-host-settings'] as { hosts: object }).hosts).toEqual({ [W]: { editor: { homePath: '/wire' } } })
    expect((settingsBefore['purdex-newtab-layout'] as { presets: object }).presets).toEqual({
      '3col': { enabled: false, columns: [[`sessions:${W}`], [], [`headless:${W}`]] },
      '2col': { enabled: false, columns: [[], [`sessions:${W}`]] },
      '1col': { enabled: true, columns: [['browser', `sessions:${W}`]] },
    })

    runHostReresolve()
    const settingsAfter = buildSectionPayload('settings')!.payload as SettingsPayload
    expect(settingsAfter).toEqual(settingsBefore)
    expect(await hashSection(settingsAfter)).toBe(await hashSection(settingsBefore))
    expect(useNewTabLayoutStore.getState().knownIds).toEqual([`sessions:${X}`])
  })
})

describe("(a''') duplicates that no mapping made (PR #1406 critic)", () => {
  // Only two DIFFERENT ids that come to name one block collide. A column repeated as it is — a wire id twice, an
  // unknown id twice, a local id twice — is left exactly as older builds sent it, by the build and by the pass.
  it('are neither merged by the build nor by the pass: the payload is what it was, before and after', async () => {
    masterOnScreen()
    addX()
    const U = syncIdOfSync('nowhere:999999')
    useNewTabLayoutStore.setState({
      presets: {
        '3col': { enabled: false, columns: [[`sessions:${X}`], [`sessions:${X}`], []] },
        '2col': { enabled: false, columns: [[`headless:${W}`], [`headless:${W}`]] },
        '1col': { enabled: true, columns: [[`sessions:${U}`, 'browser', `sessions:${U}`]] },
      },
      knownIds: [],
    })
    const before = buildSectionPayload('settings')!.payload as SettingsPayload
    expect((before['purdex-newtab-layout'] as { presets: object }).presets).toEqual({
      '3col': { enabled: false, columns: [[`sessions:${W}`], [`sessions:${W}`], []] },
      '2col': { enabled: false, columns: [[`headless:${W}`], [`headless:${W}`]] },
      '1col': { enabled: true, columns: [[`sessions:${U}`, 'browser', `sessions:${U}`]] },
    })
    runHostReresolve()
    expect(useNewTabLayoutStore.getState().presets['2col'].columns).toEqual([[`headless:${X}`], [`headless:${X}`]])
    expect(buildSectionPayload('settings')!.payload).toEqual(before)
  })
})

describe("(a'') H1b acceptance scenario 2: a host added without its daemonId first, the pass held off", () => {
  it.each([
    ['the received wire blocks at the top of the first column (the local ones land after them)', 'first'],
    ['the received wire blocks at the end of the last column (the shortest column — and the local ones — come before)', 'last'],
  ] as const)('%s: settings hashes as the SOT has it before, during and after the pass', async (_label, where) => {
    masterOnScreen()
    registerNewTabProviderSource(createHostSessionProviderSource())
    registerNewTabProviderSource(createHeadlessProviderSource())
    renderHook(() => useNewTabBootstrap()).unmount() // steady state for M

    const now = JSON.parse(JSON.stringify(buildSectionPayload('settings')!.payload)) as SettingsPayload
    const layout = now['purdex-newtab-layout'] as { presets: Record<string, { columns: string[][] }> }
    for (const preset of Object.values(layout.presets)) {
      if (where === 'first') preset.columns[0].unshift(`sessions:${W}`, `headless:${W}`)
      else preset.columns[preset.columns.length - 1].push(`sessions:${W}`, `headless:${W}`)
    }
    expect(await applySectionToStores('settings', now, { masterHostId: M })).toMatchObject({ ok: true })
    const original = await hashSection(buildSectionPayload('settings')!.payload)
    expect(original).toBe(await hashSection(now))

    // the add-host dialog writes X without a daemonId; the bootstrap places X's blocks under the local id
    const bootstrap = renderHook(() => useNewTabBootstrap())
    try {
      act(() => { addX({ daemonId: undefined }) })
      const flat3 = useNewTabLayoutStore.getState().presets['3col'].columns.flat()
      expect(flat3).toContain(`sessions:${X}`)
      if (where === 'last') expect(flat3.indexOf(`sessions:${X}`)).toBeLessThan(flat3.indexOf(`sessions:${W}`)) // the case that pushed

      const grant = useRebuildStore.getState().acquireOperationLock('someone-else')
      act(() => { useHostStore.setState((st) => ({ hosts: { ...st.hosts, [X]: { ...st.hosts[X], daemonId: DAEMON } } })) })
      expect(runHostReresolve()).toBe('busy')
      expect(await hashSection(buildSectionPayload('settings')!.payload)).toBe(original)

      useRebuildStore.getState().releaseOperationLock(grant)
      expect(runHostReresolve()).toBe('done')
      expect(JSON.stringify(useNewTabLayoutStore.getState().presets)).not.toContain(W)
      expect(await hashSection(buildSectionPayload('settings')!.payload)).toBe(original)
      for (const preset of Object.values(useNewTabLayoutStore.getState().presets)) {
        expect(preset.columns.flat().filter((id) => id === `sessions:${X}`)).toHaveLength(1)
      }
      const known = useNewTabLayoutStore.getState().knownIds
      expect(known.filter((id) => id === `sessions:${X}`)).toHaveLength(1)
      expect(known.filter((id) => id === `headless:${X}`)).toHaveLength(1)
      expect(JSON.stringify(known)).not.toContain(W)
    } finally {
      bootstrap.unmount()
    }
  })
})

// H2c-2 T5 (plan §0.13): the spec §4.3 exception to the no-push invariant. A look this device keyed by a host's LOCAL
// id (added before its daemonId was known) moves to the host's `d1_…` key once the daemonId is learned — the
// `settings` payload changes, once; every other section stays as it was. A host that arrives WITH its daemonId has
// no local-id entry, and (a) above still holds.
describe('(e) the look re-key: one push, settings only (H2c-2)', () => {
  it.each([
    ['the master on screen', masterOnScreen],
    ['a slave on screen, the master parked', slaveOnScreen],
  ])('%s: a local-id look + the daemonId learned → only settings differs, and a second pass changes nothing', async (_label, place) => {
    place()
    seedSettings()
    addX({ daemonId: undefined })
    useHostLookStore.setState({ looks: { [X]: { name: 'air26' }, [WM]: { name: 'mlab' } } })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [X]: { ...s.hosts[X], daemonId: DAEMON } } }))
    const before = await hashes()
    expect(runHostReresolve()).toBe('done')
    expect(useHostLookStore.getState().looks).toEqual({ [W]: { name: 'air26' }, [WM]: { name: 'mlab' } })
    const once = await hashes()
    for (const key of KEYS) {
      if (key === 'settings') expect(once[key], key).not.toBe(before[key])
      else expect(once[key], key).toBe(before[key])
    }
    expect((buildSectionPayload('settings')!.payload as SettingsPayload)['purdex-host-looks']).toEqual({ looks: { [W]: { name: 'air26' }, [WM]: { name: 'mlab' } } })
    expect(runHostReresolve()).toBe('done')
    expect(await hashes()).toEqual(once)
  })
})

// H2d-1 T4 (plan §0.13): the same exception for the shown-hosts store — a listed LOCAL id moves to the host's `d1_…`
// once its daemonId is learned: `settings` changes once, every other section stays. With both a look entry and a
// shown id on that local id, the pass still changes `settings` exactly once (one write each, one push).
describe('(f) the shown-hosts re-key: one push, settings only (H2d-1)', () => {
  it.each([
    ['a shown id only', false],
    ['a shown id and a look entry on the same local id', true],
  ])('%s + the daemonId learned → only settings differs, and a second pass changes nothing', async (_label, withLook) => {
    masterOnScreen()
    seedSettings()
    addX({ daemonId: undefined })
    useShownHostsStore.setState({ ids: [WM, X] })
    if (withLook) useHostLookStore.setState({ looks: { [X]: { name: 'air26' } } })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [X]: { ...s.hosts[X], daemonId: DAEMON } } }))
    const before = await hashes()
    expect(runHostReresolve()).toBe('done')
    expect(useShownHostsStore.getState().ids).toEqual([WM, W])
    if (withLook) expect(useHostLookStore.getState().looks).toEqual({ [W]: { name: 'air26' } })
    const once = await hashes()
    for (const key of KEYS) {
      if (key === 'settings') expect(once[key], key).not.toBe(before[key])
      else expect(once[key], key).toBe(before[key])
    }
    expect((buildSectionPayload('settings')!.payload as SettingsPayload)['purdex-shown-hosts']).toEqual({ ids: [WM, W] })
    expect(runHostReresolve()).toBe('done')
    expect(await hashes()).toEqual(once)
  })

  it('a host arriving WITH its daemonId, listed by its d1_ id: the pass leaves every section hash as it was', async () => {
    masterOnScreen()
    seedSettings()
    useShownHostsStore.setState({ ids: [W] })
    addX()
    const before = await hashes()
    expect(runHostReresolve()).toBe('done')
    expect(useShownHostsStore.getState().ids).toEqual([W])
    expect(await hashes()).toEqual(before)
  })
})

describe('(b) an alias-only reference', () => {
  it('is canonicalised: the settings hash changes ONCE (one push, as a pull does); a second pass moves nothing', async () => {
    masterOnScreen()
    addX({ syncAliases: ['legacy1'] })
    useHostSettingsStore.setState({ hosts: { legacy1: { editor: { homePath: '/l' } } } })
    const before = await hashes()
    runHostReresolve()
    const once = await hashes()
    expect(once.settings).not.toBe(before.settings)
    expect(useHostSettingsStore.getState().hosts).toEqual({ [X]: { editor: { homePath: '/l' } } })
    expect((buildSectionPayload('settings')!.payload as SettingsPayload)['purdex-host-settings']).toEqual({ hosts: { [W]: { editor: { homePath: '/l' } } } })
    runHostReresolve()
    expect(await hashes()).toEqual(once)
  })
})

describe('(c) receive an unknown column → restart → add the daemon', () => {
  it('ends with one live column per block kind, no duplicate, and nothing to push', async () => {
    masterOnScreen()
    registerNewTabProviderSource(createHostSessionProviderSource())
    registerNewTabProviderSource(createHeadlessProviderSource())
    renderHook(() => useNewTabBootstrap()).unmount() // the steady state for the hosts this device has

    // receive: a settings payload naming X's columns while this device lacks X
    const now = JSON.parse(JSON.stringify(buildSectionPayload('settings')!.payload)) as SettingsPayload
    const layout = now['purdex-newtab-layout'] as { presets: Record<string, { columns: string[][] }> }
    layout.presets['1col'].columns[0].push(`sessions:${W}`, `headless:${W}`)
    const received: SettingsPayload = { ...now, 'purdex-host-settings': { hosts: { ...(now['purdex-host-settings'] as { hosts: object }).hosts, [W]: { editor: { homePath: '/x' } } } } }
    expect(await applySectionToStores('settings', received, { masterHostId: M })).toMatchObject({ ok: true, hash: await hashSection(received) })

    // restart: every store comes back from storage
    for (const store of [useHostStore, useTabStore, useWorkspaceStore, useLocalProfilesStore, useHostSettingsStore, useNewTabLayoutStore]) await store.persist.rehydrate()
    const bootstrap = renderHook(() => useNewTabBootstrap())
    const stop = startHostReresolve()
    try {
      expect(JSON.stringify(useNewTabLayoutStore.getState().presets)).toContain(`sessions:${W}`)
      act(() => { addX() })
      const cols = useNewTabLayoutStore.getState().presets['1col'].columns[0]
      expect(cols.filter((id) => id === `sessions:${X}`)).toHaveLength(1)
      expect(cols.filter((id) => id === `headless:${X}`)).toHaveLength(1)
      expect(JSON.stringify(useNewTabLayoutStore.getState())).not.toContain(W)
      expect(useHostSettingsStore.getState().hosts[X]).toEqual({ editor: { homePath: '/x' } })
      expect(await hashSection(buildSectionPayload('settings')!.payload)).toBe(await hashSection(received))
    } finally {
      stop()
      bootstrap.unmount()
    }
  })
})

// H1b real-device acceptance (the failure it found): knownIds is device-local and never synced, so a device that
// RECEIVED `sessions:<wire>` / `headless:<wire>` does not know them. The pass renames them to the local id; the new
// host's providers become ready only AFTER that, and the bootstrap then saw neither the id in knownIds nor the wire
// form in a preset — and placed each block a second time (pushed: the SOT's column count doubled).
describe("(c') the providers become ready only AFTER the pass renamed the received columns", () => {
  it.each([
    ['received through a settings apply (knownIds untouched)', false],
    ['on a device whose knownIds is empty (pulled fresh from the SOT)', true],
  ])('%s: exactly one block per kind in every preset, and nothing to push', async (_label, emptyKnown) => {
    masterOnScreen()
    // the steady state for the hosts this device has; then the registry is empty again until X's providers are ready
    registerNewTabProviderSource(createHostSessionProviderSource())
    registerNewTabProviderSource(createHeadlessProviderSource())
    renderHook(() => useNewTabBootstrap()).unmount()
    clearNewTabRegistry()
    const now = JSON.parse(JSON.stringify(buildSectionPayload('settings')!.payload)) as SettingsPayload
    const layout = now['purdex-newtab-layout'] as { presets: Record<string, { columns: string[][] }> }
    for (const preset of Object.values(layout.presets)) preset.columns[0].push(`sessions:${W}`, `headless:${W}`)
    expect(await applySectionToStores('settings', now, { masterHostId: M })).toMatchObject({ ok: true, hash: await hashSection(now) })
    if (emptyKnown) useNewTabLayoutStore.setState({ knownIds: [] })
    expect(useNewTabLayoutStore.getState().knownIds).not.toContain(`sessions:${W}`)

    // the host arrives; the pass renames the received columns before any provider for it exists
    addX()
    expect(runHostReresolve()).toBe('done')
    expect(JSON.stringify(useNewTabLayoutStore.getState().presets)).not.toContain(W)
    const before = await hashes()

    // only now do the host providers become ready, and the bootstrap runs
    registerNewTabProviderSource(createHostSessionProviderSource())
    registerNewTabProviderSource(createHeadlessProviderSource())
    renderHook(() => useNewTabBootstrap()).unmount()

    for (const preset of Object.values(useNewTabLayoutStore.getState().presets)) {
      const ids = preset.columns.flat()
      expect(ids.filter((id) => id === `sessions:${X}`)).toHaveLength(1)
      expect(ids.filter((id) => id === `headless:${X}`)).toHaveLength(1)
    }
    expect(useNewTabLayoutStore.getState().knownIds).toEqual(expect.arrayContaining([`sessions:${X}`, `headless:${X}`]))
    expect(await hashes()).toEqual(before)
  })
})

describe('(d) an identity conflict', () => {
  it('nothing moves while it lasts; when one duplicate is removed, the pass runs (the signature changed)', () => {
    masterOnScreen()
    seedSettings()
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, [X]: host(X, { daemonId: DAEMON, order: 1 }), dup: host('dup', { daemonId: DAEMON, order: 2 }) },
      hostOrder: [...s.hostOrder, X, 'dup'],
    }))
    const stop = startHostReresolve()
    try {
      const before = hostIdsEverywhere()
      expect(before).toContain(W)
      expect(runHostReresolve()).toBe('conflict')
      expect(hostIdsEverywhere()).toBe(before)
      useHostStore.setState((s) => {
        const { dup: _dup, ...hosts } = s.hosts
        return { hosts, hostOrder: s.hostOrder.filter((id) => id !== 'dup') }
      })
      expect(hostIdsEverywhere()).not.toContain(W)
    } finally {
      stop()
    }
  })
})
