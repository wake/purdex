// spa/src/features/workspace/lib/adopt-standalone.test.ts — "every tab belongs to exactly one workspace" as a
// STANDING invariant (Profile Sync spec §4.3; P3 plan, P3c-1): whatever puts a tab in `tabOrder` without a
// workspace — device-state's restore and merge, a producer nobody found — the tab ends up in `Unsorted`.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useTabStore } from '../../../stores/useTabStore'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { restoreDeviceStateMerge, restoreDeviceStateReplace } from '../../../lib/device-state/restore'
import { STORAGE_KEYS } from '../../../lib/storage/keys'
import type { WorkspaceSnapshot } from '../../../lib/snapshot/types'
import { createTab, type Tab, type Workspace } from '../../../types/tab'
import { UNSORTED_WORKSPACE_ID, useWorkspaceStore } from '../store'
import { ADOPTION_SETTLE_MS, startStandaloneAdoption } from './adopt-standalone'

let stop: () => void = () => {}
let info: ReturnType<typeof vi.spyOn>
let timeouts: ReturnType<typeof vi.spyOn>

/** How many times THIS module armed its timer (jsdom arms 0 ms timers of its own for every storage write). */
const armed = (): number => timeouts.mock.calls.filter((call: unknown[]) => call[1] === ADOPTION_SETTLE_MS).length

const ws = (id: string, name: string, tabs: string[] = []): Workspace => ({ id, name, tabs, activeTabId: tabs[0] ?? null, moduleConfig: {} })
const ownersOf = (tabId: string): string[] => useWorkspaceStore.getState().workspaces.filter((w) => w.tabs.includes(tabId)).map((w) => w.id)
const unsorted = (): Workspace | undefined => useWorkspaceStore.getState().workspaces.find((w) => w.id === UNSORTED_WORKSPACE_ID)

function addStray(): Tab {
  const tab = createTab({ kind: 'new-tab' })
  useTabStore.getState().addTab(tab)
  return tab
}

function settleWorld(): void {
  useTabStore.setState({ worldId: 'master', worldEpoch: 0 })
  useWorkspaceStore.setState({ worldId: 'master', worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0 })
}

beforeEach(() => {
  vi.useFakeTimers()
  localStorage.clear()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.getState().reset()
  settleWorld()
  info = vi.spyOn(console, 'info').mockImplementation(() => {})
  timeouts = vi.spyOn(globalThis, 'setTimeout')
})

afterEach(() => {
  stop()
  stop = () => {}
  settleWorld()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('at start (before the first render)', () => {
  it('adopts the tabs persisted without a workspace into a new Unsorted, synchronously, in tab order', () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    const owned = addStray()
    useWorkspaceStore.getState().addTabToWorkspace(a.id, owned.id)
    const s1 = addStray()
    const s2 = addStray()

    stop = startStandaloneAdoption()

    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual([a.id, UNSORTED_WORKSPACE_ID])
    expect(unsorted()).toMatchObject({ name: 'Unsorted', tabs: [s1.id, s2.id] })
    expect(ownersOf(owned.id)).toEqual([a.id])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(a.id)
  })

  it('no workspace at all: Unsorted is created and becomes the active workspace', () => {
    const s1 = addStray()
    stop = startStandaloneAdoption()
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(ownersOf(s1.id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(UNSORTED_WORKSPACE_ID)
  })

  it('nothing to adopt: no Unsorted, no write, no timer, no log', () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    const before = useWorkspaceStore.getState().workspaces
    stop = startStandaloneAdoption()
    expect(useWorkspaceStore.getState().workspaces).toBe(before)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(a.id)
    expect(armed()).toBe(0)
    expect(info).not.toHaveBeenCalled()
  })

  it('logs one line per adoption: the count, never the tabs', () => {
    const s1 = addStray()
    addStray()
    stop = startStandaloneAdoption()
    expect(info).toHaveBeenCalledTimes(1)
    const line = info.mock.calls[0].map(String).join(' ')
    expect(line).toContain('2')
    expect(line).not.toContain(s1.id)
  })

  it('a persisted `activeWorkspaceId: null` with workspaces becomes the workspace of the active tab, else the first', () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    const b = useWorkspaceStore.getState().addWorkspace('B')
    const t = addStray()
    useWorkspaceStore.getState().addTabToWorkspace(b.id, t.id)
    useTabStore.getState().setActiveTab(t.id)
    useWorkspaceStore.getState().setActiveWorkspace(null)
    stop = startStandaloneAdoption()
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(b.id)

    stop()
    useTabStore.getState().setActiveTab(null)
    useWorkspaceStore.getState().setActiveWorkspace(null)
    stop = startStandaloneAdoption()
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(a.id)
  })
})

describe('while the app runs', () => {
  it('a tab that appears without a workspace is adopted once the stores have been quiet for ADOPTION_SETTLE_MS', () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    stop = startStandaloneAdoption()

    const stray = addStray()
    expect(ownersOf(stray.id)).toEqual([]) // not in the same tick: see the next test
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS - 1)
    expect(ownersOf(stray.id)).toEqual([])
    vi.advanceTimersByTime(1)
    expect(ownersOf(stray.id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(a.id)
  })

  it('does NOT adopt a tab whose workspace arrives a moment later (the two stores are written, and rehydrated, one after the other)', () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    stop = startStandaloneAdoption()

    const tab = addStray() // another window's `addTab`, rehydrated here first …
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS / 2)
    useWorkspaceStore.getState().insertTab(tab.id, a.id) // … and its `insertTab`, rehydrated second
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 4)

    expect(ownersOf(tab.id)).toEqual([a.id])
    expect(unsorted()).toBeUndefined()
    expect(info).not.toHaveBeenCalled()
  })

  it('does not loop: one adoption is one write, and leaves no timer behind', () => {
    useWorkspaceStore.getState().addWorkspace('A')
    stop = startStandaloneAdoption()
    const writes = vi.fn()
    const unsubscribe = useWorkspaceStore.subscribe(writes)

    addStray()
    addStray()
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)
    expect(writes).toHaveBeenCalledTimes(1)
    expect(armed()).toBe(1)

    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 100)
    expect(writes).toHaveBeenCalledTimes(1)
    expect(info).toHaveBeenCalledTimes(1)
    expect(armed()).toBe(1) // the adoption's own write armed nothing
    unsubscribe()
  })

  it('a tab that gets its workspace in the same tick costs one look and no write; a change with nothing to do arms nothing', () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    stop = startStandaloneAdoption()
    const before = useWorkspaceStore.getState().workspaces
    useTabStore.getState().setActiveTab(null)
    expect(armed()).toBe(0)

    const tab = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(tab) // ownerless until the next line: that is what the delay is for
    useWorkspaceStore.getState().insertTab(tab.id, a.id)
    const afterInsert = useWorkspaceStore.getState().workspaces
    expect(afterInsert).not.toBe(before)
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)
    expect(useWorkspaceStore.getState().workspaces).toBe(afterInsert)

    useTabStore.getState().setActiveTab(tab.id)
    expect(armed()).toBe(1)
    expect(info).not.toHaveBeenCalled()
  })

  it('an Unsorted made elsewhere (another language, other tabs in it) is reused as it is: found by id, never renamed', () => {
    useWorkspaceStore.setState({ workspaces: [ws('aaaaaa', 'A'), ws(UNSORTED_WORKSPACE_ID, '未分類', ['kept'])], activeWorkspaceId: 'aaaaaa' })
    stop = startStandaloneAdoption()

    const stray = addStray()
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)

    const { workspaces } = useWorkspaceStore.getState()
    expect(workspaces.map((w) => w.id)).toEqual(['aaaaaa', UNSORTED_WORKSPACE_ID])
    expect(unsorted()).toMatchObject({ name: '未分類', tabs: ['kept', stray.id] })
  })

  it('names a NEW Unsorted in the language of this device', () => {
    const t = vi.spyOn(useI18nStore.getState(), 't').mockImplementation((key: string) => (key === 'workspace.unsorted' ? '未分類' : key))
    addStray()
    stop = startStandaloneAdoption()
    expect(unsorted()?.name).toBe('未分類')
    t.mockRestore()
  })

  it('stop() ends it', () => {
    stop = startStandaloneAdoption()
    stop()
    const stray = addStray()
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 2)
    expect(ownersOf(stray.id)).toEqual([])
    expect(armed()).toBe(0)
  })
})

describe('only the world on screen, and only while it is settled', () => {
  it('another window is half-way through a switch (tabs of one world, workspaces of another): nothing is adopted until it settles', () => {
    useWorkspaceStore.getState().addWorkspace('A')
    stop = startStandaloneAdoption()

    // The slave's tabs have arrived; the workspaces and the pointer have not.
    const slaveTab = createTab({ kind: 'new-tab' })
    useTabStore.setState({ tabs: { [slaveTab.id]: slaveTab }, tabOrder: [slaveTab.id], worldId: 'slave1', worldEpoch: 7 })
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 4)
    expect(ownersOf(slaveTab.id)).toEqual([])
    expect(unsorted()).toBeUndefined()

    // The rest arrives: the slave's own workspace owns the tab. Nothing was ever adopted.
    useWorkspaceStore.setState({ workspaces: [ws('ssssss', 'S', [slaveTab.id])], activeWorkspaceId: 'ssssss', worldId: 'slave1', worldEpoch: 7 })
    useLocalProfilesStore.setState({
      slaves: { slave1: { id: 'slave1', name: 'S', createdAt: 1, world: null } }, slaveOrder: ['slave1'], activeProfileId: 'slave1', worldEpoch: 7,
      parkedMaster: { workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null },
    })
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 4)
    expect(ownersOf(slaveTab.id)).toEqual(['ssssss'])
    expect(unsorted()).toBeUndefined()
    expect(info).not.toHaveBeenCalled()
  })

  it('unsettled → no adoption; settled again → the tab that is STILL without a workspace is adopted', () => {
    useWorkspaceStore.getState().addWorkspace('A')
    stop = startStandaloneAdoption()

    useTabStore.setState({ worldEpoch: 9 }) // the tab store is ahead of the other two
    const stray = addStray()
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 4)
    expect(ownersOf(stray.id)).toEqual([])

    useWorkspaceStore.setState({ worldEpoch: 9 })
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 4)
    expect(ownersOf(stray.id)).toEqual([])
    useLocalProfilesStore.setState({ worldEpoch: 9 }) // the LAST store to arrive is neither of the two tab stores
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)
    expect(ownersOf(stray.id)).toEqual([UNSORTED_WORKSPACE_ID])
  })

  it('unsettled at start: nothing is adopted, and nothing is re-pointed', () => {
    useWorkspaceStore.getState().addWorkspace('A')
    useWorkspaceStore.getState().setActiveWorkspace(null)
    const stray = addStray()
    localStorage.setItem(STORAGE_KEYS.WORLD_EPOCH, '5') // this window is behind the fence
    stop = startStandaloneAdoption()
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 4)
    expect(ownersOf(stray.id)).toEqual([])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('a slave on screen (settled) is adopted into like any other world', () => {
    useTabStore.setState({ worldId: 'slave1', worldEpoch: 3 })
    useWorkspaceStore.setState({ worldId: 'slave1', worldEpoch: 3 })
    useLocalProfilesStore.setState({
      slaves: { slave1: { id: 'slave1', name: 'S', createdAt: 1, world: null } }, slaveOrder: ['slave1'], activeProfileId: 'slave1', worldEpoch: 3,
      parkedMaster: { workspaces: [ws('mmmmmm', 'M')], tabs: {}, activeWorkspaceId: 'mmmmmm', activeTabId: null },
    })
    const stray = addStray()
    stop = startStandaloneAdoption()
    expect(ownersOf(stray.id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(useLocalProfilesStore.getState().parkedMaster?.workspaces.map((w) => w.id)).toEqual(['mmmmmm'])
  })
})

describe('device state (lives until P4b): what it restores without a workspace is adopted', () => {
  const snapshot = (tabs: Tab[], workspaces: Workspace[]): WorkspaceSnapshot => ({
    version: 1, capturedAt: 1, sessionMeta: {},
    tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), tabOrder: tabs.map((t) => t.id), activeTabId: null,
    workspaces, activeWorkspaceId: workspaces[0]?.id ?? null,
  })
  const deps = { now: 1, buildSnapshotFn: async () => snapshot([], []) }

  it('replace', async () => {
    useWorkspaceStore.getState().addWorkspace('Old')
    stop = startStandaloneAdoption()
    const owned = createTab({ kind: 'new-tab' })
    const stray = createTab({ kind: 'new-tab' })

    await restoreDeviceStateReplace(snapshot([owned, stray], [ws('wwwwww', 'W', [owned.id])]), deps)
    expect(ownersOf(stray.id)).toEqual([])
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)

    expect(ownersOf(owned.id)).toEqual(['wwwwww'])
    expect(ownersOf(stray.id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual(['wwwwww', UNSORTED_WORKSPACE_ID])
  })

  it('merge (incoming tabs get fresh ids)', async () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    stop = startStandaloneAdoption()
    const stray = createTab({ kind: 'dashboard' }) // a merge skips tabs that are nothing but a new-tab page

    const report = await restoreDeviceStateMerge(snapshot([stray], []), deps)
    expect(report.addedTabs).toBe(1)
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)

    const { tabOrder } = useTabStore.getState()
    expect(tabOrder).toHaveLength(1)
    expect(ownersOf(tabOrder[0])).toEqual([UNSORTED_WORKSPACE_ID])
    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual([a.id, UNSORTED_WORKSPACE_ID])
  })
})

describe('two windows adopt at once', () => {
  it('both make the SAME workspace id, so after the rehydrate there is one Unsorted, not two', async () => {
    vi.useRealTimers()
    vi.stubGlobal('BroadcastChannel', undefined) // the two "windows" are one process here; they meet in localStorage only
    const stray = createTab({ kind: 'new-tab' })
    const seed = (key: string, state: unknown) => localStorage.setItem(key, JSON.stringify({ state, version: 1 }))
    seed(STORAGE_KEYS.TABS, { tabs: { [stray.id]: stray }, tabOrder: [stray.id], activeTabId: stray.id, worldId: 'master', worldEpoch: 0 })
    seed(STORAGE_KEYS.WORKSPACES, { workspaces: [ws('aaaaaa', 'A')], activeWorkspaceId: 'aaaaaa', worldId: 'master', worldEpoch: 0 })

    const openWindow = async () => {
      vi.resetModules()
      const store = await import('../store')
      const adoption = await import('./adopt-standalone')
      await store.useWorkspaceStore.persist.rehydrate()
      return { store: store.useWorkspaceStore, start: adoption.startStandaloneAdoption }
    }
    const w1 = await openWindow()
    const w2 = await openWindow()
    expect(w1.store).not.toBe(w2.store)
    expect(w2.store.getState().workspaces.map((w) => w.id)).toEqual(['aaaaaa']) // loaded before anyone adopted

    const stop1 = w1.start()
    const stop2 = w2.start() // its memory has not seen window 1's Unsorted
    const ids = (s: typeof w1.store) => s.getState().workspaces.map((w) => w.id)
    expect(ids(w1.store)).toEqual(['aaaaaa', UNSORTED_WORKSPACE_ID])
    expect(ids(w2.store)).toEqual(['aaaaaa', UNSORTED_WORKSPACE_ID])

    await w1.store.persist.rehydrate()
    await w2.store.persist.rehydrate()
    for (const s of [w1.store, w2.store]) {
      expect(ids(s)).toEqual(['aaaaaa', UNSORTED_WORKSPACE_ID])
      expect(s.getState().workspaces[1].tabs).toEqual([stray.id])
    }
    stop1()
    stop2()
  })
})
