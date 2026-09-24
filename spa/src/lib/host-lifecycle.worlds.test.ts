// spa/src/lib/host-lifecycle.worlds.test.ts — a host delete (and its undo) across the
// tab worlds this device holds: the one on screen and every parked one (host ownership
// spec §3.4). The switch in here is the real one, and so is the collector: "nothing
// is pushed" is what the collector reports.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkspaceStore } from '../features/workspace/store'
import { useHostStore } from '../stores/useHostStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../stores/useLocalProfilesStore'
import { useProfileStore } from '../stores/useProfileStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { useTabStore } from '../stores/useTabStore'
import type { Tab } from '../types/tab'
import { deleteHostCascade } from './host-lifecycle'
import { getPrimaryPane } from './pane-tree'
import { startCollector, type Collector, type SectionReport } from './profile/collector'
import { __resetMasterWorldForTest, readMasterWorld } from './profile/master-world'
import { deleteSlave, promoteToMaster, switchActiveProfile } from './profile/switch-active'
import { syncIdOfSync } from './profile/host-identity'
import { __resetHostReresolveForTest } from './host-reresolve'

vi.mock('./nex/nex-api', () => ({ releaseLease: vi.fn(async () => undefined) }))
vi.mock('./profile/hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./profile/hash')>()
  return { ...actual, hashSection: vi.fn(async (payload: unknown) => actual.structuralKey(payload)) }
})

const HOST_A = 'host-a'
const HOST_B = 'host-b'
const DAEMON_A = 'lab-a:aaaaaa'
const WIRE_A = syncIdOfSync(DAEMON_A)
const MASTER = 'SENTINEL-MASTER'
const SLAVE = 'SENTINEL-SLAVE'
const S = 's1'
/** The same workspace id in both worlds — a demoted master and the master pulled after it share theirs. */
const SHARED_WS = 'ws-shared'

function tab(id: string, hostId: string, sentinel: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 1,
    layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId, sessionCode: `c-${id}`, mode: 'terminal', cachedName: `${sentinel}-${id}`, tmuxInstance: 'i' } } },
  }
}

function world(sentinel: string, tabs: Tab[], wsId = SHARED_WS): ParkedWorld {
  return { workspaces: [{ id: wsId, name: `${sentinel}-ws`, tabs: tabs.map((t) => t.id), activeTabId: tabs[0].id }], tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), activeWorkspaceId: wsId, activeTabId: tabs[0].id }
}

function putOnScreen(w: ParkedWorld, worldId: string, epoch: number): void {
  useTabStore.setState({ tabs: w.tabs, tabOrder: w.workspaces.flatMap((x) => x.tabs), activeTabId: w.activeTabId, visitHistory: [], worldId, worldEpoch: epoch })
  useWorkspaceStore.setState({ workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId, worldId, worldEpoch: epoch })
}

/** The slave on screen, the master parked. */
function slaveOnScreen(slave: ParkedWorld, master: ParkedWorld): void {
  useLocalProfilesStore.setState({ slaves: { [S]: { id: S, name: 'Slave', createdAt: 1, world: null } }, slaveOrder: [S], activeProfileId: S, parkedMaster: master, worldEpoch: 1 })
  putOnScreen(slave, S, 1)
}

/** The master on screen, the slave parked. */
function masterOnScreen(master: ParkedWorld, slave: ParkedWorld): void {
  useLocalProfilesStore.setState({ slaves: { [S]: { id: S, name: 'Slave', createdAt: 1, world: slave } }, slaveOrder: [S], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 1 })
  putOnScreen(master, MASTER_PROFILE_ID, 1)
}

const screen = (): string => JSON.stringify([useTabStore.getState().tabs, useTabStore.getState().tabOrder, useWorkspaceStore.getState().workspaces])
const slaveWorld = (): ParkedWorld => {
  const w = useLocalProfilesStore.getState().slaves[S]?.world
  if (!w) throw new Error('the slave is not parked')
  return w
}
const paneIn = (tabs: Record<string, Tab>, tabId: string) => getPrimaryPane(tabs[tabId].layout).content

let reports: SectionReport[] = []
let collector: Collector | null = null

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  __resetHostReresolveForTest()
  reports = []
  useHostStore.setState({
    hosts: { [HOST_A]: { id: HOST_A, name: 'A', ip: '1.2.3.4', port: 7860, order: 0, daemonId: DAEMON_A }, [HOST_B]: { id: HOST_B, name: 'B', ip: '5.6.7.8', port: 7860, order: 1 } },
    hostOrder: [HOST_B, HOST_A],
    activeHostId: HOST_B,
    runtime: {},
  })
  useProfileStore.setState({ masterHostId: null, masterProfileId: null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
})

afterEach(() => {
  collector?.stop()
  collector = null
  vi.useRealTimers()
  __resetHostReresolveForTest()
  __resetMasterWorldForTest()
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  putOnScreen({ workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null }, MASTER_PROFILE_ID, 0)
})

describe('the deletion reaches every world, and pushes nothing of any', () => {
  it('a slave on screen: its panes and the parked master\'s carry the wire id, nothing closes, and the collector reports `hosts` only', async () => {
    vi.useFakeTimers()
    slaveOnScreen(world(SLAVE, [tab('st-a', HOST_A, SLAVE), tab('st-b', HOST_B, SLAVE)], 'ws-slave'), world(MASTER, [tab('mt-a', HOST_A, MASTER), tab('mt-b', HOST_B, MASTER)]))
    collector = startCollector({ onSection: (r) => reports.push(r), onProblem: () => {}, now: () => 0 })
    await collector.primeAll()
    reports = []

    deleteHostCascade(HOST_A)

    expect(paneIn(useTabStore.getState().tabs, 'st-a')).toMatchObject({ hostId: WIRE_A })
    expect(paneIn(useLocalProfilesStore.getState().parkedMaster!.tabs, 'mt-a')).toMatchObject({ hostId: WIRE_A })
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(['st-a', 'st-b'])
    await vi.advanceTimersByTimeAsync(5_000)
    expect(reports.map((r) => r.key)).toEqual(['hosts']) // pre-H3: the host list still syncs; nothing else moved
    expect(JSON.stringify(reports)).not.toContain(SLAVE)
  })

  it('the master, switched back to, shows its pane on the deleted host un-marked, on the wire id', async () => {
    slaveOnScreen(world(SLAVE, [tab('st-b', HOST_B, SLAVE)]), world(MASTER, [tab('mt-a', HOST_A, MASTER), tab('mt-b', HOST_B, MASTER)]))
    deleteHostCascade(HOST_A)
    expect(await switchActiveProfile(MASTER_PROFILE_ID)).toEqual({ ok: true })
    const pane = paneIn(useTabStore.getState().tabs, 'mt-a')
    expect(pane).toMatchObject({ hostId: WIRE_A })
    expect(pane).not.toHaveProperty('terminated')
    expect(screen()).not.toContain(SLAVE)
    expect(slaveWorld().tabs['st-b']).toBeDefined()
  })

  it('the master on screen: the parked slave\'s pane on the deleted host carries the wire id too', () => {
    masterOnScreen(world(MASTER, [tab('mt-a', HOST_A, MASTER)]), world(SLAVE, [tab('st-a', HOST_A, SLAVE), tab('st-b', HOST_B, SLAVE)]))
    deleteHostCascade(HOST_A)
    expect(paneIn(useTabStore.getState().tabs, 'mt-a')).toMatchObject({ hostId: WIRE_A })
    expect(paneIn(slaveWorld().tabs, 'st-a')).toMatchObject({ hostId: WIRE_A })
    expect(paneIn(slaveWorld().tabs, 'st-b')).toMatchObject({ hostId: HOST_B })
  })
})

// Plan H1c T3 (§0.6): the undo runs the re-resolve pass's body for the restored host. `d1_X` names X on every
// device and in every world, so whoever's world a reference is in by undo time — after a switch, after a promote that
// relabelled the worlds, two worlds sharing tab and pane ids — it resolves to X. No per-reference record is kept.
describe('the undo brings every world back, whatever happened to the worlds meanwhile', () => {
  const hostOf = (tabs: Record<string, Tab>, tabId: string): unknown => (paneIn(tabs, tabId) as { hostId?: string }).hostId
  const everyHostId = (): string => JSON.stringify([useTabStore.getState().tabs, useLocalProfilesStore.getState().parkedMaster, useLocalProfilesStore.getState().slaves])

  it('a switch inside the undo window: the slave (parked now) and the master (on screen now) are both back on the local id', async () => {
    slaveOnScreen(world(SLAVE, [tab('st-a', HOST_A, SLAVE), tab('st-b', HOST_B, SLAVE)], 'ws-slave'), world(MASTER, [tab('mt-a', HOST_A, MASTER), tab('mt-b', HOST_B, MASTER)]))
    const undo = deleteHostCascade(HOST_A)
    expect(await switchActiveProfile(MASTER_PROFILE_ID)).toEqual({ ok: true })

    undo()

    expect(hostOf(useTabStore.getState().tabs, 'mt-a')).toBe(HOST_A)
    expect(hostOf(slaveWorld().tabs, 'st-a')).toBe(HOST_A)
    expect(everyHostId()).not.toContain(WIRE_A)
    expect(screen()).not.toContain(SLAVE)
  })

  it.each([
    ['the master on screen, the slave promoted', 'master'],
    ['the slave on screen, THAT slave promoted', 'slave'],
  ] as const)('a PROMOTE inside the undo window (%s — the worlds relabelled): every world back on the local id, no tab moved between worlds', async (_name, onScreen) => {
    const m = world(MASTER, [tab('mt-a', HOST_A, MASTER), tab('mt-b', HOST_B, MASTER)])
    const sl = world(SLAVE, [tab('st-a', HOST_A, SLAVE), tab('st-b', HOST_B, SLAVE)], 'ws-slave')
    if (onScreen === 'master') masterOnScreen(m, sl)
    else slaveOnScreen(sl, m)
    const undo = deleteHostCascade(HOST_A)
    expect(await promoteToMaster(S, 'Old master')).toMatchObject({ ok: true })
    const tabIdsBefore = JSON.stringify([Object.keys(useTabStore.getState().tabs), Object.keys(useLocalProfilesStore.getState().parkedMaster?.tabs ?? {}), Object.values(useLocalProfilesStore.getState().slaves).map((x) => Object.keys(x.world?.tabs ?? {}))])

    undo()

    expect(everyHostId()).not.toContain(WIRE_A)
    expect(everyHostId()).toContain(`"hostId":"${HOST_A}"`)
    expect(JSON.stringify([Object.keys(useTabStore.getState().tabs), Object.keys(useLocalProfilesStore.getState().parkedMaster?.tabs ?? {}), Object.values(useLocalProfilesStore.getState().slaves).map((x) => Object.keys(x.world?.tabs ?? {}))])).toBe(tabIdsBefore)
    expect(useHostStore.getState().hostOrder).toEqual([HOST_B, HOST_A])
  })

  it('the SAME tab and pane ids in two worlds, both on the wire id: both resolved — d1_X is X in every world', async () => {
    masterOnScreen(world(MASTER, [tab('t1', HOST_A, MASTER), tab('t2', HOST_B, MASTER)]), world(SLAVE, [tab('t1', HOST_A, SLAVE), tab('t2', HOST_B, SLAVE)]))
    const undo = deleteHostCascade(HOST_A)
    expect(await promoteToMaster(S, 'Old master')).toMatchObject({ ok: true })
    undo()
    expect(hostOf(useTabStore.getState().tabs, 't1')).toBe(HOST_A)
    const parked = useLocalProfilesStore.getState().parkedMaster ?? Object.values(useLocalProfilesStore.getState().slaves).find((x) => x.world)?.world
    expect(parked && hostOf(parked.tabs, 't1')).toBe(HOST_A)
    expect(everyHostId()).not.toContain(WIRE_A)
  })

  it('the slave was DELETED inside the undo window: the rest comes back, nothing lands on screen from it', async () => {
    slaveOnScreen(world(SLAVE, [tab('st-a', HOST_A, SLAVE)]), world(MASTER, [tab('mt-a', HOST_A, MASTER), tab('mt-b', HOST_B, MASTER)]))
    const undo = deleteHostCascade(HOST_A)
    expect(await switchActiveProfile(MASTER_PROFILE_ID)).toEqual({ ok: true })
    expect(deleteSlave(S)).toEqual({ ok: true })
    const tabsBefore = Object.keys(useTabStore.getState().tabs)

    expect(() => undo()).not.toThrow()

    expect(Object.keys(useTabStore.getState().tabs)).toEqual(tabsBefore)
    expect(hostOf(useTabStore.getState().tabs, 'mt-a')).toBe(HOST_A)
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
  })

  it('half-way through another window\'s switch (the tags disagree): the undo writes no world then — the scheduled pass does once it settles', async () => {
    vi.useFakeTimers()
    slaveOnScreen(world(SLAVE, [tab('st-a', HOST_A, SLAVE)]), world(MASTER, [tab('mt-b', HOST_B, MASTER)]))
    const undo = deleteHostCascade(HOST_A)
    const w = useTabStore.getState()
    useTabStore.setState({ worldId: MASTER_PROFILE_ID, worldEpoch: 2 }) // the other window's `tabs` tag arrived, the rest not yet
    expect(readMasterWorld().settled).toBe(false)
    const tabs = useTabStore.getState().tabs

    undo()

    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
    expect(useTabStore.getState().tabs).toBe(tabs)
    useTabStore.setState({ worldId: S, worldEpoch: w.worldEpoch }) // settled again
    await vi.advanceTimersByTimeAsync(500)
    expect(hostOf(useTabStore.getState().tabs, 'st-a')).toBe(HOST_A)
  })
})

