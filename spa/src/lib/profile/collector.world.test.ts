// spa/src/lib/profile/collector.world.test.ts — the collector and the master's tab
// world (P3 plan, P3b Task 4): a local profile ("slave") on screen never reaches a
// report, in any order in which another window's switch arrives here.
//
// Every world carries a SENTINEL — in each tab's pane and in each workspace's
// name — so "did a slave's content get out" is a string search over the reports.
// The assertions are stronger than that on purpose: between two rehydrates the
// live stores can hold a MIX (the slave's tabs under the master's workspaces),
// whose report would be wrong without containing a single slave string.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../../stores/useHostStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../../stores/useLocalProfilesStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { STORAGE_KEYS } from '../storage'
import type { Tab, Workspace } from '../../types/tab'
import { applySectionToStores } from './apply-to-stores'
import { startCollector, type Collector, type SectionReport } from './collector'
import { MASTER_WORLD_STUCK_MS, __resetMasterWorldForTest, commitTabWorld, readMasterWorld } from './master-world'
import type { WorkspacesPayload } from './types'

// As collector.test.ts: a `crypto.subtle` digest cannot be flushed by fake timers; the structural key is an
// equivalent identity — and apply-to-stores hashes through the same mock, so the two sides stay comparable.
vi.mock('./hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./hash')>()
  return { ...actual, hashSection: vi.fn(async (payload: unknown) => actual.structuralKey(payload)) }
})

const MASTER_SENTINEL = 'SENTINEL-MASTER'
const SLAVE_SENTINEL = 'SENTINEL-SLAVE'
const SLAVE = 's1'

function tab(id: string, sentinel: string): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 1,
    layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: `c-${id}`, mode: 'terminal', cachedName: `${sentinel}-${id}`, tmuxInstance: 'inst' } } },
  }
}

function world(prefix: string, sentinel: string): ParkedWorld {
  const a = `${prefix}t1`
  const b = `${prefix}t2`
  const ws: Workspace = { id: `${prefix}ws`, name: `${sentinel}-ws`, tabs: [a, b], activeTabId: a }
  return { workspaces: [ws], tabs: { [a]: tab(a, sentinel), [b]: tab(b, sentinel) }, activeWorkspaceId: ws.id, activeTabId: a }
}

const masterWorld = (): ParkedWorld => world('m', MASTER_SENTINEL)
const slaveWorld = (): ParkedWorld => world('s', SLAVE_SENTINEL)

const slaveRecord = (w: ParkedWorld | null) => ({ [SLAVE]: { id: SLAVE, name: 'Slave', createdAt: 1, shownHostIds: [], world: w } })

/** This window, before anybody switched: the master on screen, the slave parked, epoch 0. */
function masterOnScreen(): void {
  const m = masterWorld()
  useLocalProfilesStore.setState({ slaves: slaveRecord(slaveWorld()), slaveOrder: [SLAVE], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  useTabStore.setState({ tabs: m.tabs, tabOrder: Object.keys(m.tabs), activeTabId: m.activeTabId, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: m.workspaces, activeWorkspaceId: m.activeWorkspaceId, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
}

function slaveOnScreen(epoch = 1): void {
  const s = slaveWorld()
  useLocalProfilesStore.setState({ slaves: slaveRecord(null), slaveOrder: [SLAVE], activeProfileId: SLAVE, parkedMaster: masterWorld(), worldEpoch: epoch })
  useTabStore.setState({ tabs: s.tabs, tabOrder: Object.keys(s.tabs), activeTabId: s.activeTabId, visitHistory: [], worldId: SLAVE, worldEpoch: epoch })
  useWorkspaceStore.setState({ workspaces: s.workspaces, activeWorkspaceId: s.activeWorkspaceId, worldId: SLAVE, worldEpoch: epoch })
}

/** What ANOTHER window's switch to the slave left in storage, one key per store — what this window's rehydrates read. */
const SWITCHED_TO_SLAVE: Record<string, { state: Record<string, unknown>; version: number }> = {
  [STORAGE_KEYS.TABS]: { state: { tabs: slaveWorld().tabs, tabOrder: Object.keys(slaveWorld().tabs), activeTabId: 'st1', worldId: SLAVE, worldEpoch: 1 }, version: 3 },
  [STORAGE_KEYS.WORKSPACES]: { state: { workspaces: slaveWorld().workspaces, activeWorkspaceId: 'sws', worldId: SLAVE, worldEpoch: 1 }, version: 1 },
  [STORAGE_KEYS.LOCAL_PROFILES]: { state: { slaves: slaveRecord(null), slaveOrder: [SLAVE], activeProfileId: SLAVE, parkedMaster: masterWorld(), worldEpoch: 1 }, version: 1 },
}

const STORES = {
  [STORAGE_KEYS.TABS]: useTabStore,
  [STORAGE_KEYS.WORKSPACES]: useWorkspaceStore,
  [STORAGE_KEYS.LOCAL_PROFILES]: useLocalProfilesStore,
} as const

/** One store of this window catches up with the other window's write — exactly what `syncManager` makes it do. */
async function rehydrate(key: string): Promise<void> {
  localStorage.setItem(key, JSON.stringify(SWITCHED_TO_SLAVE[key]))
  await STORES[key as keyof typeof STORES].persist.rehydrate()
}

function permutations<T>(items: T[]): T[][] {
  if (items.length <= 1) return [items]
  return items.flatMap((item, i) => permutations([...items.slice(0, i), ...items.slice(i + 1)]).map((rest) => [item, ...rest]))
}

let reports: SectionReport[] = []
let problems: { kind: string; detail: string }[] = []
let collector: Collector | null = null
let clock = 0

function start(): Collector {
  collector = startCollector({ onSection: (r) => reports.push(r), onProblem: (p) => problems.push(p), now: () => clock })
  return collector
}

/** Started, primed, and the baseline thrown away: from here on every report is news. */
async function primed(): Promise<Collector> {
  const c = start()
  await c.primeAll()
  reports = []
  return c
}

const SETTLE = 5_000

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  __resetMasterWorldForTest()
  reports = []
  problems = []
  clock = 0
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'h1', ip: '10.0.0.1', port: 7860, order: 0 } }, hostOrder: ['h1'], activeHostId: 'h1', devHostId: null, runtime: {} })
  useWorkspaceSettingsStore.setState({ workspaces: {} })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  masterOnScreen()
})

afterEach(() => {
  collector?.stop()
  collector = null
  vi.useRealTimers()
  __resetMasterWorldForTest()
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
})

describe('another window switches to a slave: this window\'s three rehydrates, in every order', () => {
  const KEYS = [STORAGE_KEYS.TABS, STORAGE_KEYS.WORKSPACES, STORAGE_KEYS.LOCAL_PROFILES]

  it.each(permutations(KEYS).map((order) => [order.join(' → '), order] as const))('%s: nothing is reported at any point — the master world did not change', async (_name, order) => {
    await primed()
    for (const key of order) {
      await rehydrate(key)
      await vi.advanceTimersByTimeAsync(SETTLE) // far past the debounce: whatever was armed has fired
      expect(reports).toEqual([])
    }
    expect(readMasterWorld()).toMatchObject({ settled: true, onScreen: false })
    expect(JSON.stringify(readMasterWorld())).not.toContain(SLAVE_SENTINEL)
    await vi.advanceTimersByTimeAsync(SETTLE)
    expect(reports).toEqual([])
  })

  it.each(KEYS.map((missing) => [missing, KEYS.filter((k) => k !== missing)] as const))('the rehydrate of %s never arrives: silence, for as long as it lasts', async (_missing, arriving) => {
    for (const order of permutations([...arriving])) {
      masterOnScreen()
      collector?.stop()
      await primed()
      for (const key of order) {
        await rehydrate(key)
        await vi.advanceTimersByTimeAsync(SETTLE)
      }
      expect(readMasterWorld().settled).toBe(false)
      // …and the user goes on working in what they see, which is the slave:
      useTabStore.getState().togglePin('st1')
      useWorkspaceStore.getState().renameWorkspace('sws', `${SLAVE_SENTINEL}-renamed`)
      await vi.advanceTimersByTimeAsync(60_000)
      expect(reports).toEqual([])
    }
  })

  it('a hosts edit reports nothing, settled or not: `hosts` is retired (host ownership H3a-2)', async () => {
    await primed()
    await rehydrate(STORAGE_KEYS.TABS) // unsettled
    useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'renamed', ip: '10.0.0.1', port: 7860, order: 0 } } })
    await vi.advanceTimersByTimeAsync(SETTLE)
    expect(reports).toEqual([])
  })
})

describe('another window switched away AND back (the world id is `master` before and after — only the epoch tells)', () => {
  // While the master was parked over there an apply changed it: `mt2` is gone, the workspace was renamed.
  const after = (): ParkedWorld => {
    const m = masterWorld()
    return { ...m, tabs: { mt1: m.tabs.mt1 }, workspaces: [{ ...m.workspaces[0], name: `${MASTER_SENTINEL}-after`, tabs: ['mt1'] }] }
  }
  const BACK_ON_MASTER: Record<string, { state: Record<string, unknown>; version: number }> = {
    [STORAGE_KEYS.TABS]: { state: { tabs: after().tabs, tabOrder: ['mt1'], activeTabId: 'mt1', worldId: MASTER_PROFILE_ID, worldEpoch: 2 }, version: 3 },
    [STORAGE_KEYS.WORKSPACES]: { state: { workspaces: after().workspaces, activeWorkspaceId: 'mws', worldId: MASTER_PROFILE_ID, worldEpoch: 2 }, version: 1 },
    [STORAGE_KEYS.LOCAL_PROFILES]: { state: { slaves: slaveRecord(slaveWorld()), slaveOrder: [SLAVE], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 2 }, version: 1 },
  }

  it.each(permutations(Object.keys(BACK_ON_MASTER)).map((order) => [order.join(' → '), order] as const))('%s: no report is ever built from a MIX of the old and the new master world', async (_name, order) => {
    await primed()
    for (const [i, key] of order.entries()) {
      localStorage.setItem(key, JSON.stringify(BACK_ON_MASTER[key]))
      await STORES[key as keyof typeof STORES].persist.rehydrate()
      await vi.advanceTimersByTimeAsync(SETTLE)
      if (i < order.length - 1) expect(reports).toEqual([]) // two of three: nobody can say
    }
    // All three: the master world DID change, and it is reported — as it is now, once.
    expect(problems).toEqual([])
    expect(reports.map((r) => r.key).sort()).toEqual(['tabs.mws', 'workspaces'])
    expect((reports.find((r) => r.key === 'tabs.mws')!.payload as { order: string[] }).order).toEqual(['mt1'])
    expect(JSON.stringify(reports.find((r) => r.key === 'workspaces')!.payload)).toContain(`${MASTER_SENTINEL}-after`)
  })
})

describe('a slave is on screen (settled)', () => {
  it('editing the slave arms no timer and reports nothing', async () => {
    slaveOnScreen()
    await primed()
    useTabStore.getState().togglePin('st1')
    useWorkspaceStore.getState().renameWorkspace('sws', 'x')
    useWorkspaceStore.getState().addWorkspace('another')
    await vi.advanceTimersByTimeAsync(0)
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(SETTLE)
    expect(reports).toEqual([])
  })

  it('primeAll reports the PARKED master, and the settings section is scoped by ITS workspaces', async () => {
    slaveOnScreen()
    useWorkspaceSettingsStore.setState({ workspaces: { mws: { a: 1 }, sws: { a: 2 } } } as never)
    await start().primeAll()
    expect(reports.map((r) => r.key).sort()).toEqual(['settings', 'tabs.mws', 'workspaces'])
    const all = JSON.stringify(reports)
    expect(all).toContain(MASTER_SENTINEL)
    expect(all).not.toContain(SLAVE_SENTINEL)
    const settings = reports.find((r) => r.key === 'settings')!.payload as Record<string, { workspaces?: Record<string, unknown> }>
    expect(Object.keys(settings['purdex-workspace-settings'].workspaces ?? {})).toEqual(['mws'])
  })

  // (Was: "the standalone census counts the MASTER world's tabs" — the census is gone in P3c-2. What is left
  // of it: a parked master's ownerless tab still enters no section, and is reported by nobody.)
  it('a tab of the parked MASTER world that is in no workspace enters no section and is no problem', async () => {
    slaveOnScreen()
    const m = masterWorld()
    useLocalProfilesStore.setState({ parkedMaster: { ...m, tabs: { ...m.tabs, solo: tab('solo', MASTER_SENTINEL) } } })
    await start().primeAll()
    expect(reports.map((r) => r.key).sort()).toEqual(['settings', 'tabs.mws', 'workspaces'])
    expect(JSON.stringify(reports)).not.toContain('"solo"')
    expect(problems).toEqual([])
  })

  it('an apply lands in the parked master: the screen does not move, and the one report carries the hash the apply answered — what the executor already holds, so nothing is pushed back', async () => {
    slaveOnScreen()
    await primed()
    const liveTabs = useTabStore.getState()
    const liveWs = useWorkspaceStore.getState()
    const payload: WorkspacesPayload = { order: ['mws'], workspaces: { mws: { name: `${MASTER_SENTINEL}-from-the-sot` } } }

    const outcome = await applySectionToStores('workspaces', payload, { masterHostId: 'h1' })
    await vi.advanceTimersByTimeAsync(SETTLE)

    expect(useLocalProfilesStore.getState().parkedMaster?.workspaces[0].name).toBe(`${MASTER_SENTINEL}-from-the-sot`)
    expect(useTabStore.getState()).toBe(liveTabs)
    expect(useWorkspaceStore.getState()).toBe(liveWs)
    expect(outcome.ok).toBe(true)
    expect(reports.map((r) => [r.key, r.hash])).toEqual([['workspaces', outcome.ok ? outcome.hash : '']])
  })

  it('switching back after such an apply puts the APPLIED master on screen, and reports nothing', async () => {
    slaveOnScreen()
    await primed()
    await applySectionToStores('workspaces', { order: ['mws'], workspaces: { mws: { name: `${MASTER_SENTINEL}-from-the-sot` } } }, { masterHostId: 'h1' })
    await vi.advanceTimersByTimeAsync(SETTLE)
    reports = []

    // What Task 5's `switchActiveProfile('master')` does, as ONE synchronous block.
    const t = useTabStore.getState()
    const w = useWorkspaceStore.getState()
    const swapped = useLocalProfilesStore.getState().swapActive(MASTER_PROFILE_ID, { tabs: t.tabs, workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId, activeTabId: t.activeTabId }, 2)
    if (!swapped.ok) throw new Error(swapped.reason)
    commitTabWorld(swapped.world, undefined, { worldId: MASTER_PROFILE_ID, worldEpoch: 2 })

    expect(readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
    expect(useWorkspaceStore.getState().workspaces[0].name).toBe(`${MASTER_SENTINEL}-from-the-sot`)
    expect(useTabStore.getState().activeTabId).toBe('mt1')
    expect(useLocalProfilesStore.getState().slaves[SLAVE].world).toMatchObject({ activeWorkspaceId: 'sws' })
    await vi.advanceTimersByTimeAsync(SETTLE)
    expect(reports).toEqual([])
  })
})

describe('unsettled → settled: the master world is looked at again, whole', () => {
  it('an edit whose debounce was cut off by the unsettled stretch is reported once it settles', async () => {
    await primed()
    useWorkspaceStore.getState().renameWorkspace('mws', `${MASTER_SENTINEL}-edited`)
    await vi.advanceTimersByTimeAsync(100) // inside the debounce
    useWorkspaceStore.setState({ worldEpoch: 9 }) // unsettled: the timer is dropped with it
    await vi.advanceTimersByTimeAsync(SETTLE)
    expect(reports).toEqual([])

    useWorkspaceStore.setState({ worldEpoch: 0 }) // settled again — and not one reference of the world has moved
    await vi.advanceTimersByTimeAsync(SETTLE)
    expect(reports.map((r) => r.key)).toEqual(['workspaces'])
    expect(JSON.stringify(reports[0].payload)).toContain(`${MASTER_SENTINEL}-edited`)
  })

  it('primed while unsettled: nothing (`hosts` is retired, H3a-2); the rest follows by itself the moment the world settles', async () => {
    useTabStore.setState({ worldEpoch: 9 })
    await start().primeAll()
    expect(reports).toEqual([])
    reports = []
    useTabStore.setState({ worldEpoch: 0 })
    await vi.advanceTimersByTimeAsync(SETTLE)
    expect(reports.map((r) => r.key).sort()).toEqual(['settings', 'tabs.mws', 'workspaces'])
  })

  it('a settings edit made while unsettled is neither built nor lost', async () => {
    await primed()
    useTabStore.setState({ worldEpoch: 9 })
    useWorkspaceSettingsStore.setState({ workspaces: { mws: { a: 1 } } } as never)
    await vi.advanceTimersByTimeAsync(SETTLE)
    expect(reports).toEqual([])
    useTabStore.setState({ worldEpoch: 0 })
    await vi.advanceTimersByTimeAsync(SETTLE)
    expect(reports.map((r) => r.key)).toEqual(['settings'])
  })
})

describe('world-unsettled: a stretch that does not end is said, once', () => {
  it('not at five seconds, at the first change after them; once per stretch; again for the next one', async () => {
    await primed()
    clock = 1_000
    useTabStore.setState({ worldEpoch: 9, worldId: SLAVE }) // the stretch begins
    clock = 1_000 + MASTER_WORLD_STUCK_MS
    useTabStore.getState().togglePin('mt1')
    expect(problems).toEqual([])
    clock += 1
    useTabStore.getState().togglePin('mt1')
    expect(problems).toEqual([{ kind: 'world-unsettled', detail: 'epoch-mismatch' }])
    clock += 60_000
    useTabStore.getState().togglePin('mt1')
    expect(problems).toHaveLength(1)

    useTabStore.setState({ worldEpoch: 0, worldId: MASTER_PROFILE_ID }) // settled
    clock += 60_000
    useTabStore.setState({ worldId: SLAVE }) // a new stretch, with its own clock
    useTabStore.getState().togglePin('mt1')
    expect(problems).toHaveLength(1)
    clock += MASTER_WORLD_STUCK_MS + 1
    useTabStore.getState().togglePin('mt1')
    expect(problems).toEqual([
      { kind: 'world-unsettled', detail: 'epoch-mismatch' },
      { kind: 'world-unsettled', detail: 'world-mismatch' },
    ])
  })

  it('a settled collector never asks, whatever the screen does', async () => {
    slaveOnScreen()
    await primed()
    clock = 1_000_000
    useTabStore.getState().togglePin('st1')
    expect(problems).toEqual([])
  })
})
