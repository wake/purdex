// spa/src/features/workspace/lib/adopt-standalone.test.ts — "every tab belongs to exactly one workspace" as a
// STANDING invariant (Profile Sync spec §4.3; P3 plan, P3c-1): whatever puts a tab in `tabOrder` without a
// workspace — device-state's restore and merge, a producer nobody found — the tab ends up in `Unsorted`.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useTabStore } from '../../../stores/useTabStore'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { restoreDeviceStateMerge, restoreDeviceStateReplace } from '../../../lib/device-state/restore'
import { replaceTabSnapshot } from '../../../lib/snapshot/restore'
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

/** Starts it and lets the first look happen: there is NO synchronous look at start (see the race in 'at start'). */
function boot(): void {
  stop = startStandaloneAdoption()
  vi.advanceTimersByTime(ADOPTION_SETTLE_MS)
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

describe('at start', () => {
  it('adopts the tabs persisted without a workspace into a new Unsorted, in tab order — after the settle delay, not before', () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    const owned = addStray()
    useWorkspaceStore.getState().addTabToWorkspace(a.id, owned.id)
    const s1 = addStray()
    const s2 = addStray()

    stop = startStandaloneAdoption()
    expect(unsorted()).toBeUndefined() // what storage held a moment ago may be half of another window's write
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS - 1)
    expect(unsorted()).toBeUndefined()
    vi.advanceTimersByTime(1)

    expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual([a.id, UNSORTED_WORKSPACE_ID])
    expect(unsorted()).toMatchObject({ name: 'Unsorted', tabs: [s1.id, s2.id] })
    expect(ownersOf(owned.id)).toEqual([a.id])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(a.id)
  })

  it('no workspace at all: Unsorted is created and becomes the active workspace', () => {
    const s1 = addStray()
    boot()
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
    boot()
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
    // Re-pointing is a write of the whole persisted workspace store, too: it waits like an adoption does.
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(b.id)

    stop()
    useTabStore.getState().setActiveTab(null)
    useWorkspaceStore.getState().setActiveWorkspace(null)
    boot()
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
    // The stray is the tab on screen (`addTab` focuses the first tab), so the pointer goes where it went
    // (P3c-2 review, F3; was: stays on A — with the tab on screen in no bar). A stray that is NOT on screen
    // leaves the pointer alone: see the last describe.
    expect(useTabStore.getState().activeTabId).toBe(stray.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(UNSORTED_WORKSPACE_ID)
    expect(a.id).not.toBe(UNSORTED_WORKSPACE_ID)
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

  it('does not loop: one adoption is one write, and its own write arms nothing', () => {
    useWorkspaceStore.getState().addWorkspace('A')
    stop = startStandaloneAdoption()
    const writes = vi.fn()
    const unsubscribe = useWorkspaceStore.subscribe(writes)

    addStray()
    addStray()
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)
    expect(writes).toHaveBeenCalledTimes(1)
    expect(armed()).toBe(2) // once per stray: the second re-armed it

    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 100)
    expect(writes).toHaveBeenCalledTimes(1)
    expect(info).toHaveBeenCalledTimes(1)
    expect(armed()).toBe(2) // the adoption's own write armed nothing
    unsubscribe()
  })

  it('a tab that gets its workspace in the same tick is never looked at; a change that is not about membership arms nothing', () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    stop = startStandaloneAdoption()
    useTabStore.getState().setActiveTab(null)
    expect(armed()).toBe(0)

    const tab = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(tab) // ownerless until the next line: armed …
    useWorkspaceStore.getState().insertTab(tab.id, a.id) // … and disarmed
    const afterInsert = useWorkspaceStore.getState().workspaces
    const looks = vi.spyOn(useLocalProfilesStore, 'getState') // `readMasterWorld` is the first thing a look does
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 2)
    expect(looks).not.toHaveBeenCalled()
    expect(useWorkspaceStore.getState().workspaces).toBe(afterInsert)

    useTabStore.getState().setActiveTab(tab.id)
    expect(armed()).toBe(1)
    expect(info).not.toHaveBeenCalled()
  })

  // Review F1: the delay is a DEBOUNCE. As a throttle, the timer armed for the first stray fired 10 ms after another
  // window's `addTab` had been rehydrated here, adopted that tab — and the workspace that owns it arrived afterwards.
  it('every membership change pushes the look back: a tab that arrives just before the first deadline is not adopted at it', () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    stop = startStandaloneAdoption()

    const stray = addStray() // t = 0: a real stray
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS - 10)
    const theirs = addStray() // t = 490: another window's `addTab`, rehydrated here
    vi.advanceTimersByTime(20) // t = 510: past the FIRST deadline
    expect(ownersOf(theirs.id)).toEqual([])
    expect(ownersOf(stray.id)).toEqual([])
    useWorkspaceStore.getState().insertTab(theirs.id, a.id) // … and its `insertTab`
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS - 1)
    expect(ownersOf(stray.id)).toEqual([])
    vi.advanceTimersByTime(1)

    expect(ownersOf(stray.id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(ownersOf(theirs.id)).toEqual([a.id])
  })

  it('tab-store writes that change no membership (a busy agent rewrites pane records all the time) do not push the look back', () => {
    useWorkspaceStore.getState().addWorkspace('A')
    stop = startStandaloneAdoption()
    const stray = addStray()
    for (let t = 0; t < ADOPTION_SETTLE_MS; t += 100) {
      vi.advanceTimersByTime(100)
      if (t + 100 < ADOPTION_SETTLE_MS) useTabStore.setState({ tabs: { ...useTabStore.getState().tabs } })
    }
    expect(ownersOf(stray.id)).toEqual([UNSORTED_WORKSPACE_ID])
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
    boot()
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
    boot()
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

// The two "windows" are two instances of the modules (`vi.resetModules()`); they meet in localStorage only.
describe('two windows', () => {
  const seed = (key: string, state: unknown) => localStorage.setItem(key, JSON.stringify({ state, version: 1 }))
  const seedTabs = (tabs: Tab[]) => seed(STORAGE_KEYS.TABS, { tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), tabOrder: tabs.map((t) => t.id), activeTabId: null, worldId: 'master', worldEpoch: 0 })
  const seedWorkspaces = (workspaces: Workspace[]) => seed(STORAGE_KEYS.WORKSPACES, { workspaces, activeWorkspaceId: workspaces[0]?.id ?? null, worldId: 'master', worldEpoch: 0 })
  const stored = (): Workspace[] => JSON.parse(localStorage.getItem(STORAGE_KEYS.WORKSPACES)!).state.workspaces

  const openWindow = async () => {
    vi.resetModules()
    const store = await import('../store')
    const adoption = await import('./adopt-standalone')
    return { store: store.useWorkspaceStore, start: adoption.startStandaloneAdoption }
  }
  type Win = Awaited<ReturnType<typeof openWindow>>
  const ids = (w: Win) => w.store.getState().workspaces.map((x) => x.id)

  beforeEach(() => {
    vi.stubGlobal('BroadcastChannel', undefined)
  })

  it('both adopt at once: they make the SAME workspace id, so after the rehydrate there is one Unsorted, not two', async () => {
    const stray = createTab({ kind: 'new-tab' })
    seedTabs([stray])
    seedWorkspaces([ws('aaaaaa', 'A')])
    const w1 = await openWindow()
    const w2 = await openWindow()
    expect(w1.store).not.toBe(w2.store)

    const stops = [w1.start(), w2.start()]
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS) // window 2's memory has not seen window 1's Unsorted
    expect(ids(w1)).toEqual(['aaaaaa', UNSORTED_WORKSPACE_ID])
    expect(ids(w2)).toEqual(['aaaaaa', UNSORTED_WORKSPACE_ID])

    await w1.store.persist.rehydrate()
    await w2.store.persist.rehydrate()
    for (const w of [w1, w2]) {
      expect(ids(w)).toEqual(['aaaaaa', UNSORTED_WORKSPACE_ID])
      expect(w.store.getState().workspaces[1].tabs).toEqual([stray.id])
    }
    stops.forEach((off) => off())
  })

  // Review F2: window A has persisted `addTab`; its `insertTab` has not landed when window B starts.
  it('a window that starts between another window\'s two writes adopts nothing, and does not write its old workspaces over them', async () => {
    const theirs = createTab({ kind: 'new-tab' })
    seedTabs([theirs]) // A's first write
    seedWorkspaces([ws('aaaaaa', 'A')]) // … the second is not there yet
    const b = await openWindow()
    const stopB = b.start()
    expect(ids(b)).toEqual(['aaaaaa'])

    vi.advanceTimersByTime(ADOPTION_SETTLE_MS / 2)
    seedWorkspaces([ws('aaaaaa', 'A', [theirs.id])]) // A's second write lands, and B is told
    await b.store.persist.rehydrate()
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 4)

    expect(ids(b)).toEqual(['aaaaaa'])
    expect(stored()).toEqual([ws('aaaaaa', 'A', [theirs.id])])
    expect(info).not.toHaveBeenCalled()
    stopB()
  })

  it('both de-duplicate the same two-owner world: the same result in both, and no ping-pong afterwards', async () => {
    const t = createTab({ kind: 'new-tab' })
    seedTabs([t])
    seedWorkspaces([ws('aaaaaa', 'A', [t.id]), ws('bbbbbb', 'B', [t.id])])
    const w1 = await openWindow()
    const w2 = await openWindow()
    const stops = [w1.start(), w2.start()]
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)

    const expected = [ws('aaaaaa', 'A', [t.id]), { ...ws('bbbbbb', 'B'), activeTabId: null }]
    expect(w1.store.getState().workspaces).toEqual(expected)
    expect(w2.store.getState().workspaces).toEqual(expected)

    const before = timeouts.mock.calls.length
    await w1.store.persist.rehydrate()
    await w2.store.persist.rehydrate()
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 4)
    expect(stored()).toEqual(expected)
    expect(timeouts.mock.calls.slice(before).filter((call: unknown[]) => call[1] === ADOPTION_SETTLE_MS)).toEqual([])
    stops.forEach((off) => off())
  })
})

// Review F3: "exactly one" is also "not two". Old storage, a snapshot restore and cross-window last-write-wins can all
// list one tab in two workspaces; the tab bar then renders it twice while `tabOrder` has it once.
describe('a tab in more than one workspace', () => {
  const twoOwners = (tabId: string): Workspace[] => [ws('aaaaaa', 'A', ['a1', tabId]), ws('bbbbbb', 'B', [tabId, 'b1']), ws('cccccc', 'C', [tabId])]

  it('at start: the FIRST workspace in workspace order keeps it; a workspace that loses its active tab gets `null` (removeTabFromWorkspace\'s rule)', () => {
    const t = addStray()
    useWorkspaceStore.setState({ workspaces: twoOwners(t.id), activeWorkspaceId: 'bbbbbb' })
    boot()
    const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
    expect(workspaces.map((w) => w.tabs)).toEqual([['a1', t.id], ['b1'], []])
    expect(workspaces.map((w) => w.activeTabId)).toEqual(['a1', null, null])
    // The tab is on screen and the ACTIVE workspace is the one that lost it: the pointer follows it to the
    // owner that keeps it (P3c-2 review, F3; was: stays on 'bbbbbb', whose bar no longer has the tab).
    expect(useTabStore.getState().activeTabId).toBe(t.id)
    expect(activeWorkspaceId).toBe('aaaaaa')
    expect(unsorted()).toBeUndefined()
  })

  it('the same id twice in ONE workspace: the first stays', () => {
    const t = addStray()
    useWorkspaceStore.setState({ workspaces: [ws('aaaaaa', 'A', [t.id, 'a1', t.id])], activeWorkspaceId: 'aaaaaa' })
    boot()
    expect(useWorkspaceStore.getState().workspaces[0]).toMatchObject({ tabs: [t.id, 'a1'], activeTabId: t.id })
  })

  it('after a rehydrate / a snapshot restore: converges after the settle delay, with one log line that has the count and no tab id', () => {
    useWorkspaceStore.getState().addWorkspace('Old')
    stop = startStandaloneAdoption()
    const t = createTab({ kind: 'new-tab' })
    replaceTabSnapshot({
      version: 1, capturedAt: 1, sessionMeta: {}, tabs: { [t.id]: t }, tabOrder: [t.id], activeTabId: null,
      workspaces: [ws('aaaaaa', 'A', [t.id]), ws('bbbbbb', 'B', [t.id])], activeWorkspaceId: 'aaaaaa',
    })
    expect(ownersOf(t.id)).toEqual(['aaaaaa', 'bbbbbb'])
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)
    expect(ownersOf(t.id)).toEqual(['aaaaaa'])
    expect(info).toHaveBeenCalledTimes(1)
    const line = info.mock.calls[0].map(String).join(' ')
    expect(line).toContain('1')
    expect(line).not.toContain(t.id)
  })

  it('together with a stray: one write does both', () => {
    const t = addStray()
    const stray = addStray()
    useWorkspaceStore.setState({ workspaces: [ws('aaaaaa', 'A', [t.id]), ws('bbbbbb', 'B', [t.id])], activeWorkspaceId: 'aaaaaa' })
    stop = startStandaloneAdoption()
    const writes = vi.fn()
    const unsubscribe = useWorkspaceStore.subscribe(writes)
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 10)
    expect(writes).toHaveBeenCalledTimes(1)
    expect(ownersOf(t.id)).toEqual(['aaaaaa'])
    expect(ownersOf(stray.id)).toEqual([UNSORTED_WORKSPACE_ID])
    unsubscribe()
  })

  it('not while the world is unsettled: the two owners may be two worlds', () => {
    const t = addStray()
    useWorkspaceStore.setState({ workspaces: [ws('aaaaaa', 'A', [t.id]), ws('bbbbbb', 'B', [t.id])], activeWorkspaceId: 'aaaaaa' })
    useTabStore.setState({ worldEpoch: 9 })
    boot()
    expect(ownersOf(t.id)).toEqual(['aaaaaa', 'bbbbbb'])
    useWorkspaceStore.setState({ worldEpoch: 9 })
    useLocalProfilesStore.setState({ worldEpoch: 9 })
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)
    expect(ownersOf(t.id)).toEqual(['aaaaaa'])
  })
})

// P3c-2 review, F3. A click on a notification (or on anything else) can put a tab nobody has adopted yet on
// screen while `activeWorkspaceId` points at some workspace. Adoption then files the tab under Unsorted — and if
// the pointer stayed, the tab on screen would be in no bar, for good. The pointer follows the tab on screen only
// when THIS repair moved it; a pointer the user chose is otherwise never touched.
describe('the pointer follows the tab on screen — only when this repair moved that tab', () => {
  it('the adopted tab is the active tab → activeWorkspaceId becomes the workspace that adopted it, in the same write', () => {
    const a = addStray()
    const wsA = useWorkspaceStore.getState().addWorkspace('A')
    useWorkspaceStore.getState().addTabToWorkspace(wsA.id, a.id)
    useWorkspaceStore.getState().setActiveWorkspace(wsA.id)
    boot()
    const orphan = addStray()
    useTabStore.getState().setActiveTab(orphan.id) // what handleNotificationClick / handleSelectTab do
    const writes = vi.fn()
    const off = useWorkspaceStore.subscribe(writes)
    const before = armed()

    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)

    expect(ownersOf(orphan.id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(UNSORTED_WORKSPACE_ID)
    expect(writes).toHaveBeenCalledTimes(1)
    // No loop: the write changes the signature, finds nothing to do, arms nothing — now or later.
    vi.advanceTimersByTime(ADOPTION_SETTLE_MS * 4)
    expect(armed()).toBe(before)
    expect(writes).toHaveBeenCalledTimes(1)
    off()
  })

  it('the adopted tab is NOT the active tab → the pointer does not move', () => {
    const a = addStray()
    const wsA = useWorkspaceStore.getState().addWorkspace('A')
    useWorkspaceStore.getState().addTabToWorkspace(wsA.id, a.id)
    useWorkspaceStore.getState().setActiveWorkspace(wsA.id)
    useTabStore.getState().setActiveTab(a.id)
    boot()
    const orphan = addStray()

    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)

    expect(ownersOf(orphan.id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(wsA.id)
  })

  it('a pointer the user chose is not "aligned": active tab in A, the user looks at B, a stray is adopted → still B', () => {
    const [a, b] = [addStray(), addStray()]
    const wsA = useWorkspaceStore.getState().addWorkspace('A')
    const wsB = useWorkspaceStore.getState().addWorkspace('B')
    useWorkspaceStore.getState().addTabToWorkspace(wsA.id, a.id)
    useWorkspaceStore.getState().addTabToWorkspace(wsB.id, b.id)
    useTabStore.getState().setActiveTab(a.id)
    useWorkspaceStore.getState().setActiveWorkspace(wsB.id)
    boot()
    const orphan = addStray()

    vi.advanceTimersByTime(ADOPTION_SETTLE_MS)

    expect(ownersOf(orphan.id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(wsB.id)
  })

  it('de-duplication takes the active tab out of the ACTIVE workspace → the pointer follows it to the owner that keeps it', () => {
    const t = addStray()
    useTabStore.getState().setActiveTab(t.id)
    useWorkspaceStore.setState({ workspaces: [ws('w1', 'One', [t.id]), ws('w2', 'Two', [t.id])], activeWorkspaceId: 'w2' })
    boot()

    expect(ownersOf(t.id)).toEqual(['w1'])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('w1')
  })

  it('de-duplication that leaves the active workspace\'s listing alone → the pointer does not move', () => {
    const [t, c] = [addStray(), addStray()]
    useTabStore.getState().setActiveTab(t.id)
    // The user looks at w3; the active tab is listed in w1 and w2, and w2 loses it. Nobody asked for w1.
    useWorkspaceStore.setState({ workspaces: [ws('w1', 'One', [t.id]), ws('w2', 'Two', [t.id]), ws('w3', 'Three', [c.id])], activeWorkspaceId: 'w3' })
    boot()

    expect(ownersOf(t.id)).toEqual(['w1'])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('w3')
  })
})
