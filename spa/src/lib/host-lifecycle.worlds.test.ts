// spa/src/lib/host-lifecycle.worlds.test.ts — the undo of a host delete puts every
// entry back into THE WORLD IT CAME FROM (host-lifecycle.ts, THE UNDO KNOWS WHOSE
// WORLD EACH ENTRY IS FROM). The switch in here is the real one, and so is the
// collector: "a slave's tab reached the SOT" is a string search for a sentinel.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkspaceStore } from '../features/workspace/store'
import { useHostStore } from '../stores/useHostStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../stores/useLocalProfilesStore'
import { useProfileStore } from '../stores/useProfileStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { useTabStore } from '../stores/useTabStore'
import type { Tab, TerminatedReason } from '../types/tab'
import { deleteHostCascade, deleteHostWithUndoToast } from './host-lifecycle'
import { useUndoToast } from '../stores/useUndoToast'
import { getPrimaryPane } from './pane-tree'
import { startCollector, type Collector, type SectionReport } from './profile/collector'
import { __resetMasterWorldForTest, readMasterWorld } from './profile/master-world'
import { deleteSlave, promoteToMaster, switchActiveProfile } from './profile/switch-active'

vi.mock('./nex/nex-api', () => ({ releaseLease: vi.fn(async () => undefined) }))
vi.mock('./profile/hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./profile/hash')>()
  return { ...actual, hashSection: vi.fn(async (payload: unknown) => actual.structuralKey(payload)) }
})

const HOST_A = 'host-a'
const HOST_B = 'host-b'
const MASTER = 'SENTINEL-MASTER'
const SLAVE = 'SENTINEL-SLAVE'
const S = 's1'
/** The same workspace id in both worlds — a demoted master and the master pulled after it share theirs. */
const SHARED_WS = 'ws-shared'

function tab(id: string, hostId: string, sentinel: string, terminated?: TerminatedReason): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 1,
    layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId, sessionCode: `c-${id}`, mode: 'terminal', cachedName: `${sentinel}-${id}`, tmuxInstance: 'i', ...(terminated ? { terminated } : {}) } } },
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
const terminatedIn = (tabs: Record<string, Tab>, tabId: string): unknown => {
  const content = getPrimaryPane(tabs[tabId].layout).content
  return content.kind === 'tmux-session' ? content.terminated : 'not-a-session'
}

let reports: SectionReport[] = []
let collector: Collector | null = null

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  reports = []
  useHostStore.setState({
    hosts: { [HOST_A]: { id: HOST_A, name: 'A', ip: '1.2.3.4', port: 7860, order: 0 }, [HOST_B]: { id: HOST_B, name: 'B', ip: '5.6.7.8', port: 7860, order: 1 } },
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
  __resetMasterWorldForTest()
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  putOnScreen({ workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null }, MASTER_PROFILE_ID, 0)
})

describe('A1 — tabs closed in a slave, undone after the master came on screen', () => {
  it.each([
    ['the two worlds share a workspace id', SHARED_WS],
    ['the two worlds share nothing', 'ws-slave'],
  ])('%s: the tabs go back into the SLAVE, now parked — the master on screen gets nothing, and the collector reports not one byte of the slave', async (_name, slaveWs) => {
    vi.useFakeTimers()
    const closed = tab('st-a', HOST_A, SLAVE)
    slaveOnScreen(world(SLAVE, [closed, tab('st-b', HOST_B, SLAVE)], slaveWs), world(MASTER, [tab('mt-b', HOST_B, MASTER)]))
    collector = startCollector({ onSection: (r) => reports.push(r), onProblem: () => {}, now: () => 0 })
    await collector.primeAll()
    reports = []

    const undo = deleteHostCascade(HOST_A, true)
    expect(useTabStore.getState().tabs[closed.id]).toBeUndefined()
    expect(await switchActiveProfile(MASTER_PROFILE_ID)).toEqual({ ok: true })
    const masterBefore = screen()

    undo()

    expect(screen()).toBe(masterBefore)
    expect(screen()).not.toContain(SLAVE)
    expect(slaveWorld().tabs[closed.id]).toEqual(closed)
    expect(slaveWorld().workspaces[0].tabs).toEqual(['st-b', 'st-a'])
    expect(readMasterWorld()).toMatchObject({ settled: true, onScreen: true })

    await vi.advanceTimersByTimeAsync(5_000)
    expect(JSON.stringify(reports)).not.toContain(SLAVE)
    // …and it comes back with the slave
    expect(await switchActiveProfile(S)).toEqual({ ok: true })
    expect(useTabStore.getState().tabs[closed.id]).toEqual(closed)
    expect(useWorkspaceStore.getState().findWorkspaceByTab(closed.id)?.id).toBe(slaveWs)
  })

  it('the other way round — closed in the master, undone with the slave on screen: back into the parked master', async () => {
    const closed = tab('mt-a', HOST_A, MASTER)
    masterOnScreen(world(MASTER, [closed, tab('mt-b', HOST_B, MASTER)]), world(SLAVE, [tab('st-b', HOST_B, SLAVE)]))
    const undo = deleteHostCascade(HOST_A, true)
    expect(await switchActiveProfile(S)).toEqual({ ok: true })
    const slaveBefore = screen()

    undo()

    expect(screen()).toBe(slaveBefore)
    const parked = useLocalProfilesStore.getState().parkedMaster
    expect(parked?.tabs[closed.id]).toEqual(closed)
    expect(parked?.workspaces[0].tabs).toEqual(['mt-b', 'mt-a'])
  })

  it('no switch in between: back on screen, as ever', () => {
    const closed = tab('st-a', HOST_A, SLAVE)
    slaveOnScreen(world(SLAVE, [closed, tab('st-b', HOST_B, SLAVE)]), world(MASTER, [tab('mt-b', HOST_B, MASTER)]))
    const parkedBefore = useLocalProfilesStore.getState().parkedMaster
    deleteHostCascade(HOST_A, true)()
    expect(useTabStore.getState().tabs[closed.id]).toEqual(closed)
    expect(useWorkspaceStore.getState().workspaces[0].tabs).toEqual(['st-b', 'st-a'])
    expect(JSON.stringify(useLocalProfilesStore.getState().parkedMaster)).toBe(JSON.stringify(parkedBefore))
  })

  it('a tab the parked world holds again by undo time is not overwritten; a workspace that is gone takes no member', async () => {
    const closed = tab('st-a', HOST_A, SLAVE)
    slaveOnScreen(world(SLAVE, [closed, tab('st-b', HOST_B, SLAVE)]), world(MASTER, [tab('mt-b', HOST_B, MASTER)]))
    const undo = deleteHostCascade(HOST_A, true)
    expect(await switchActiveProfile(MASTER_PROFILE_ID)).toEqual({ ok: true })
    const recreated = tab('st-a', HOST_B, 'RECREATED')
    const w = slaveWorld()
    useLocalProfilesStore.getState().replaceParkedWorld(S, { ...w, tabs: { ...w.tabs, [recreated.id]: recreated }, workspaces: [{ ...w.workspaces[0], id: 'ws-renamed' }] })
    undo()
    expect(slaveWorld().tabs['st-a']).toEqual(recreated)
    expect(slaveWorld().workspaces[0].tabs).toEqual(['st-b'])
  })

  it('the slave was DELETED inside the undo window: its tabs are skipped — nothing lands on screen, the rest of the undo happens', async () => {
    const closed = tab('st-a', HOST_A, SLAVE)
    slaveOnScreen(world(SLAVE, [closed, tab('st-b', HOST_B, SLAVE)]), world(MASTER, [tab('mt-a', HOST_A, MASTER), tab('mt-b', HOST_B, MASTER)]))
    const undo = deleteHostCascade(HOST_A, true)
    expect(await switchActiveProfile(MASTER_PROFILE_ID)).toEqual({ ok: true })
    expect(deleteSlave(S)).toEqual({ ok: true })
    const tabsBefore = Object.keys(useTabStore.getState().tabs)

    expect(() => undo()).not.toThrow()

    expect(Object.keys(useTabStore.getState().tabs)).toEqual(tabsBefore)
    expect(screen()).not.toContain(SLAVE)
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
    expect(terminatedIn(useTabStore.getState().tabs, 'mt-a')).toBeUndefined() // the master's own mark IS taken back
  })

  it('half-way through another window\'s switch (the tags disagree) nobody can say whose tabs the screen holds: skipped', () => {
    const closed = tab('st-a', HOST_A, SLAVE)
    slaveOnScreen(world(SLAVE, [closed, tab('st-b', HOST_B, SLAVE)]), world(MASTER, [tab('mt-b', HOST_B, MASTER)]))
    const undo = deleteHostCascade(HOST_A, true)
    // the other window's `tabs` has arrived here, its pointer and `workspaces` not yet
    useTabStore.setState({ tabs: world(MASTER, [tab('mt-b', HOST_B, MASTER)]).tabs, tabOrder: ['mt-b'], worldId: MASTER_PROFILE_ID, worldEpoch: 2 })
    undo()
    expect(JSON.stringify(useTabStore.getState().tabs)).not.toContain(SLAVE)
  })
})

describe('R2 — two worlds hold the same tab and pane ids; a mark is cleared in its owner\'s world only', () => {
  it('marked in the PARKED world by this delete, marked on screen by an earlier one: undo clears the parked one and leaves the screen\'s', () => {
    masterOnScreen(world(MASTER, [tab('t1', HOST_A, MASTER, 'host-removed'), tab('t2', HOST_B, MASTER)]), world(SLAVE, [tab('t1', HOST_A, SLAVE), tab('t2', HOST_B, SLAVE)]))
    const undo = deleteHostCascade(HOST_A, false)
    expect(terminatedIn(slaveWorld().tabs, 't1')).toBe('host-removed')
    undo()
    expect(terminatedIn(slaveWorld().tabs, 't1')).toBeUndefined()
    expect(terminatedIn(useTabStore.getState().tabs, 't1')).toBe('host-removed')
  })

  it('marked ON SCREEN by this delete, marked in the parked world by an earlier one: undo clears the screen\'s and leaves the parked one', () => {
    masterOnScreen(world(MASTER, [tab('t1', HOST_A, MASTER), tab('t2', HOST_B, MASTER)]), world(SLAVE, [tab('t1', HOST_A, SLAVE, 'host-removed'), tab('t2', HOST_B, SLAVE)]))
    const undo = deleteHostCascade(HOST_A, false)
    expect(terminatedIn(useTabStore.getState().tabs, 't1')).toBe('host-removed')
    undo()
    expect(terminatedIn(useTabStore.getState().tabs, 't1')).toBeUndefined()
    expect(terminatedIn(slaveWorld().tabs, 't1')).toBe('host-removed')
  })

  it('…and the same after a switch inside the undo window: each mark follows its world', async () => {
    masterOnScreen(world(MASTER, [tab('t1', HOST_A, MASTER, 'host-removed'), tab('t2', HOST_B, MASTER)]), world(SLAVE, [tab('t1', HOST_A, SLAVE), tab('t2', HOST_B, SLAVE)]))
    const undo = deleteHostCascade(HOST_A, false)
    expect(await switchActiveProfile(S)).toEqual({ ok: true })
    undo()
    expect(terminatedIn(useTabStore.getState().tabs, 't1')).toBeUndefined() // the slave, on screen now
    expect(terminatedIn(useLocalProfilesStore.getState().parkedMaster?.tabs ?? {}, 't1')).toBe('host-removed')
  })

  it('a mark whose owner is gone is skipped', async () => {
    masterOnScreen(world(MASTER, [tab('t1', HOST_A, MASTER, 'host-removed'), tab('t2', HOST_B, MASTER)]), world(SLAVE, [tab('t1', HOST_A, SLAVE), tab('t2', HOST_B, SLAVE)]))
    const undo = deleteHostCascade(HOST_A, false)
    expect(deleteSlave(S)).toEqual({ ok: true })
    expect(() => undo()).not.toThrow()
    expect(terminatedIn(useTabStore.getState().tabs, 't1')).toBe('host-removed')
  })
})

describe('C2 — a PROMOTE inside the undo window relabels the worlds: `master` names another world now', () => {
  /** Every world on this device, as bytes. */
  const worlds = (): string => JSON.stringify([useTabStore.getState().tabs, useTabStore.getState().tabOrder, useWorkspaceStore.getState().workspaces, useLocalProfilesStore.getState().parkedMaster, useLocalProfilesStore.getState().slaves])

  it.each([true, false])('master on screen, delete (closeTabs=%s), the slave is promoted — the old master is a slave under a NEW id now: the undo restores the host and touches no world', async (closeTabs) => {
    masterOnScreen(world(MASTER, [tab('mt-a', HOST_A, MASTER), tab('mt-b', HOST_B, MASTER)]), world(SLAVE, [tab('st-a', HOST_A, SLAVE), tab('st-b', HOST_B, SLAVE)]))
    const undo = deleteHostCascade(HOST_A, closeTabs)
    expect(await promoteToMaster(S, 'Old master')).toMatchObject({ ok: true })
    const before = worlds()

    const result = undo()

    expect(result).toEqual({ worldSkipped: true })
    expect(worlds()).toBe(before)
    expect(JSON.stringify(useLocalProfilesStore.getState().parkedMaster)).not.toContain(MASTER) // the new master holds nothing of the old one's
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
    expect(useHostStore.getState().hostOrder).toEqual([HOST_B, HOST_A])
  })

  it.each([true, false])('the slave on screen, delete (closeTabs=%s), THAT slave is promoted — the screen is the master now: same', async (closeTabs) => {
    slaveOnScreen(world(SLAVE, [tab('st-a', HOST_A, SLAVE), tab('st-b', HOST_B, SLAVE)]), world(MASTER, [tab('mt-a', HOST_A, MASTER), tab('mt-b', HOST_B, MASTER)]))
    const undo = deleteHostCascade(HOST_A, closeTabs)
    expect(await promoteToMaster(S, 'Old master')).toMatchObject({ ok: true })
    const before = worlds()

    expect(undo()).toEqual({ worldSkipped: true })

    expect(worlds()).toBe(before)
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
  })

  it('the two worlds share their tab and pane ids (a demoted master and the master pulled after it): not one mark is taken back in either', async () => {
    masterOnScreen(world(MASTER, [tab('t1', HOST_A, MASTER), tab('t2', HOST_B, MASTER)]), world(SLAVE, [tab('t1', HOST_A, SLAVE), tab('t2', HOST_B, SLAVE)]))
    const undo = deleteHostCascade(HOST_A, false)
    expect(await promoteToMaster(S, 'Old master')).toMatchObject({ ok: true })
    const before = worlds()
    expect(undo()).toEqual({ worldSkipped: true })
    expect(worlds()).toBe(before)
    expect(terminatedIn(useTabStore.getState().tabs, 't1')).toBe('host-removed')
    expect(terminatedIn(useLocalProfilesStore.getState().parkedMaster?.tabs ?? {}, 't1')).toBe('host-removed')
  })

  it('no promote: nothing is skipped, and the undo says so', () => {
    masterOnScreen(world(MASTER, [tab('mt-a', HOST_A, MASTER), tab('mt-b', HOST_B, MASTER)]), world(SLAVE, [tab('st-b', HOST_B, SLAVE)]))
    expect(deleteHostCascade(HOST_A, true)()).toEqual({ worldSkipped: false })
    expect(useTabStore.getState().tabs['mt-a']).toBeDefined()
  })

  it('a switch is no relabelling: nothing is skipped', async () => {
    masterOnScreen(world(MASTER, [tab('mt-a', HOST_A, MASTER), tab('mt-b', HOST_B, MASTER)]), world(SLAVE, [tab('st-b', HOST_B, SLAVE)]))
    const undo = deleteHostCascade(HOST_A, true)
    expect(await switchActiveProfile(S)).toEqual({ ok: true })
    expect(undo()).toEqual({ worldSkipped: false })
    expect(useLocalProfilesStore.getState().parkedMaster?.tabs['mt-a']).toBeDefined()
  })

  it('the veto (last host) has nothing to undo and skips nothing', () => {
    useHostStore.setState({ hosts: { [HOST_A]: useHostStore.getState().hosts[HOST_A] }, hostOrder: [HOST_A] })
    expect(deleteHostCascade(HOST_A, true)()).toEqual({ worldSkipped: false })
  })

  describe('the user is told (`deleteHostWithUndoToast`)', () => {
    const MESSAGES = { deleted: 'A deleted', worldSkipped: 'A is back, its tabs are not' }
    /** What GlobalUndoToast does on a click: run the action, then dismiss. */
    async function clickUndo(): Promise<void> {
      useUndoToast.getState().toast?.action?.()
      useUndoToast.getState().dismiss()
      await Promise.resolve()
    }

    beforeEach(() => useUndoToast.setState({ toast: null }))

    it('a skipped world: a second toast says so — after the first has dismissed itself, and with nothing to press', async () => {
      masterOnScreen(world(MASTER, [tab('mt-a', HOST_A, MASTER), tab('mt-b', HOST_B, MASTER)]), world(SLAVE, [tab('st-b', HOST_B, SLAVE)]))
      deleteHostWithUndoToast(HOST_A, true, MESSAGES)
      expect(useUndoToast.getState().toast).toMatchObject({ message: 'A deleted' })
      expect(useHostStore.getState().hosts[HOST_A]).toBeUndefined()
      expect(await promoteToMaster(S, 'Old master')).toMatchObject({ ok: true })

      await clickUndo()

      expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
      expect(useUndoToast.getState().toast).toEqual({ message: 'A is back, its tabs are not', action: undefined, actionLabel: undefined })
    })

    it('nothing skipped: no second toast', async () => {
      masterOnScreen(world(MASTER, [tab('mt-a', HOST_A, MASTER), tab('mt-b', HOST_B, MASTER)]), world(SLAVE, [tab('st-b', HOST_B, SLAVE)]))
      deleteHostWithUndoToast(HOST_A, true, MESSAGES)
      await clickUndo()
      expect(useTabStore.getState().tabs['mt-a']).toBeDefined()
      expect(useUndoToast.getState().toast).toBeNull()
    })
  })
})
