// The way back from the watcher area (journal v5 §3.7, U11): the back button and ⌘W both call `leaveWatchArea`, which
// returns to the workspace AND the tab `openWatchArea` took when the area was entered.
import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore, WATCH_WORKSPACE_ID, __resetReturnWorkspaceForTest, getWatchReturnTarget } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { usePoolStore } from '../../../lib/pool/store'
import { usePoolHostsStore } from '../../../stores/usePoolHostsStore'
import { createTab, createWorkspace } from '../../../types/tab'
import { WATCH_GUARDIAN_TAB_ID as G, watchHostTabId } from '../../../lib/watch/ids'
import { commitTabWorld, restampWorld } from '../../../lib/profile/master-world'
import { openWatchArea, leaveWatchArea } from './open-watch-area'

const ws = () => useWorkspaceStore.getState()
const wsOf = (id: string) => ws().workspaces.find((w) => w.id === id)!
const at = () => ({ ws: ws().activeWorkspaceId, tab: useTabStore.getState().activeTabId })

function setPool(ids: string[]): void {
  usePoolStore.setState({ hosts: ids.map((id) => ({ host_id: id, alias: id })) as never, sourceHostId: 'cfg-a' } as never)
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
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [], worldId: 'master', worldEpoch: 0 })
  usePoolStore.setState({ hosts: [], sourceHostId: null } as never)
  usePoolHostsStore.setState({ order: [], looks: {} } as never)
  __resetReturnWorkspaceForTest()
  a = ws().addWorkspace('A').id
  b = ws().addWorkspace('B').id
  t1 = tabIn(a)
  t2 = tabIn(a)
  t3 = tabIn(b)
  select(b, t3)
  select(a, t1)
  setPool(['h1', 'h2'])
})

describe('leaveWatchArea', () => {
  it('returns to the workspace and the tab the area was entered from', () => {
    openWatchArea({ poolHost: { hostId: 'h1' } })
    expect(ws().activeWorkspaceId).toBe(WATCH_WORKSPACE_ID)
    // Meanwhile A's own active tab moves (a synced workspace from another device): the way back is still t1.
    ws().setWorkspaceActiveTab(a, t2)
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t1 })
    expect(wsOf(a).activeTabId).toBe(t1)
  })

  it('the target is the one taken on entry: tabs switched and the area opened again from inside do not change it', () => {
    openWatchArea({ poolHost: { hostId: 'h1' } })
    openWatchArea({ poolHost: { hostId: 'h2' } })
    select(WATCH_WORKSPACE_ID, G)
    openWatchArea({ tab: 'guardian' })
    expect(getWatchReturnTarget()).toEqual({ workspaceId: a, tabId: t1 })
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t1 })
  })

  it('a new entry from elsewhere takes a new target', () => {
    openWatchArea({ poolHost: { hostId: 'h1' } })
    leaveWatchArea()
    select(b, t3)
    openWatchArea({ poolHost: { hostId: 'h2' } })
    leaveWatchArea()
    expect(at()).toEqual({ ws: b, tab: t3 })
  })

  it('the tab gone on return: the workspace\'s active tab', () => {
    openWatchArea({ poolHost: { hostId: 'h1' } })
    dropTab(a, t1)
    ws().setWorkspaceActiveTab(a, t2)
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t2 })
  })

  it('the tab moved to another workspace is gone from this one: the workspace\'s active tab, the workspace kept', () => {
    openWatchArea({ poolHost: { hostId: 'h1' } })
    ws().removeTabFromWorkspace(a, t1)
    ws().addTabToWorkspace(b, t1)
    ws().setWorkspaceActiveTab(a, t2)
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t2 })
  })

  it('the workspace gone on return: the return workspace (the first other one) and its active tab', () => {
    openWatchArea({ poolHost: { hostId: 'h1' } })
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
    openWatchArea({ poolHost: { hostId: 'h1' } })
    expect(getWatchReturnTarget()).toEqual({ workspaceId: a, tabId: t1 })
    const before = wsOf(a)
    ws().reset()
    expect(getWatchReturnTarget()).toBeNull()
    // The world that comes in reuses the id (a tear-off / merge keeps `workspace.id`), with another active tab.
    ws().importWorkspace({ ...before, activeTabId: t2 })
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t2 })
  })

  // Another world (a profile switch: `commitTabWorld` with a new tag) has workspaces of its own ids: the way back names
  // none of them, so a leave takes the fallback — checked on leaving, not by clearing on the switch.
  function anotherWorld(): { c1: string; u1: string } {
    const u1 = createTab({ kind: 'new-tab' })
    const c1 = { ...createWorkspace('C1'), tabs: [u1.id], activeTabId: u1.id }
    const area = ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID) ?? { ...createWorkspace('watch'), id: WATCH_WORKSPACE_ID }
    const all = useTabStore.getState().tabs
    const areaTabs = Object.fromEntries(area.tabs.map((id) => [id, all[id]]))
    commitTabWorld({
      tabs: { ...areaTabs, [u1.id]: u1 },
      workspaces: [c1, area],
      activeWorkspaceId: WATCH_WORKSPACE_ID,
      activeTabId: area.tabs[0] ?? null,
    }, undefined, { worldId: 'p2', worldEpoch: 7 })
    expect(ws().worldId).toBe('p2')
    expect(ws().activeWorkspaceId).toBe(WATCH_WORKSPACE_ID)
    return { c1: c1.id, u1: u1.id }
  }

  it('another world while the area is shown: a leave takes the fallback, the new world\'s first workspace', () => {
    openWatchArea({ poolHost: { hostId: 'h1' } })
    const { c1, u1 } = anotherWorld()
    leaveWatchArea()
    expect(at()).toEqual({ ws: c1, tab: u1 })
  })

  it('another world that lands on the area: a leave takes the fallback, not a workspace of the old world', () => {
    const { c1, u1 } = anotherWorld()
    leaveWatchArea()
    expect(at()).toEqual({ ws: c1, tab: u1 })
  })

  it('a restamp (a promote: `restampWorld` moves the tag, the content stays) keeps the way back', () => {
    openWatchArea({ poolHost: { hostId: 'h1' } })
    ws().setWorkspaceActiveTab(a, t2) // A's own active tab moved meanwhile (a sync): only the way back still says t1
    restampWorld({ worldId: 'p2', worldEpoch: 7 })
    expect(ws().worldId).toBe('p2')
    expect(getWatchReturnTarget()).toEqual({ workspaceId: a, tabId: t1 })
    leaveWatchArea()
    expect(at()).toEqual({ ws: a, tab: t1 })
  })

  it('leaves the area\'s tabs as they are', () => {
    openWatchArea({ poolHost: { hostId: 'h1' } })
    const tabs = [...wsOf(WATCH_WORKSPACE_ID).tabs]
    leaveWatchArea()
    expect(wsOf(WATCH_WORKSPACE_ID).tabs).toEqual(tabs)
    expect(tabs).toEqual([G, watchHostTabId('h1'), watchHostTabId('h2')])
  })
})
