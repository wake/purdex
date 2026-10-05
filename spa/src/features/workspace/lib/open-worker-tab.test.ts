// spa/src/features/workspace/lib/open-worker-tab.test.ts — opening a worker (shell cleanup spec §4.4, rule B.2):
// a tab that already shows the worker is selected where it is, with its workspace; otherwise a new tab lands in
// the workspace on screen — not in `Unsorted` half a second later.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useTabStore } from '../../../stores/useTabStore'
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { createTab } from '../../../types/tab'
import { UNSORTED_WORKSPACE_ID, useWorkspaceStore } from '../store'
import { startStandaloneAdoption } from './adopt-standalone'
import { openWorkerTab } from './open-worker-tab'

const worker = (executionId: string) => ({ kind: 'execution' as const, executionId, host: 'host-a' })
const ownersOf = (tabId: string): string[] => useWorkspaceStore.getState().workspaces.filter((w) => w.tabs.includes(tabId)).map((w) => w.id)
const tabsOf = (wsId: string): string[] => useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!.tabs

let stop: () => void = () => {}

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
  vi.spyOn(console, 'info').mockImplementation(() => {})
})

afterEach(() => {
  stop()
  stop = () => {}
  settleWorld()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('openWorkerTab', () => {
  it('a tab already showing the worker in W2, W1 on screen: selects it, W2 becomes active, W2 keeps its tabs as they were', () => {
    const store = useWorkspaceStore.getState()
    const w1 = store.addWorkspace('W1')
    const w2 = store.addWorkspace('W2')
    const other = createTab({ kind: 'new-tab' })
    const existing = createTab(worker('exc_1'))
    useTabStore.getState().addTab(other)
    useTabStore.getState().addTab(existing)
    store.addTabToWorkspace(w2.id, existing.id)
    store.addTabToWorkspace(w2.id, other.id)
    store.setWorkspaceActiveTab(w2.id, other.id)
    store.setActiveWorkspace(w1.id)
    useTabStore.getState().setActiveTab(null)

    const id = openWorkerTab(worker('exc_1'))

    expect(id).toBe(existing.id)
    expect(useTabStore.getState().activeTabId).toBe(existing.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(w2.id)
    expect(tabsOf(w2.id)).toEqual([existing.id, other.id])
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === w2.id)!.activeTabId).toBe(existing.id)
    expect(tabsOf(w1.id)).toEqual([])
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(2)
  })

  it('a new worker, W1 on screen: the new tab goes into W1, W1 stays active, the tab is selected', () => {
    const store = useWorkspaceStore.getState()
    const w1 = store.addWorkspace('W1')
    const w2 = store.addWorkspace('W2')
    store.setActiveWorkspace(w1.id)

    const id = openWorkerTab(worker('exc_new'))

    expect(useTabStore.getState().tabs[id]).toBeDefined()
    expect(useTabStore.getState().activeTabId).toBe(id)
    expect(ownersOf(id)).toEqual([w1.id])
    expect(tabsOf(w2.id)).toEqual([])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(w1.id)
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === w1.id)!.activeTabId).toBe(id)
  })

  it('a new worker with Home on screen (no active workspace): the insertTab fallback takes it, and that workspace becomes active', () => {
    const store = useWorkspaceStore.getState()
    const w1 = store.addWorkspace('W1')
    store.addWorkspace('W2')
    store.setActiveWorkspace(null)

    const id = openWorkerTab(worker('exc_home'))

    expect(ownersOf(id)).toEqual([w1.id])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(w1.id)
    expect(useTabStore.getState().activeTabId).toBe(id)
  })

  it('a new worker with no workspace at all: Unsorted is made for it and becomes active', () => {
    const id = openWorkerTab(worker('exc_none'))

    expect(ownersOf(id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(UNSORTED_WORKSPACE_ID)
  })

  it('regression: with standalone adoption running, the new tab is still in W1 (not Unsorted) after 1000 ms', () => {
    const store = useWorkspaceStore.getState()
    const w1 = store.addWorkspace('W1')
    store.setActiveWorkspace(w1.id)
    stop = startStandaloneAdoption()

    const id = openWorkerTab(worker('exc_late'))
    vi.advanceTimersByTime(1000)

    expect(ownersOf(id)).toEqual([w1.id])
    expect(useWorkspaceStore.getState().workspaces.some((w) => w.id === UNSORTED_WORKSPACE_ID)).toBe(false)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(w1.id)
  })
})
