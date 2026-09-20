// spa/src/lib/profile/master-world.test.ts — the world tag, the epoch barrier,
// and the one door to the master's tab world (P3 plan, P3b Task 4).
//
// Every world's tabs carry a SENTINEL in a pane's `cachedName`, so "which world
// did that come from" is a string search and never an inference.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkspaceStore } from '../../features/workspace/store'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../../stores/useLocalProfilesStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { STORAGE_KEYS } from '../storage'
import type { Tab, Workspace } from '../../types/tab'
import { replaceTabSnapshot } from '../snapshot/restore'
import { commitTabWorld as commitViaApply } from './apply-to-stores'
import {
  MASTER_WORLD_STUCK_MS,
  __resetMasterWorldForTest,
  commitTabWorld,
  masterWorkspaceIds,
  masterWorldStuck,
  readMasterWorld,
  repointActiveTab,
  subscribeMasterWorld,
  writeMasterWorld,
} from './master-world'

// === fixtures ===

const MASTER_SENTINEL = 'SENTINEL-MASTER'
const SLAVE_SENTINEL = 'SENTINEL-SLAVE'

function tab(id: string, sentinel: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 1,
    layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: `c-${id}`, mode: 'terminal', cachedName: `${sentinel}-${id}`, tmuxInstance: 'inst' } } },
  }
}

function ws(id: string, tabs: string[]): Workspace {
  return { id, name: id.toUpperCase(), tabs, activeTabId: tabs[0] ?? null }
}

function world(prefix: string, sentinel: string): ParkedWorld {
  const a = `${prefix}-t1`
  const b = `${prefix}-t2`
  return {
    workspaces: [ws(`${prefix}-ws`, [a, b])],
    tabs: { [a]: tab(a, sentinel), [b]: tab(b, sentinel) },
    activeWorkspaceId: `${prefix}-ws`,
    activeTabId: a,
  }
}

const masterWorld = (): ParkedWorld => world('m', MASTER_SENTINEL)
const slaveWorld = (): ParkedWorld => world('s', SLAVE_SENTINEL)

/** Master on screen, settled, epoch 0, no slaves. */
function putMasterOnScreen(w: ParkedWorld = masterWorld()): void {
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  useTabStore.setState({ tabs: w.tabs, tabOrder: Object.keys(w.tabs), activeTabId: w.activeTabId, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
}

/** Slave `s1` on screen, the master parked, all three at `epoch`. */
function putSlaveOnScreen(epoch = 1, master: ParkedWorld = masterWorld(), slave: ParkedWorld = slaveWorld()): void {
  useLocalProfilesStore.setState({
    slaves: { s1: { id: 's1', name: 'Slave', createdAt: 1, world: null } },
    slaveOrder: ['s1'],
    activeProfileId: 's1',
    parkedMaster: master,
    worldEpoch: epoch,
  })
  useTabStore.setState({ tabs: slave.tabs, tabOrder: Object.keys(slave.tabs), activeTabId: slave.activeTabId, visitHistory: [], worldId: 's1', worldEpoch: epoch })
  useWorkspaceStore.setState({ workspaces: slave.workspaces, activeWorkspaceId: slave.activeWorkspaceId, worldId: 's1', worldEpoch: epoch })
}

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  useWorkspaceSettingsStore.setState({ workspaces: {} })
  putMasterOnScreen()
})

afterEach(() => {
  vi.restoreAllMocks()
  __resetMasterWorldForTest()
  putMasterOnScreen({ workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null })
})

// === A. the tag and the epoch on the two live stores ===

describe('the world tag on useTabStore / useWorkspaceStore', () => {
  it('defaults to the master at epoch 0', () => {
    expect(useTabStore.getInitialState()).toMatchObject({ worldId: 'master', worldEpoch: 0 })
    expect(useWorkspaceStore.getInitialState()).toMatchObject({ worldId: 'master', worldEpoch: 0 })
  })

  it('is persisted with both stores', () => {
    putSlaveOnScreen(4)
    const persisted = (key: string): Record<string, unknown> => (JSON.parse(localStorage.getItem(key) ?? '{}') as { state: Record<string, unknown> }).state
    expect(persisted(STORAGE_KEYS.TABS)).toMatchObject({ worldId: 's1', worldEpoch: 4 })
    expect(persisted(STORAGE_KEYS.WORKSPACES)).toMatchObject({ worldId: 's1', worldEpoch: 4 })
  })

  it('reset() of the workspace store goes back to the master at epoch 0', () => {
    putSlaveOnScreen(4)
    useWorkspaceStore.getState().reset()
    expect(useWorkspaceStore.getState()).toMatchObject({ workspaces: [], activeWorkspaceId: null, worldId: 'master', worldEpoch: 0 })
  })

  it('a user whose persisted data predates the tag boots settled, master on screen, with everything they had', async () => {
    // What alpha.414 wrote: no `worldId`, no `worldEpoch`, the tab store at version 3, no local-profiles key at all.
    const m = masterWorld()
    localStorage.clear()
    localStorage.setItem(STORAGE_KEYS.TABS, JSON.stringify({ state: { tabs: m.tabs, tabOrder: Object.keys(m.tabs), activeTabId: m.activeTabId }, version: 3 }))
    localStorage.setItem(STORAGE_KEYS.WORKSPACES, JSON.stringify({ state: { workspaces: m.workspaces, activeWorkspaceId: m.activeWorkspaceId }, version: 1 }))
    vi.resetModules()
    const fresh = await import('./master-world')
    const tabs = (await import('../../stores/useTabStore')).useTabStore.getState()
    const wss = (await import('../../features/workspace/store')).useWorkspaceStore.getState()

    expect(tabs.tabs).toEqual(m.tabs)
    expect(tabs.tabOrder).toEqual(Object.keys(m.tabs))
    expect(wss.workspaces).toEqual(m.workspaces)
    expect(tabs).toMatchObject({ worldId: 'master', worldEpoch: 0 })
    expect(wss).toMatchObject({ worldId: 'master', worldEpoch: 0 })
    const read = fresh.readMasterWorld()
    expect(read).toMatchObject({ settled: true, onScreen: true })
    expect(JSON.stringify(read)).toContain(MASTER_SENTINEL)
  })
})

// === C. readMasterWorld ===

describe('readMasterWorld', () => {
  it('master on screen: the live stores', () => {
    const read = readMasterWorld()
    expect(read).toEqual({ settled: true, onScreen: true, world: masterWorld() })
    if (!read.settled) throw new Error('unreachable')
    expect(read.world.tabs).toBe(useTabStore.getState().tabs)
    expect(read.world.workspaces).toBe(useWorkspaceStore.getState().workspaces)
  })

  it('slave on screen: the parked master, and nothing of the screen', () => {
    putSlaveOnScreen()
    const read = readMasterWorld()
    expect(read).toEqual({ settled: true, onScreen: false, world: masterWorld() })
    expect(JSON.stringify(read)).not.toContain(SLAVE_SENTINEL)
  })

  it.each([
    ['the tab store is behind', () => useTabStore.setState({ worldEpoch: 0 })],
    ['the workspace store is behind', () => useWorkspaceStore.setState({ worldEpoch: 0 })],
    ['the local-profiles store is behind', () => useLocalProfilesStore.setState({ worldEpoch: 0 })],
    ['an epoch is not a number', () => useTabStore.setState({ worldEpoch: '1' as unknown as number })],
  ])('unsettled (epoch-mismatch) when %s', (_name, breakIt) => {
    putSlaveOnScreen(1)
    breakIt()
    expect(readMasterWorld()).toEqual({ settled: false, reason: 'epoch-mismatch' })
  })

  it.each([
    ['the two live stores carry different worlds', () => useTabStore.setState({ worldId: 'master' })],
    ['the live stores agree with each other but not with the pointer', () => useLocalProfilesStore.setState({ activeProfileId: MASTER_PROFILE_ID })],
    ['a tag is not a string', () => useWorkspaceStore.setState({ worldId: null as unknown as string })],
  ])('unsettled (world-mismatch) when %s', (_name, breakIt) => {
    putSlaveOnScreen(1)
    breakIt()
    expect(readMasterWorld()).toEqual({ settled: false, reason: 'world-mismatch' })
  })

  it('unsettled (no-parked-master) when a slave is on screen and nothing is parked for the master', () => {
    putSlaveOnScreen(1)
    useLocalProfilesStore.setState({ parkedMaster: null })
    expect(readMasterWorld()).toEqual({ settled: false, reason: 'no-parked-master' })
  })
})

describe('masterWorkspaceIds', () => {
  it('the master world\'s syncable ids — from the parked master while a slave is on screen', () => {
    expect(masterWorkspaceIds()).toEqual(new Set(['m-ws']))
    putSlaveOnScreen()
    expect(masterWorkspaceIds()).toEqual(new Set(['m-ws']))
  })

  it('null — never an empty set — while unsettled', () => {
    putSlaveOnScreen(1)
    useTabStore.setState({ worldEpoch: 0 })
    expect(masterWorkspaceIds()).toBeNull()
  })
})

// === B. commitTabWorld stamps ===

describe('commitTabWorld', () => {
  it('is the same function from apply-to-stores (the plan\'s address) and from here', () => {
    expect(commitViaApply).toBe(commitTabWorld)
  })

  it('without a stamp keeps the tag of both stores', () => {
    putSlaveOnScreen(3)
    const next = slaveWorld()
    commitTabWorld({ tabs: next.tabs, workspaces: next.workspaces, activeWorkspaceId: next.activeWorkspaceId })
    expect(useTabStore.getState()).toMatchObject({ worldId: 's1', worldEpoch: 3 })
    expect(useWorkspaceStore.getState()).toMatchObject({ worldId: 's1', worldEpoch: 3 })
  })

  it('with a stamp writes it into BOTH stores together with the world', () => {
    const s = slaveWorld()
    commitTabWorld({ ...s }, undefined, { worldId: 's1', worldEpoch: 7 })
    expect(useTabStore.getState()).toMatchObject({ tabs: s.tabs, worldId: 's1', worldEpoch: 7 })
    expect(useWorkspaceStore.getState()).toMatchObject({ workspaces: s.workspaces, worldId: 's1', worldEpoch: 7 })
  })

  it('a world that names its active tab gets it (a switch brings its own), ahead of the one on screen', () => {
    const s = slaveWorld()
    commitTabWorld({ ...s, activeTabId: 's-t2' }, undefined, { worldId: 's1', worldEpoch: 1 })
    expect(useTabStore.getState().activeTabId).toBe('s-t2')
  })

  it('a throw rolls the stamp back in both stores', () => {
    const s = slaveWorld()
    expect(() =>
      commitTabWorld({ ...s }, () => {
        throw new Error('boom')
      }, { worldId: 's1', worldEpoch: 7 }),
    ).toThrow('boom')
    expect(useTabStore.getState()).toMatchObject({ tabs: masterWorld().tabs, worldId: 'master', worldEpoch: 0 })
    expect(useWorkspaceStore.getState()).toMatchObject({ workspaces: masterWorld().workspaces, worldId: 'master', worldEpoch: 0 })
  })
})

describe('repointActiveTab', () => {
  const w = masterWorld()
  it('keeps the preferred tab while it survives', () => expect(repointActiveTab(w, 'm-t2')).toBe('m-t2'))
  it('else the active workspace\'s', () => expect(repointActiveTab(w, 'gone')).toBe('m-t1'))
  it('else null', () => expect(repointActiveTab({ ...w, activeWorkspaceId: null }, 'gone')).toBeNull())
})

describe('replaceTabSnapshot (lib/snapshot/restore.ts) does not change worlds', () => {
  it('leaves the tag alone: still settled afterwards', () => {
    putSlaveOnScreen(5)
    const s = slaveWorld()
    replaceTabSnapshot({ tabs: s.tabs, tabOrder: Object.keys(s.tabs), activeTabId: 's-t2', workspaces: s.workspaces, activeWorkspaceId: s.activeWorkspaceId } as Parameters<typeof replaceTabSnapshot>[0])
    expect(useTabStore.getState()).toMatchObject({ activeTabId: 's-t2', worldId: 's1', worldEpoch: 5 })
    expect(useWorkspaceStore.getState()).toMatchObject({ worldId: 's1', worldEpoch: 5 })
    expect(readMasterWorld().settled).toBe(true)
  })
})

// === C. writeMasterWorld ===

describe('writeMasterWorld', () => {
  it('master on screen: the live stores take it', () => {
    const m = masterWorld()
    const tabs = { 'm-t1': m.tabs['m-t1'] }
    expect(writeMasterWorld({ tabs, workspaces: [ws('m-ws', ['m-t1'])], activeWorkspaceId: 'm-ws' })).toBe('ok')
    expect(useTabStore.getState().tabs).toBe(tabs)
    expect(useWorkspaceStore.getState().workspaces).toEqual([ws('m-ws', ['m-t1'])])
    expect(useLocalProfilesStore.getState().parkedMaster).toBeNull()
  })

  it('slave on screen: the parked master takes it; the live stores and the epoch do not move', () => {
    putSlaveOnScreen(2)
    const liveTabs = useTabStore.getState()
    const liveWs = useWorkspaceStore.getState()
    const m = masterWorld()
    const tabs = { 'm-t2': m.tabs['m-t2'] }
    expect(writeMasterWorld({ tabs, workspaces: [ws('m-ws', ['m-t2'])], activeWorkspaceId: 'm-ws' })).toBe('ok')

    expect(useTabStore.getState()).toBe(liveTabs)
    expect(useWorkspaceStore.getState()).toBe(liveWs)
    // `activeTabId` pointed at m-t1, which is gone: re-pointed by the rule `commitTabWorld` uses.
    expect(useLocalProfilesStore.getState().parkedMaster).toEqual({ tabs, workspaces: [ws('m-ws', ['m-t2'])], activeWorkspaceId: 'm-ws', activeTabId: 'm-t2' })
    expect(useLocalProfilesStore.getState().worldEpoch).toBe(2)
    expect(readMasterWorld().settled).toBe(true)
  })

  it('slave on screen: `afterWrite` runs, and a throw puts the parked master and the scoped settings back', () => {
    putSlaveOnScreen(2)
    useWorkspaceSettingsStore.setState({ workspaces: { 'm-ws': { a: 1 } } as never })
    const scoped = useWorkspaceSettingsStore.getState().workspaces
    const parked = useLocalProfilesStore.getState().parkedMaster
    expect(() =>
      writeMasterWorld({ tabs: {}, workspaces: [], activeWorkspaceId: null }, () => {
        useWorkspaceSettingsStore.getState().clearWorkspace('m-ws')
        throw new Error('boom')
      }),
    ).toThrow('boom')
    expect(useLocalProfilesStore.getState().parkedMaster).toBe(parked)
    expect(useWorkspaceSettingsStore.getState().workspaces).toBe(scoped)
  })

  it('unsettled: nothing is written anywhere', () => {
    putSlaveOnScreen(2)
    useWorkspaceStore.setState({ worldEpoch: 1 })
    const before = [useTabStore.getState(), useWorkspaceStore.getState(), useLocalProfilesStore.getState()]
    const afterWrite = vi.fn()
    expect(writeMasterWorld({ tabs: {}, workspaces: [], activeWorkspaceId: null }, afterWrite)).toBe('unsettled')
    expect([useTabStore.getState(), useWorkspaceStore.getState(), useLocalProfilesStore.getState()]).toEqual(before)
    expect(afterWrite).not.toHaveBeenCalled()
  })
})

// === C. subscribeMasterWorld ===

describe('subscribeMasterWorld', () => {
  it('tells of a change to the master world on screen, and of nothing after the unsubscribe', () => {
    const fn = vi.fn()
    const off = subscribeMasterWorld(fn)
    useWorkspaceStore.getState().renameWorkspace('m-ws', 'Renamed')
    expect(fn).toHaveBeenCalledTimes(1)
    off()
    useWorkspaceStore.getState().renameWorkspace('m-ws', 'Again')
    expect(fn).toHaveBeenCalledTimes(1)
  })

  it('slave on screen: an edit of the screen is not a change of the master world; a write to the parked master is', () => {
    putSlaveOnScreen()
    const fn = vi.fn()
    const off = subscribeMasterWorld(fn)
    useWorkspaceStore.getState().renameWorkspace('s-ws', 'Renamed')
    useTabStore.getState().togglePin('s-t1')
    expect(fn).not.toHaveBeenCalled()
    useLocalProfilesStore.getState().replaceParkedWorld(MASTER_PROFILE_ID, { ...masterWorld(), workspaces: [ws('m-ws', ['m-t2', 'm-t1'])] })
    expect(fn).toHaveBeenCalledTimes(1)
    off()
  })

  it('tells when settled turns unsettled and back', () => {
    const fn = vi.fn()
    const off = subscribeMasterWorld(fn)
    useTabStore.setState({ worldEpoch: 1 })
    expect(fn).toHaveBeenCalledTimes(1)
    useTabStore.setState({ worldEpoch: 0 })
    expect(fn).toHaveBeenCalledTimes(2)
    off()
  })
})

// === F. a stuck unsettled ===

describe('masterWorldStuck', () => {
  it('false while settled', () => {
    expect(masterWorldStuck(1_000)).toBe(false)
    expect(masterWorldStuck(1_000_000)).toBe(false)
  })

  it('true once unsettled has been observed for MORE than five seconds; cleared by settling', () => {
    expect(MASTER_WORLD_STUCK_MS).toBe(5_000)
    useTabStore.setState({ worldEpoch: 9 })
    expect(masterWorldStuck(10_000)).toBe(false) // first observation
    expect(masterWorldStuck(15_000)).toBe(false) // exactly 5 s
    expect(masterWorldStuck(15_001)).toBe(true)
    useTabStore.setState({ worldEpoch: 0 })
    expect(masterWorldStuck(99_000)).toBe(false)
    // A new unsettled period starts its own clock.
    useTabStore.setState({ worldEpoch: 9 })
    expect(masterWorldStuck(100_000)).toBe(false)
    expect(masterWorldStuck(105_001)).toBe(true)
  })
})
