// The way back from the watcher area (journal v5 §3.7, U11): the back button and ⌘W both call `leaveWatchArea`, which
// returns to the workspace AND the tab `openWatchArea` took when the area was entered.
import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore, WATCH_WORKSPACE_ID, __resetReturnWorkspaceForTest, getWatchReturnTarget } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { useFleetStore, type FleetSnapshot } from '../../../lib/fleet/store'
import { createTab } from '../../../types/tab'
import { WATCH_GUARDIAN_TAB_ID as G, watchHostTabId } from '../../../lib/watch/ids'
import { openWatchArea, leaveWatchArea } from './open-watch-area'

const ws = () => useWorkspaceStore.getState()
const wsOf = (id: string) => ws().workspaces.find((w) => w.id === id)!
const at = () => ({ ws: ws().activeWorkspaceId, tab: useTabStore.getState().activeTabId })

function setFleet(ids: string[]): void {
  const snapshot: FleetSnapshot = {
    hostId: 'p', term: 1, order: ids,
    hosts: Object.fromEntries(ids.map((id) => [id, { id, alias: id } as never])),
  }
  useFleetStore.setState({ snapshot, primaryHostId: 'p', stale: false })
}

function tabIn(wsId: string): string {
  const tab = createTab({ kind: 'new-tab' })
  useTabStore.getState().addTab(tab)
  ws().insertTab(tab.id, wsId)
  return tab.id
}

/** What a click on a tab does (`handleSelectTab`). */
function select(wsId: string, tabId: string): void {
  useTabStore.getState().setActiveTab(tabId)
  ws().setActiveWorkspace(wsId)
  ws().setWorkspaceActiveTab(wsId, tabId)
}

/** A tab closed elsewhere (another window): gone from the tab store and its workspace. */
function dropTab(wsId: string, tabId: string): void {
  ws().removeTabFromWorkspace(wsId, tabId)
  useTabStore.setState((s) => {
    const { [tabId]: _gone, ...tabs } = s.tabs
    return { tabs, tabOrder: s.tabOrder.filter((id) => id !== tabId) }
  })
}

let a: string
let b: string
let t1: string
let t2: string
let t3: string
beforeEach(() => {
  ws().reset()
  useWorkspaceStore.setState({ worldId: 'master', worldEpoch: 0 }) // the world tests below move the tag
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useFleetStore.getState().reset()
  __resetReturnWorkspaceForTest()
  a = ws().addWorkspace('A').id
  b = ws().addWorkspace('B').id
  t1 = tabIn(a)
  t2 = tabIn(a)
  t3 = tabIn(b)
  select(b, t3)
  select(a, t1)
  setFleet(['h1', 'h2'])
})

describe('leaveWatchArea', () => {
  it('returns to the workspace and the tab the area was entered from', () => {
    openWatchArea({ host: 'h1' })
    expect(ws().activeWorkspaceId).toBe(WATCH_WORKSPACE_ID)
    // Meanwhile A's own active tab moves (a synced workspace from another device): the way back is still t1.
    ws().setWorkspaceActiveTab(a, t2)
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t1 })
    expect(wsOf(a).activeTabId).toBe(t1)
  })

  it('the target is the one taken on entry: tabs switched and the area opened again from inside do not change it', () => {
    openWatchArea({ host: 'h1' })
    openWatchArea({ host: 'h2' })
    select(WATCH_WORKSPACE_ID, G)
    openWatchArea({ tab: 'guardian' })
    expect(getWatchReturnTarget()).toEqual({ workspaceId: a, tabId: t1 })
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t1 })
  })

  it('a new entry from elsewhere takes a new target', () => {
    openWatchArea({ host: 'h1' })
    leaveWatchArea()
    select(b, t3)
    openWatchArea({ host: 'h2' })
    leaveWatchArea()
    expect(at()).toEqual({ ws: b, tab: t3 })
  })

  it('the tab gone on return: the workspace\'s active tab', () => {
    openWatchArea({ host: 'h1' })
    dropTab(a, t1)
    ws().setWorkspaceActiveTab(a, t2)
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t2 })
  })

  it('the tab moved to another workspace is gone from this one: the workspace\'s active tab, the workspace kept', () => {
    openWatchArea({ host: 'h1' })
    ws().removeTabFromWorkspace(a, t1)
    ws().addTabToWorkspace(b, t1)
    ws().setWorkspaceActiveTab(a, t2)
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t2 })
  })

  it('the workspace gone on return: the return workspace (the first other one) and its active tab', () => {
    openWatchArea({ host: 'h1' })
    ws().removeWorkspace(a)
    leaveWatchArea()
    expect(at()).toEqual({ ws: b, tab: t3 })
  })

  it('no target (a restart with the area shown): the first workspace and its active tab', () => {
    ws().ensureWatchWorkspace()
    ws().setActiveWorkspace(WATCH_WORKSPACE_ID)
    __resetReturnWorkspaceForTest()
    expect(getWatchReturnTarget()).toBeNull()
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t1 })
  })

  it('reset (a tear-off `replace`) clears the way back: a leave right after takes the fallback, not the old target', () => {
    openWatchArea({ host: 'h1' })
    expect(getWatchReturnTarget()).toEqual({ workspaceId: a, tabId: t1 })
    const before = wsOf(a)
    ws().reset()
    expect(getWatchReturnTarget()).toBeNull()
    // The world that comes in reuses the id (a tear-off / merge keeps `workspace.id`), with another active tab.
    ws().importWorkspace({ ...before, activeTabId: t2 })
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t2 })
  })

  it('another world (a profile switch writes its tag) clears the way back, also when the area stays shown', () => {
    openWatchArea({ host: 'h1' })
    // switch-active's `commitTabWorld`: the new world and its tag in one write; here it shows the area as well, and its
    // ids collide with the old world's (`unsorted` is one fixed id in every world).
    useWorkspaceStore.setState((s) => ({
      worldId: 'p2',
      worldEpoch: 7,
      workspaces: s.workspaces.map((w) => (w.id === a ? { ...w, activeTabId: t2 } : w)),
    }))
    expect(ws().activeWorkspaceId).toBe(WATCH_WORKSPACE_ID)
    expect(getWatchReturnTarget()).toBeNull()
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t2 })
  })

  it('a world switch that lands on the area takes no way back from the old world', () => {
    useWorkspaceStore.setState({ worldId: 'p2', worldEpoch: 7, activeWorkspaceId: WATCH_WORKSPACE_ID })
    expect(getWatchReturnTarget()).toBeNull()
  })

  it('leaves the area\'s tabs as they are', () => {
    openWatchArea({ host: 'h1' })
    const tabs = [...wsOf(WATCH_WORKSPACE_ID).tabs]
    leaveWatchArea()
    expect(wsOf(WATCH_WORKSPACE_ID).tabs).toEqual(tabs)
    expect(tabs).toEqual([G, watchHostTabId('h1'), watchHostTabId('h2')])
  })
})
