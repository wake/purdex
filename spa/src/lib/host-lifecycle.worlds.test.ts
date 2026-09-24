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
import { __resetMasterWorldForTest } from './profile/master-world'
import { switchActiveProfile } from './profile/switch-active'
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
