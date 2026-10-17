// The way back from the watcher area with two windows (journal v5 §3.7, WA-2 R2). Electron has one renderer per window,
// and the workspace store's `activeWorkspaceId` is persisted and synced: window A opening the area takes window B into
// it by a rehydrate, without B running `openWatchArea`. B's way back is still B's own — the workspace B showed just
// before, and the tab active in it. The back button and ⌘W both call `leaveWatchArea` (TabBar / useShortcuts tests).
//
// Each `openWindow()` is a fresh module graph (`vi.resetModules()`) over the same localStorage; the BroadcastChannel is
// off, so a window sees another's writes only when `syncInto` rehydrates it, as the sync manager does on a write.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createTab } from '../../../types/tab'

async function openWindow() {
  vi.resetModules()
  const [store, tabs, area, world] = await Promise.all([
    import('../store'),
    import('../../../stores/useTabStore'),
    import('./open-watch-area'),
    import('../../../lib/profile/master-world'),
  ])
  const ws = () => store.useWorkspaceStore.getState()
  return {
    store,
    tabs,
    area,
    world,
    ws,
    /** A new tab in `wsId`, made in this window. */
    tabIn(wsId: string): string {
      const tab = createTab({ kind: 'new-tab' })
      tabs.useTabStore.getState().addTab(tab)
      ws().insertTab(tab.id, wsId)
      return tab.id
    },
    /** What a click on a tab does in this window (`handleSelectTab`). */
    select(wsId: string, tabId: string): void {
      tabs.useTabStore.getState().setActiveTab(tabId)
      ws().setActiveWorkspace(wsId)
      ws().setWorkspaceActiveTab(wsId, tabId)
    },
    at: () => ({ ws: ws().activeWorkspaceId, tab: tabs.useTabStore.getState().activeTabId }),
  }
}
type Win = Awaited<ReturnType<typeof openWindow>>

/** Another window wrote: this one reads storage again, tab store first (either order works). */
async function syncInto(w: Win): Promise<void> {
  await w.tabs.useTabStore.persist.rehydrate()
  await w.store.useWorkspaceStore.persist.rehydrate()
}

let A: Win
let B: Win
let x: string
let y: string
let x1: string
let y1: string
let y2: string
beforeEach(async () => {
  localStorage.clear()
  vi.stubGlobal('BroadcastChannel', undefined)
  A = await openWindow()
  x = A.ws().addWorkspace('X').id
  y = A.ws().addWorkspace('Y').id
  x1 = A.tabIn(x)
  y1 = A.tabIn(y)
  y2 = A.tabIn(y)
  A.select(y, y1)
  A.select(x, x1)
  B = await openWindow() // the same world, from storage
  expect(B.at()).toEqual({ ws: x, tab: x1 })
})
afterEach(() => {
  vi.unstubAllGlobals()
})

describe('two windows: the way back is each window\'s own', () => {
  it('B, taken into the area by A\'s write, goes back to the workspace and tab B showed', async () => {
    B.select(y, y2) // B moves on; A never hears of it
    A.area.openWatchArea({ tab: 'guardian' })
    await syncInto(B)
    expect(B.ws().activeWorkspaceId).toBe('watch')
    expect(B.ws().workspaces.find((w) => w.id === y)!.activeTabId).toBe(y1) // A's write replaced what B knew of Y

    B.area.leaveWatchArea()
    expect(B.at()).toEqual({ ws: y, tab: y2 })
    A.area.leaveWatchArea()
    expect(A.at()).toEqual({ ws: x, tab: x1 })
  })

  it('a way back B took on an earlier entry of its own is not used: the synced entry takes a new one', async () => {
    B.area.openWatchArea({ tab: 'guardian' }) // B's own entry, from X / x1
    B.area.leaveWatchArea()
    expect(B.at()).toEqual({ ws: x, tab: x1 })
    B.select(y, y2)

    A.area.openWatchArea({ tab: 'guardian' })
    await syncInto(B)
    expect(B.store.getWatchReturnTarget()).toEqual({ workspaceId: y, tabId: y2 })
    B.area.leaveWatchArea()
    expect(B.at()).toEqual({ ws: y, tab: y2 })
  })

  it('a promote in A (`restampWorld`: the tag moves, the content stays) synced into B keeps B\'s way back', async () => {
    B.select(y, y2)
    A.area.openWatchArea({ tab: 'guardian' })
    await syncInto(B)
    expect(B.store.getWatchReturnTarget()).toEqual({ workspaceId: y, tabId: y2 })

    A.world.restampWorld({ worldId: 'p2', worldEpoch: 7 })
    await syncInto(B) // B's rehydrate builds new workspace objects: the same ids, the same world
    expect(B.ws().worldId).toBe('p2')
    expect(B.store.getWatchReturnTarget()).toEqual({ workspaceId: y, tabId: y2 })
    B.area.leaveWatchArea()
    expect(B.at()).toEqual({ ws: y, tab: y2 })
  })

  it('a sync while B is already in the area keeps B\'s way back', async () => {
    B.select(y, y2)
    B.area.openWatchArea({ tab: 'guardian' })
    A.area.openWatchArea({ tab: 'guardian' }) // A enters too and writes
    await syncInto(B)
    expect(B.store.getWatchReturnTarget()).toEqual({ workspaceId: y, tabId: y2 })
  })
})
