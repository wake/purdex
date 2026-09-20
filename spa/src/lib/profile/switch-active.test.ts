// spa/src/lib/profile/switch-active.test.ts — switching the world on screen, the
// one copy, the one move (P3 plan, P3b Task 5).
//
// As in collector.world.test.ts every world carries a SENTINEL in each pane's
// `cachedName` and each workspace's name, so "whose content is that" is a string
// search. The collector in here is the real one.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../../stores/useHostStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../../stores/useLocalProfilesStore'
import { useProfileStore } from '../../stores/useProfileStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { STORAGE_KEYS } from '../storage'
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import { readSettingsSources } from './apply-to-stores'
import { startCollector, type Collector, type SectionReport } from './collector'
import { __resetMasterWorldForTest, masterWorkspaceIds, readMasterWorld } from './master-world'
import { buildSettingsSection } from './sections'
import {
  PROFILE_SWITCH_LOCK_OWNER,
  copyMasterAsSlave,
  deleteSlave,
  promoteToMaster,
  renameSlave,
  reorderSlaves,
  saveScreenAsSlave,
  switchActiveProfile,
} from './switch-active'

vi.mock('./hash', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./hash')>()
  return { ...actual, hashSection: vi.fn(async (payload: unknown) => actual.structuralKey(payload)) }
})

// === fixtures ===

const MASTER_SENTINEL = 'SENTINEL-MASTER'
const SLAVE_SENTINEL = 'SENTINEL-SLAVE'
const OTHER_SENTINEL = 'SENTINEL-OTHER'
const SLAVE = 's1'
const OTHER = 's2'

const terminal = (id: string, sentinel: string): PaneLayout => ({
  type: 'leaf',
  pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: `c-${id}`, mode: 'terminal', cachedName: `${sentinel}-${id}`, tmuxInstance: 'inst' } },
})

function tab(id: string, sentinel: string): Tab {
  return { id, pinned: false, locked: false, createdAt: 1, layout: terminal(id, sentinel) }
}

/** A split: a terminal next to the settings page OF ITS OWN WORKSPACE — the one pane content that names a workspace id. */
function splitTab(id: string, sentinel: string, workspaceId: string): Tab {
  return {
    id,
    pinned: true,
    locked: false,
    createdAt: 2,
    layout: {
      type: 'split',
      id: `split-${id}`,
      direction: 'h',
      sizes: [50, 50],
      children: [terminal(`${id}-l`, sentinel), { type: 'leaf', pane: { id: `p-${id}-r`, content: { kind: 'settings', scope: { workspaceId } } } }],
    },
  }
}

function world(prefix: string, sentinel: string): ParkedWorld {
  const a = `${prefix}t1`
  const b = `${prefix}t2`
  const wsId = `${prefix}ws`
  const ws: Workspace = { id: wsId, name: `${sentinel}-ws`, tabs: [a, b], activeTabId: b, moduleConfig: { files: { projectPath: `/${sentinel}` } } }
  return { workspaces: [ws], tabs: { [a]: tab(a, sentinel), [b]: splitTab(b, sentinel, wsId) }, activeWorkspaceId: wsId, activeTabId: b }
}

const masterWorld = (): ParkedWorld => world('m', MASTER_SENTINEL)
const slaveWorld = (): ParkedWorld => world('s', SLAVE_SENTINEL)
const otherWorld = (): ParkedWorld => world('o', OTHER_SENTINEL)

const slave = (id: string, w: ParkedWorld | null) => ({ id, name: `Slave ${id}`, createdAt: 1, world: w })

function putOnScreen(w: ParkedWorld, worldId: string, epoch: number): void {
  useTabStore.setState({ tabs: w.tabs, tabOrder: w.workspaces.flatMap((x) => x.tabs), activeTabId: w.activeTabId, visitHistory: [], worldId, worldEpoch: epoch })
  useWorkspaceStore.setState({ workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId, worldId, worldEpoch: epoch })
}

/** The master on screen, two slaves parked, epoch 0. */
function masterOnScreen(): void {
  useLocalProfilesStore.setState({ slaves: { [SLAVE]: slave(SLAVE, slaveWorld()), [OTHER]: slave(OTHER, otherWorld()) }, slaveOrder: [SLAVE, OTHER], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  putOnScreen(masterWorld(), MASTER_PROFILE_ID, 0)
}

/** `s1` on screen, the master and `s2` parked, epoch 1. */
function slaveOnScreen(): void {
  useLocalProfilesStore.setState({ slaves: { [SLAVE]: slave(SLAVE, null), [OTHER]: slave(OTHER, otherWorld()) }, slaveOrder: [SLAVE, OTHER], activeProfileId: SLAVE, parkedMaster: masterWorld(), worldEpoch: 1 })
  putOnScreen(slaveWorld(), SLAVE, 1)
}

const LOCAL_FIELDS = ['slaves', 'slaveOrder', 'activeProfileId', 'parkedMaster', 'worldEpoch'] as const
const TAB_FIELDS = ['tabs', 'tabOrder', 'activeTabId', 'visitHistory', 'worldId', 'worldEpoch'] as const
const WS_FIELDS = ['workspaces', 'activeWorkspaceId', 'worldId', 'worldEpoch'] as const

const pick = (state: object, fields: readonly string[]): unknown[] => fields.map((f) => (state as Record<string, unknown>)[f])

/** The three stores' data, BY REFERENCE: equal before and after ⇔ nothing was written, or everything was put back. */
const threeStores = (): unknown[] => [...pick(useLocalProfilesStore.getState(), LOCAL_FIELDS), ...pick(useTabStore.getState(), TAB_FIELDS), ...pick(useWorkspaceStore.getState(), WS_FIELDS)]

const screen = (): ParkedWorld => {
  const t = useTabStore.getState()
  const w = useWorkspaceStore.getState()
  return { workspaces: w.workspaces, tabs: t.tabs, activeWorkspaceId: w.activeWorkspaceId, activeTabId: t.activeTabId }
}

const persisted = (key: string): Record<string, unknown> => (JSON.parse(localStorage.getItem(key) ?? '{"state":{}}') as { state: Record<string, unknown> }).state

function layoutIds(layout: PaneLayout, out: string[]): void {
  if (layout.type === 'leaf') {
    out.push(layout.pane.id)
    return
  }
  out.push(layout.id)
  for (const child of layout.children) layoutIds(child, out)
}

/** Every id a world MINTS: workspaces, tabs, panes, splits. */
function mintedIds(w: ParkedWorld): { workspaces: string[]; tabs: string[]; layout: string[] } {
  const layout: string[] = []
  for (const t of Object.values(w.tabs)) layoutIds(t.layout, layout)
  return { workspaces: w.workspaces.map((x) => x.id), tabs: Object.keys(w.tabs), layout }
}

function objectsOf(value: unknown, out = new Set<object>()): Set<object> {
  if (typeof value !== 'object' || value === null || out.has(value)) return out
  out.add(value)
  for (const child of Object.values(value)) objectsOf(child, out)
  return out
}

/** A world with every minted id replaced by its POSITION — two worlds that differ in ids only come out equal. */
function shapeOf(w: ParkedWorld): unknown {
  const ids = mintedIds(w)
  const names = new Map<string, string>()
  ids.workspaces.forEach((id, i) => names.set(id, `W${i}`))
  w.workspaces.flatMap((x) => x.tabs).forEach((id, i) => names.set(id, `T${i}`))
  ids.layout.forEach((id, i) => names.set(id, `L${i}`))
  const rename = (v: unknown): unknown => {
    if (typeof v === 'string') return names.get(v) ?? v
    if (Array.isArray(v)) return v.map(rename)
    if (typeof v === 'object' && v !== null) return Object.fromEntries(Object.entries(v).map(([k, x]) => [names.get(k) ?? k, rename(x)]))
    return v
  }
  return rename({ ...w, tabs: w.workspaces.flatMap((x) => x.tabs).map((id) => w.tabs[id]) })
}

// === the real collector ===

let reports: SectionReport[] = []
let collector: Collector | null = null
const SETTLE = 5_000

async function primed(): Promise<void> {
  collector = startCollector({ onSection: (r) => reports.push(r), onProblem: () => {}, now: () => 0 })
  await collector.primeAll()
  reports = []
}

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  reports = []
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'h1', ip: '10.0.0.1', port: 7860, order: 0 } }, hostOrder: ['h1'], activeHostId: 'h1', devHostId: null, runtime: {} })
  useProfileStore.setState({ masterHostId: null, masterProfileId: null })
  useWorkspaceSettingsStore.setState({ workspaces: {} })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  masterOnScreen()
})

afterEach(() => {
  collector?.stop()
  collector = null
  vi.restoreAllMocks()
  vi.useRealTimers()
  __resetMasterWorldForTest()
  useProfileStore.setState({ masterHostId: null, masterProfileId: null })
  useWorkspaceSettingsStore.setState({ workspaces: {} })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  putOnScreen({ workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null }, MASTER_PROFILE_ID, 0)
})

// === A. switchActiveProfile ===

describe('switchActiveProfile', () => {
  it('master → slave: the slave is on screen, the master is parked, and the three stores carry ONE new tag', async () => {
    expect(await switchActiveProfile(SLAVE)).toEqual({ ok: true })

    expect(screen()).toEqual(slaveWorld())
    expect(useTabStore.getState().tabOrder).toEqual(['st1', 'st2'])
    expect(useLocalProfilesStore.getState().parkedMaster).toEqual(masterWorld())
    expect(useLocalProfilesStore.getState().slaves[SLAVE].world).toBeNull()
    expect(useLocalProfilesStore.getState()).toMatchObject({ activeProfileId: SLAVE, worldEpoch: 1 })
    expect(useTabStore.getState()).toMatchObject({ worldId: SLAVE, worldEpoch: 1 })
    expect(useWorkspaceStore.getState()).toMatchObject({ worldId: SLAVE, worldEpoch: 1 })
    expect(readMasterWorld()).toMatchObject({ settled: true, onScreen: false })
    // …and that is what another window will read.
    expect(persisted(STORAGE_KEYS.TABS)).toMatchObject({ worldId: SLAVE, worldEpoch: 1 })
    expect(persisted(STORAGE_KEYS.WORKSPACES)).toMatchObject({ worldId: SLAVE, worldEpoch: 1 })
    expect(persisted(STORAGE_KEYS.LOCAL_PROFILES)).toMatchObject({ activeProfileId: SLAVE, worldEpoch: 1 })
  })

  it('the whole exchange is ONE synchronous block: it is over before the promise is even looked at, and no microtask ran inside it', () => {
    let ticked = false
    const seen: boolean[] = []
    queueMicrotask(() => {
      ticked = true
    })
    const unsubs = [useLocalProfilesStore, useTabStore, useWorkspaceStore].map((s) => s.subscribe(() => seen.push(ticked)))
    const pending = switchActiveProfile(SLAVE)
    for (const unsub of unsubs) unsub()

    expect(readMasterWorld()).toMatchObject({ settled: true, onScreen: false }) // synchronously
    expect(screen()).toEqual(slaveWorld())
    expect(seen.length).toBeGreaterThanOrEqual(3)
    expect(seen).not.toContain(true)
    return expect(pending).resolves.toEqual({ ok: true })
  })

  it('slave → another slave, directly: the master stays parked and untouched', async () => {
    slaveOnScreen()
    const parked = useLocalProfilesStore.getState().parkedMaster
    expect(await switchActiveProfile(OTHER)).toEqual({ ok: true })
    expect(screen()).toEqual(otherWorld())
    expect(useLocalProfilesStore.getState().parkedMaster).toBe(parked)
    expect(useLocalProfilesStore.getState().slaves[SLAVE].world).toEqual(slaveWorld())
    expect(useLocalProfilesStore.getState().slaves[OTHER].world).toBeNull()
    expect(readMasterWorld()).toMatchObject({ settled: true, onScreen: false })
  })

  it('master → slave → master: the master world is what it was, byte for byte, at epoch 2', async () => {
    const before = JSON.stringify(screen())
    const orderBefore = JSON.stringify(useTabStore.getState().tabOrder)
    await switchActiveProfile(SLAVE)
    expect(await switchActiveProfile(MASTER_PROFILE_ID)).toEqual({ ok: true })

    expect(JSON.stringify(screen())).toBe(before)
    expect(JSON.stringify(useTabStore.getState().tabOrder)).toBe(orderBefore)
    expect(useLocalProfilesStore.getState()).toMatchObject({ activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 2 })
    expect(JSON.stringify(useLocalProfilesStore.getState().slaves[SLAVE].world)).toBe(JSON.stringify(slaveWorld()))
    expect(readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
    expect(useTabStore.getState()).toMatchObject({ worldId: MASTER_PROFILE_ID, worldEpoch: 2 })
    expect(useWorkspaceStore.getState()).toMatchObject({ worldId: MASTER_PROFILE_ID, worldEpoch: 2 })
  })

  it('what was edited on screen is what gets parked', async () => {
    await switchActiveProfile(SLAVE)
    useTabStore.getState().togglePin('st1')
    await switchActiveProfile(MASTER_PROFILE_ID)
    expect(useLocalProfilesStore.getState().slaves[SLAVE].world?.tabs.st1.pinned).toBe(true)
  })

  it.each([
    ['an unknown id', 'nope', 'not-found'],
    ['the master, while it is on screen', MASTER_PROFILE_ID, 'already-on-screen'],
  ])('%s: refused, and not one store was written', async (_name, target, reason) => {
    const before = threeStores()
    expect(await switchActiveProfile(target)).toEqual({ ok: false, reason })
    expect(threeStores()).toEqual(before)
    threeStores().forEach((v, i) => expect(v).toBe(before[i]))
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('the slave that is on screen: refused', async () => {
    slaveOnScreen()
    expect(await switchActiveProfile(SLAVE)).toEqual({ ok: false, reason: 'already-on-screen' })
  })

  it('unsettled — nobody can say whose world the screen holds, so nobody may park it under a label: refused', async () => {
    // Another window switched to the slave; this one has its tabs already and the pointer not yet.
    useTabStore.setState({ tabs: slaveWorld().tabs, worldId: SLAVE, worldEpoch: 1 })
    const before = threeStores()
    const pending = switchActiveProfile(OTHER)
    // Looked at before the promise is: the refusal wrote nothing. (A turn later the three stores are asked to read
    // storage again — master-world.ts, `recoverUnsettledWorld` — and that does give every object a new identity.)
    threeStores().forEach((v, i) => expect(v).toBe(before[i]))
    expect(await pending).toEqual({ ok: false, reason: 'unsettled' })
    expect(readMasterWorld().settled).toBe(false) // storage holds the same disagreement: nothing to recover from
  })

  it('takes the operation lock: refused while a rebuild, a restore or an apply holds it; released afterwards either way', async () => {
    const grant = useRebuildStore.getState().acquireOperationLock('rebuild:batch')
    const before = threeStores()
    expect(await switchActiveProfile(SLAVE)).toEqual({ ok: false, reason: 'busy' })
    threeStores().forEach((v, i) => expect(v).toBe(before[i]))
    useRebuildStore.getState().releaseOperationLock(grant)

    const owners: (string | null)[] = []
    const unsub = useTabStore.subscribe(() => owners.push(useRebuildStore.getState().lockedBy))
    expect(await switchActiveProfile(SLAVE)).toEqual({ ok: true })
    unsub()
    expect(owners).toEqual([PROFILE_SWITCH_LOCK_OWNER])
    expect(useRebuildStore.getState().lockedBy).toBeNull()
    await switchActiveProfile('nope')
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  describe('a write that throws', () => {
    it('in the second live store — after `swapActive` has already moved the pointer: all THREE stores are back, synchronously', () => {
      const before = threeStores()
      const real = useWorkspaceStore.setState
      let calls = 0
      vi.spyOn(useWorkspaceStore, 'setState').mockImplementation((...args) => {
        if (++calls === 1) throw new Error('workspace write failed')
        real(...(args as Parameters<typeof real>))
      })
      const pending = switchActiveProfile(SLAVE)
      threeStores().forEach((v, i) => expect(v).toBe(before[i])) // before the promise is looked at
      expect(readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
      return pending.then((result) => {
        expect(result).toEqual({ ok: false, reason: 'write-failed', detail: 'workspace write failed' })
        expect(useRebuildStore.getState().lockedBy).toBeNull()
        expect(persisted(STORAGE_KEYS.LOCAL_PROFILES)).toMatchObject({ activeProfileId: MASTER_PROFILE_ID, worldEpoch: 0 })
      })
    })

    it('in the parking lot\'s own storage write — persist has moved the pointer IN MEMORY by then: it is put back, the live stores were never touched', async () => {
      const before = threeStores()
      const real = Storage.prototype.setItem
      let thrown = 0
      vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key: string, value: string) {
        if (key === STORAGE_KEYS.LOCAL_PROFILES && thrown++ === 0) throw new Error('quota')
        real.call(this, key, value)
      })
      expect(await switchActiveProfile(SLAVE)).toEqual({ ok: false, reason: 'write-failed', detail: 'quota' })
      expect(thrown).toBeGreaterThan(0)
      threeStores().forEach((v, i) => expect(v).toBe(before[i]))
      expect(readMasterWorld()).toMatchObject({ settled: true, onScreen: true })
    })
  })

  describe('with the real collector running', () => {
    beforeEach(() => {
      vi.useFakeTimers()
    })

    it('master → slave → edit the slave → master: not one report, at any point', async () => {
      await primed()
      await switchActiveProfile(SLAVE)
      await vi.advanceTimersByTimeAsync(SETTLE)
      expect(reports).toEqual([])

      useTabStore.getState().togglePin('st1')
      useWorkspaceStore.getState().renameWorkspace('sws', `${SLAVE_SENTINEL}-renamed`)
      useWorkspaceStore.getState().addWorkspace(`${SLAVE_SENTINEL}-new`)
      await vi.advanceTimersByTimeAsync(SETTLE)
      expect(reports).toEqual([])

      await switchActiveProfile(MASTER_PROFILE_ID)
      await vi.advanceTimersByTimeAsync(SETTLE)
      expect(reports).toEqual([])
      expect(JSON.stringify(screen())).not.toContain(SLAVE_SENTINEL)

      // …and the master is still listened to: an edit of it now IS news.
      useWorkspaceStore.getState().renameWorkspace('mws', `${MASTER_SENTINEL}-edited`)
      await vi.advanceTimersByTimeAsync(SETTLE)
      expect(reports.map((r) => r.key)).toEqual(['workspaces'])
      expect(JSON.stringify(reports)).not.toContain(SLAVE_SENTINEL)
    })
  })
})

// === B. copyMasterAsSlave ===

describe('copyMasterAsSlave', () => {
  const copied = (): ParkedWorld => {
    const result = copyMasterAsSlave('Copy')
    if (!result.ok) throw new Error(result.reason)
    const world = useLocalProfilesStore.getState().slaves[result.id].world
    if (world === null) throw new Error('the copy is not parked')
    return world
  }

  it('a new PARKED slave, last in the order, under the normalised name; the screen does not move', () => {
    const before = [...pick(useTabStore.getState(), TAB_FIELDS), ...pick(useWorkspaceStore.getState(), WS_FIELDS)]
    const result = copyMasterAsSlave('  Copy  ')
    expect(result.ok).toBe(true)
    if (!result.ok) return
    const local = useLocalProfilesStore.getState()
    expect(local.slaveOrder).toEqual([SLAVE, OTHER, result.id])
    expect(local.slaves[result.id]).toMatchObject({ id: result.id, name: 'Copy' })
    expect(local).toMatchObject({ activeProfileId: MASTER_PROFILE_ID, worldEpoch: 0 })
    const after = [...pick(useTabStore.getState(), TAB_FIELDS), ...pick(useWorkspaceStore.getState(), WS_FIELDS)]
    after.forEach((v, i) => expect(v).toBe(before[i]))
  })

  it('EVERY id is new — workspace, tab, pane, split — and the same world comes out underneath them', () => {
    const copy = copied()
    const source = mintedIds(masterWorld())
    const fresh = mintedIds(copy)
    for (const kind of ['workspaces', 'tabs', 'layout'] as const) {
      expect(fresh[kind]).toHaveLength(source[kind].length)
      for (const id of fresh[kind]) expect(source[kind]).not.toContain(id)
    }
    const all = [...fresh.workspaces, ...fresh.tabs, ...fresh.layout]
    expect(new Set(all).size).toBe(all.length)
    expect(shapeOf(copy)).toEqual(shapeOf(masterWorld()))
  })

  it('every cross-reference follows its id', () => {
    const copy = copied()
    const [ws] = copy.workspaces
    expect(Object.keys(copy.tabs).sort()).toEqual([...ws.tabs].sort())
    for (const [key, t] of Object.entries(copy.tabs)) expect(t.id).toBe(key)
    expect(copy.activeWorkspaceId).toBe(ws.id)
    expect(copy.activeTabId).toBe(ws.tabs[1])
    expect(ws.activeTabId).toBe(ws.tabs[1])
    const split = copy.tabs[ws.tabs[1]].layout
    if (split.type !== 'split' || split.children[1].type !== 'leaf') throw new Error('not the split')
    expect(split.children[1].pane.content).toEqual({ kind: 'settings', scope: { workspaceId: ws.id } })
  })

  it('the tmux bindings are copied as they are: a slave borrows the same sessions (decisions 9, 13)', () => {
    const copy = copied()
    const first = copy.tabs[copy.workspaces[0].tabs[0]].layout
    if (first.type !== 'leaf') throw new Error('not a leaf')
    expect(first.pane.content).toEqual({ kind: 'tmux-session', hostId: 'h1', sessionCode: 'c-mt1', mode: 'terminal', cachedName: `${MASTER_SENTINEL}-mt1`, tmuxInstance: 'inst' })
  })

  it('shares NOT ONE object with its source: editing the copy cannot reach the master', () => {
    const source = screen()
    const frozen = JSON.stringify(source)
    const copy = copied()
    const sourceObjects = objectsOf(source)
    for (const obj of objectsOf(copy)) expect(sourceObjects.has(obj)).toBe(false)

    const t = copy.tabs[copy.workspaces[0].tabs[0]]
    t.pinned = true
    if (t.layout.type === 'leaf' && t.layout.pane.content.kind === 'tmux-session') t.layout.pane.content.cachedName = 'changed'
    ;(copy.workspaces[0].moduleConfig as Record<string, Record<string, unknown>>).files.projectPath = '/changed'
    expect(JSON.stringify(screen())).toBe(frozen)
  })

  it('while a slave is on screen it copies the PARKED master, never the screen', () => {
    slaveOnScreen()
    const copy = copied()
    expect(JSON.stringify(copy)).toContain(MASTER_SENTINEL)
    expect(JSON.stringify(copy)).not.toContain(SLAVE_SENTINEL)
  })

  it('unsettled: refused — the live stores are NOT a stand-in for a master nobody can locate', () => {
    useTabStore.setState({ tabs: slaveWorld().tabs, worldId: SLAVE, worldEpoch: 1 })
    const before = useLocalProfilesStore.getState().slaves
    expect(copyMasterAsSlave('Copy')).toEqual({ ok: false, reason: 'unsettled' })
    expect(useLocalProfilesStore.getState().slaves).toBe(before)
  })

  it('a blank name: refused, nothing added, no scoped settings written', () => {
    useWorkspaceSettingsStore.setState({ workspaces: { mws: { files: { a: 1 } } } })
    const settings = useWorkspaceSettingsStore.getState().workspaces
    expect(copyMasterAsSlave('   ')).toEqual({ ok: false, reason: 'bad-name' })
    expect(Object.keys(useLocalProfilesStore.getState().slaves)).toEqual([SLAVE, OTHER])
    expect(useWorkspaceSettingsStore.getState().workspaces).toBe(settings)
  })

  describe('workspace-scoped settings', () => {
    it('follow the workspace to its new id, as a copy of their own', () => {
      useWorkspaceSettingsStore.setState({ workspaces: { mws: { files: { showHidden: true, nested: { a: 1 } } }, unrelated: { files: { x: 1 } } } })
      const copy = copied()
      const all = useWorkspaceSettingsStore.getState().workspaces
      const newId = copy.workspaces[0].id
      expect(all[newId]).toEqual({ files: { showHidden: true, nested: { a: 1 } } })
      expect(all[newId]).not.toBe(all.mws)
      expect(all[newId].files).not.toBe(all.mws.files)
      expect(all[newId].files.nested).not.toBe(all.mws.files.nested)
      expect(Object.keys(all).sort()).toEqual([newId, 'mws', 'unrelated'].sort())
    })

    it('never reach the SOT: the settings section is built without the new id, and the real collector reports nothing', async () => {
      vi.useFakeTimers()
      useWorkspaceSettingsStore.setState({ workspaces: { mws: { files: { showHidden: true } } } })
      await primed()
      const newId = copied().workspaces[0].id
      await vi.advanceTimersByTimeAsync(SETTLE)
      expect(reports).toEqual([])

      const ids = masterWorkspaceIds()
      if (ids === null) throw new Error('unsettled')
      const built = JSON.stringify(buildSettingsSection(readSettingsSources(), ids))
      expect(built).toContain('mws')
      expect(built).not.toContain(newId)
    })
  })
})

// === C. saveScreenAsSlave ===

describe('saveScreenAsSlave', () => {
  it('copies WHAT IS ON SCREEN, whoever owns it — here a slave — under new ids, and the screen does not move', () => {
    slaveOnScreen()
    useWorkspaceSettingsStore.setState({ workspaces: { sws: { files: { a: 1 } } } })
    const before = threeStores().slice(LOCAL_FIELDS.length)
    const result = saveScreenAsSlave('Saved')
    expect(result.ok).toBe(true)
    if (!result.ok) return
    threeStores().slice(LOCAL_FIELDS.length).forEach((v, i) => expect(v).toBe(before[i]))

    const saved = useLocalProfilesStore.getState().slaves[result.id].world
    if (saved === null) throw new Error('not parked')
    expect(shapeOf(saved)).toEqual(shapeOf(slaveWorld()))
    expect(mintedIds(saved).workspaces).not.toContain('sws')
    for (const obj of objectsOf(saved)) expect(objectsOf(screen()).has(obj)).toBe(false)
    expect(useWorkspaceSettingsStore.getState().workspaces[saved.workspaces[0].id]).toEqual({ files: { a: 1 } })
    expect(useLocalProfilesStore.getState()).toMatchObject({ activeProfileId: SLAVE, worldEpoch: 1 })
  })

  it('with the master on screen it is the master\'s', () => {
    const result = saveScreenAsSlave('Saved')
    if (!result.ok) throw new Error(result.reason)
    expect(JSON.stringify(useLocalProfilesStore.getState().slaves[result.id].world)).toContain(MASTER_SENTINEL)
  })

  it('works while unsettled too: what the user sees is saved before a pull, whatever the labels say (decision 12)', () => {
    useTabStore.setState({ worldEpoch: 9 })
    expect(saveScreenAsSlave('Saved').ok).toBe(true)
  })

  it('a blank name: refused', () => {
    expect(saveScreenAsSlave('')).toEqual({ ok: false, reason: 'bad-name' })
  })
})

// === D. rename / delete / reorder ===

describe('renameSlave / reorderSlaves / deleteSlave', () => {
  it('rename and reorder are the store\'s, answer included', () => {
    expect(renameSlave(SLAVE, ' New ')).toEqual({ ok: true })
    expect(useLocalProfilesStore.getState().slaves[SLAVE].name).toBe('New')
    expect(renameSlave('nope', 'x')).toEqual({ ok: false, reason: 'not-found' })
    expect(renameSlave(SLAVE, ' ')).toEqual({ ok: false, reason: 'bad-name' })
    expect(reorderSlaves([OTHER, SLAVE])).toEqual({ ok: true })
    expect(useLocalProfilesStore.getState().slaveOrder).toEqual([OTHER, SLAVE])
    expect(reorderSlaves([OTHER])).toEqual({ ok: false, reason: 'bad-order' })
  })

  it('deleteSlave: never the one on screen, never an unknown one', () => {
    slaveOnScreen()
    expect(deleteSlave(SLAVE)).toEqual({ ok: false, reason: 'on-screen' })
    expect(deleteSlave('nope')).toEqual({ ok: false, reason: 'not-found' })
    expect(Object.keys(useLocalProfilesStore.getState().slaves)).toEqual([SLAVE, OTHER])
  })

  it('deleteSlave clears the scoped settings of every workspace the slave held — or they would be orphans for ever', () => {
    useWorkspaceSettingsStore.setState({ workspaces: { sws: { files: { a: 1 } }, mws: { files: { b: 1 } }, ows: { files: { c: 1 } } } })
    expect(deleteSlave(SLAVE)).toEqual({ ok: true })
    expect(Object.keys(useLocalProfilesStore.getState().slaves)).toEqual([OTHER])
    expect(Object.keys(useWorkspaceSettingsStore.getState().workspaces).sort()).toEqual(['mws', 'ows'])
  })

  it('…but not those of an id ANOTHER world on this device also uses (a demoted master keeps its ids, and a later pull brings the same ones back)', () => {
    const twin = slaveWorld()
    twin.workspaces[0].id = 'mws' // the master's id
    twin.activeWorkspaceId = 'mws'
    useLocalProfilesStore.setState({ slaves: { ...useLocalProfilesStore.getState().slaves, [SLAVE]: slave(SLAVE, twin) } })
    useWorkspaceSettingsStore.setState({ workspaces: { mws: { files: { b: 1 } } } })
    expect(deleteSlave(SLAVE)).toEqual({ ok: true })
    expect(useWorkspaceSettingsStore.getState().workspaces).toEqual({ mws: { files: { b: 1 } } })
  })
})

// === E. promoteToMaster ===

describe('promoteToMaster — a move, never a copy (decision 10)', () => {
  const demoted = (id: string) => useLocalProfilesStore.getState().slaves[id]

  it('refused while a master is attached, whatever else is true', () => {
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: 'p1' })
    const before = threeStores()
    expect(promoteToMaster(SLAVE, 'Old master')).toEqual({ ok: false, reason: 'master-attached' })
    threeStores().forEach((v, i) => expect(v).toBe(before[i]))
  })

  it('the master on screen: the screen does not move and is now the demoted slave\'s; the parked slave is the master', () => {
    const live = screen()
    const result = promoteToMaster(SLAVE, 'Old master')
    expect(result.ok).toBe(true)
    if (!result.ok) return

    expect(screen().tabs).toBe(live.tabs) // content untouched: a re-stamp, not a rewrite
    expect(screen().workspaces).toBe(live.workspaces)
    expect(useTabStore.getState()).toMatchObject({ worldId: result.demotedId, worldEpoch: 1 })
    expect(useWorkspaceStore.getState()).toMatchObject({ worldId: result.demotedId, worldEpoch: 1 })
    expect(useLocalProfilesStore.getState()).toMatchObject({ activeProfileId: result.demotedId, worldEpoch: 1, slaveOrder: [result.demotedId, OTHER] })
    expect(demoted(result.demotedId)).toMatchObject({ name: 'Old master', world: null })
    expect(demoted(SLAVE)).toBeUndefined()
    const read = readMasterWorld()
    expect(read).toMatchObject({ settled: true, onScreen: false })
    if (read.settled) expect(read.world).toEqual(slaveWorld())
  })

  it('that slave on screen: the screen does not move and is now the master\'s; the old master is a parked slave', () => {
    slaveOnScreen()
    const live = screen()
    const result = promoteToMaster(SLAVE, 'Old master')
    expect(result.ok).toBe(true)
    if (!result.ok) return

    expect(screen().tabs).toBe(live.tabs)
    expect(useTabStore.getState()).toMatchObject({ worldId: MASTER_PROFILE_ID, worldEpoch: 2 })
    expect(useWorkspaceStore.getState()).toMatchObject({ worldId: MASTER_PROFILE_ID, worldEpoch: 2 })
    expect(useLocalProfilesStore.getState()).toMatchObject({ activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 2 })
    expect(demoted(result.demotedId).world).toEqual(masterWorld())
    const read = readMasterWorld()
    expect(read).toMatchObject({ settled: true, onScreen: true })
    if (read.settled) expect(read.world).toEqual(slaveWorld())
  })

  it('ANOTHER slave on screen: the screen keeps its label, and still all three stores get the new epoch', () => {
    slaveOnScreen()
    const result = promoteToMaster(OTHER, 'Old master')
    expect(result.ok).toBe(true)
    if (!result.ok) return

    expect(useTabStore.getState()).toMatchObject({ worldId: SLAVE, worldEpoch: 2 })
    expect(useWorkspaceStore.getState()).toMatchObject({ worldId: SLAVE, worldEpoch: 2 })
    expect(useLocalProfilesStore.getState()).toMatchObject({ activeProfileId: SLAVE, worldEpoch: 2 })
    expect(demoted(result.demotedId).world).toEqual(masterWorld())
    const read = readMasterWorld()
    expect(read).toMatchObject({ settled: true, onScreen: false })
    if (read.settled) expect(read.world).toEqual(otherWorld())
  })

  it.each([
    ['an unknown slave', 'nope', 'Old', 'not-found'],
    ['a blank name for the demoted master', SLAVE, '  ', 'bad-name'],
  ])('%s: refused, nothing written', (_name, id, name, reason) => {
    const before = threeStores()
    expect(promoteToMaster(id, name)).toEqual({ ok: false, reason })
    threeStores().forEach((v, i) => expect(v).toBe(before[i]))
  })

  it('unsettled: refused', () => {
    useTabStore.setState({ worldEpoch: 9 })
    expect(promoteToMaster(SLAVE, 'Old')).toEqual({ ok: false, reason: 'unsettled' })
  })

  it('refused while the operation lock is held, and leaves no lock behind', () => {
    const grant = useRebuildStore.getState().acquireOperationLock('rebuild:batch')
    expect(promoteToMaster(SLAVE, 'Old')).toEqual({ ok: false, reason: 'busy' })
    useRebuildStore.getState().releaseOperationLock(grant)
    expect(promoteToMaster(SLAVE, 'Old').ok).toBe(true)
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })

  it('a re-stamp that throws: the parking lot is put back, all three stores are as they were', () => {
    const before = threeStores()
    vi.spyOn(useWorkspaceStore, 'setState').mockImplementationOnce(() => {
      throw new Error('stamp failed')
    })
    expect(promoteToMaster(SLAVE, 'Old')).toEqual({ ok: false, reason: 'write-failed', detail: 'stamp failed' })
    threeStores().forEach((v, i) => expect(v).toBe(before[i]))
    expect(useRebuildStore.getState().lockedBy).toBeNull()
  })
})
