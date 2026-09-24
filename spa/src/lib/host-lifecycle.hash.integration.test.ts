// spa/src/lib/host-lifecycle.hash.integration.test.ts — a host deletion and its undo against the REAL collector
// builders and hashes (host ownership spec §3.4 no-push invariant, plan H1c T6, §0.11, §0.13): deleting a host
// affects this device only, so no section the collector builds may change but `hosts` — the pre-H3 exception, still
// synced until H3 retires it. Every section is compared, not just the host-bearing ones.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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
import { buildSectionPayload } from './profile/collector'
import { hashSection } from './profile/hash'
import { syncIdOfSync } from './profile/host-identity'
import { __resetMasterWorldForTest } from './profile/master-world'
import type { ProfileSectionKey, SettingsPayload } from './profile/types'
import type { PaneContent, PaneLayout, Tab, Workspace } from '../types/tab'
import { __resetHostReresolveForTest } from './host-reresolve'
import { deleteHostCascade } from './host-lifecycle'

vi.mock('./nex/nex-api', () => ({ releaseLease: vi.fn(async () => undefined), pinnedLeaseRelease: vi.fn(() => async () => undefined) }))

const MLAB = 'mlab-daemon:111111'
const DAEMON = 'air-lab:26cccc'
const W = syncIdOfSync(DAEMON)
const M = 'hm0001' // this device's master host (mlab)
const X = 'hx0001' // the host deleted
const SLAVE = 'slave-1'
/** EVERY section the collector builds for these fixtures (plan §0.13). */
const KEYS: ProfileSectionKey[] = ['hosts', 'workspaces', 'settings', 'tabs.wa', 'tabs.wb']
const NON_HOSTS = KEYS.filter((k) => k !== 'hosts')

const host = (id: string, over: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, token: 'tok', order: 0, ...over })
const leaf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
const tmux = (hostId: string): PaneContent => ({ kind: 'tmux-session', hostId, sessionCode: 'c1', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i' })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 1, layout })
const ws = (id: string, tabs: string[]): Workspace => ({ id, name: id, tabs, activeTabId: tabs[0] ?? null })

/** The master world: `wa` with a tmux pane, a daemon editor and an execution on `X`, and one on `M`; `wb` on `M` only. */
function masterWorld(): ParkedWorld {
  const tabs = {
    a1: tab('a1', { type: 'split', id: 's', direction: 'h', sizes: [50, 50], children: [leaf('p1', tmux(X)), leaf('p2', tmux(M))] }),
    a2: tab('a2', leaf('p3', { kind: 'editor', source: { type: 'daemon', hostId: X }, filePath: '/a' })),
    a3: tab('a3', leaf('p4', { kind: 'execution', executionId: 'e1', host: X })),
    a4: tab('a4', leaf('p5', { kind: 'execution', executionId: 'legacy', host: '' })),
    b1: tab('b1', leaf('p6', tmux(M))),
  }
  return { tabs, workspaces: [ws('wa', ['a1', 'a2', 'a3', 'a4']), ws('wb', ['b1'])], activeWorkspaceId: 'wa', activeTabId: 'a1' }
}

function masterOnScreen(): void {
  const w = masterWorld()
  useTabStore.setState({ tabs: w.tabs, tabOrder: Object.keys(w.tabs), activeTabId: w.activeTabId, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
}

/** A slave on screen (its own pane on `X`); the master's world parked. */
function slaveOnScreen(): void {
  const st = tab('st1', leaf('sp', tmux(X)))
  useLocalProfilesStore.setState({ slaves: { [SLAVE]: { id: SLAVE, name: 'Slave', createdAt: 1, world: null } }, slaveOrder: [SLAVE], activeProfileId: SLAVE, parkedMaster: masterWorld(), worldEpoch: 1 })
  useTabStore.setState({ tabs: { st1: st }, tabOrder: ['st1'], activeTabId: 'st1', visitHistory: [], worldId: SLAVE, worldEpoch: 1 })
  useWorkspaceStore.setState({ workspaces: [ws('SLAVE-ws', ['st1'])], activeWorkspaceId: 'SLAVE-ws', worldId: SLAVE, worldEpoch: 1 })
}

function seedSettings(): void {
  useHostSettingsStore.setState({ hosts: { [X]: { editor: { homePath: '/x' } }, [M]: { editor: { homePath: '/m' } } } })
  useNewTabLayoutStore.setState({
    presets: {
      '3col': { enabled: true, columns: [[`sessions:${M}`], [`sessions:${X}`], [`headless:${X}`]] },
      '2col': { enabled: false, columns: [[`sessions:${X}`], []] },
      '1col': { enabled: true, columns: [[`sessions:${M}`, `sessions:${X}`, `headless:${X}`]] },
    },
    knownIds: [`sessions:${M}`, `sessions:${X}`, `headless:${X}`],
  })
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

const pick = (h: Record<string, string>, keys: readonly string[]): Record<string, string> => Object.fromEntries(keys.map((k) => [k, h[k]]))

function hostIdsEverywhere(): string {
  return JSON.stringify([useTabStore.getState().tabs, useLocalProfilesStore.getState().parkedMaster, useLocalProfilesStore.getState().slaves, useHostSettingsStore.getState().hosts, useNewTabLayoutStore.getState()])
}

const withX = (over: Partial<HostConfig> = {}) =>
  useHostStore.setState({ hosts: { [M]: host(M, { daemonId: MLAB }), [X]: host(X, { ip: '10.0.0.4', daemonId: DAEMON, order: 1, ...over }) }, hostOrder: [M, X], activeHostId: X, runtime: {} })

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  __resetHostReresolveForTest()
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
  useHostSettingsStore.setState({ hosts: {} })
  // Both hosts have a daemonId, so their looks live under their wire ids — the shape the re-resolve pass keeps
  // (H2c-2: a local-id entry of a host with a known daemonId is moved to its `d1_…` key by the next pass, which the
  // undo's host write triggers; an `M`-keyed entry here would move and change `settings` for a reason unrelated to
  // the deletion).
  useHostLookStore.setState({ looks: { [W]: { name: 'air26' }, [syncIdOfSync(MLAB)]: { name: 'mlab' } } })
})

afterEach(() => {
  __resetHostReresolveForTest()
  __resetMasterWorldForTest()
  localStorage.clear()
})

describe('a deletion and its undo change no section but `hosts` (pre-H3)', () => {
  it.each([
    ['the master on screen', masterOnScreen],
    ['a slave on screen, the master parked', slaveOnScreen],
  ])('a host with a daemon (%s): every reference moves to the wire id and back; every non-hosts hash stays', async (_label, place) => {
    place()
    seedSettings()
    withX()
    const looks = useHostLookStore.getState().looks
    const before = await hashes()

    const undo = deleteHostCascade(X)

    expect(hostIdsEverywhere()).not.toContain(`"${X}"`) // every ref moved, on screen and parked …
    expect(hostIdsEverywhere()).not.toContain(`:${X}"`) // … the columns too
    expect(hostIdsEverywhere()).toContain(W)
    const afterDelete = await hashes()
    expect(pick(afterDelete, NON_HOSTS)).toEqual(pick(before, NON_HOSTS))
    expect(afterDelete.hosts).not.toBe(before.hosts) // the pre-H3 exception, asserted so H3 flips it
    expect(useHostLookStore.getState().looks).toBe(looks) // wire-keyed: never touched

    undo()

    expect(hostIdsEverywhere()).not.toContain(W)
    expect(await hashes()).toEqual(before) // `hosts` included: the row comes back verbatim, daemonId and all (#1396)
    expect(useHostLookStore.getState().looks).toBe(looks)
  })

  it('a host without a daemon: nothing is rewritten; non-hosts hashes stay; undo gives back `hosts` too', async () => {
    masterOnScreen()
    seedSettings()
    useHostStore.setState({ hosts: { [M]: host(M, { daemonId: MLAB }), [X]: host(X, { ip: '10.0.0.4', order: 1 }) }, hostOrder: [M, X], activeHostId: M, runtime: {} })
    const refs = hostIdsEverywhere()
    const before = await hashes()

    const undo = deleteHostCascade(X)

    expect(hostIdsEverywhere()).toBe(refs)
    const afterDelete = await hashes()
    expect(pick(afterDelete, NON_HOSTS)).toEqual(pick(before, NON_HOSTS))
    expect(afterDelete.hosts).not.toBe(before.hosts)

    undo()
    expect(await hashes()).toEqual(before)
  })
})

describe('both forms of one host (plan §0.11)', () => {
  // The store holds X's block under its local id AND its wire id (the add-host bootstrap's stopgap next to a received
  // one), and host settings under both keys. The build already sends one of each — the sync-id entry, the wire-form
  // column — so the deletion's rewrite, which keeps the same one, changes no byte of the payload: the store loses its
  // duplicate, nothing is pushed.
  it('host settings: the sync-id entry is kept; New Tab: the wire-form column is kept at its place — no hash moves, before, after, after undo', async () => {
    masterOnScreen()
    withX()
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
    const before = await hashes()

    const undo = deleteHostCascade(X)

    expect(useHostSettingsStore.getState().hosts).toEqual({ [W]: { editor: { homePath: '/wire' } } })
    expect(useNewTabLayoutStore.getState().presets['1col'].columns).toEqual([['browser', `sessions:${W}`]])
    expect(useNewTabLayoutStore.getState().presets['3col'].columns).toEqual([[`sessions:${W}`], [], [`headless:${W}`]])
    expect(useNewTabLayoutStore.getState().knownIds).toEqual([`sessions:${W}`])
    expect(buildSectionPayload('settings')!.payload).toEqual(settingsBefore)
    expect(pick(await hashes(), NON_HOSTS)).toEqual(pick(before, NON_HOSTS))

    undo()

    expect(useHostSettingsStore.getState().hosts).toEqual({ [X]: { editor: { homePath: '/wire' } } }) // what every other device had
    expect(useNewTabLayoutStore.getState().presets['1col'].columns).toEqual([['browser', `sessions:${X}`]]) // one column
    expect(await hashes()).toEqual(before)
  })
})
