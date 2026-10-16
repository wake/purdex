import { describe, it, expect, beforeEach } from 'vitest'
import {
  UNSORTED_WORKSPACE_ID, WATCH_WORKSPACE_ID, useWorkspaceStore, getReturnWorkspaceId, visibleWorkspaces, __resetReturnWorkspaceForTest,
} from './store'
import { useTabStore } from '../../stores/useTabStore'
import { createTab } from '../../types/tab'
import { isWatchTabId, watchHostTabId } from '../../lib/watch/ids'

const ws = () => useWorkspaceStore.getState()

describe('the watch workspace in the store', () => {
  beforeEach(() => {
    ws().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    __resetReturnWorkspaceForTest()
  })

  it('ensureWatchWorkspace creates it last, once, and does not activate it', () => {
    const a = ws().addWorkspace('A')
    const w1 = ws().ensureWatchWorkspace()
    const w2 = ws().ensureWatchWorkspace()
    expect(w1.id).toBe(WATCH_WORKSPACE_ID)
    expect(w2).toBe(w1)
    expect(ws().workspaces.map((x) => x.id)).toEqual([a.id, WATCH_WORKSPACE_ID])
    expect(ws().activeWorkspaceId).toBe(a.id)
    expect(visibleWorkspaces(ws().workspaces).map((x) => x.id)).toEqual([a.id])
  })

  it('ids: watch- prefix recognised, generated ids never', () => {
    expect(isWatchTabId('watch-guardian')).toBe(true)
    expect(isWatchTabId(watchHostTabId('h1'))).toBe(true)
    expect(isWatchTabId('abc123')).toBe(false)
  })

  it('removeWorkspace(watch) is a no-op', () => {
    ws().addWorkspace('A')
    ws().ensureWatchWorkspace()
    ws().removeWorkspace(WATCH_WORKSPACE_ID)
    expect(ws().workspaces.some((x) => x.id === WATCH_WORKSPACE_ID)).toBe(true)
  })

  it('watch is never the heir of a removed workspace, nor the active fallback', () => {
    const a = ws().addWorkspace('A')
    const b = ws().addWorkspace('B')
    ws().ensureWatchWorkspace()
    const tab = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(tab)
    ws().insertTab(tab.id, b.id)
    ws().setActiveWorkspace(b.id)
    ws().removeWorkspace(b.id)
    expect(ws().workspaces.find((x) => x.id === WATCH_WORKSPACE_ID)!.tabs).toEqual([])
    expect(ws().workspaces.find((x) => x.id === a.id)!.tabs).toEqual([tab.id])
    expect(ws().activeWorkspaceId).toBe(a.id)
  })

  it('removing the last visible workspace makes an Unsorted heir, not watch', () => {
    const a = ws().addWorkspace('A')
    ws().ensureWatchWorkspace()
    const tab = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(tab)
    ws().insertTab(tab.id, a.id)
    ws().removeWorkspace(a.id)
    expect(ws().workspaces.find((x) => x.id === UNSORTED_WORKSPACE_ID)!.tabs).toEqual([tab.id])
    expect(ws().workspaces.find((x) => x.id === WATCH_WORKSPACE_ID)!.tabs).toEqual([])
    expect(ws().activeWorkspaceId).toBe(UNSORTED_WORKSPACE_ID)
  })

  it('reorderWorkspaces with only the visible ids keeps watch', () => {
    const a = ws().addWorkspace('A')
    const b = ws().addWorkspace('B')
    ws().ensureWatchWorkspace()
    ws().reorderWorkspaces([b.id, a.id])
    expect(ws().workspaces.map((x) => x.id)).toEqual([b.id, a.id, WATCH_WORKSPACE_ID])
  })

  describe('insertTab never puts a non-watch tab in the area', () => {
    it('an explicit watch target goes to the return workspace', () => {
      const a = ws().addWorkspace('A')
      const b = ws().addWorkspace('B')
      ws().ensureWatchWorkspace()
      ws().setActiveWorkspace(b.id)
      ws().setActiveWorkspace(WATCH_WORKSPACE_ID)
      const tab = createTab({ kind: 'new-tab' })
      useTabStore.getState().addTab(tab)
      ws().insertTab(tab.id, WATCH_WORKSPACE_ID)
      expect(ws().workspaces.find((x) => x.id === b.id)!.tabs).toEqual([tab.id])
      expect(ws().workspaces.find((x) => x.id === a.id)!.tabs).toEqual([])
      expect(ws().workspaces.find((x) => x.id === WATCH_WORKSPACE_ID)!.tabs).toEqual([])
    })

    it('opening a session while the area is shown (active = watch, no target) goes to the return workspace', () => {
      const a = ws().addWorkspace('A')
      ws().ensureWatchWorkspace()
      ws().setActiveWorkspace(WATCH_WORKSPACE_ID)
      const tab = createTab({ kind: 'new-tab' })
      useTabStore.getState().addTab(tab)
      ws().insertTab(tab.id)
      expect(ws().workspaces.find((x) => x.id === a.id)!.tabs).toEqual([tab.id])
      expect(ws().workspaces.find((x) => x.id === WATCH_WORKSPACE_ID)!.tabs).toEqual([])
    })

    it('with no other workspace it makes Unsorted', () => {
      ws().ensureWatchWorkspace()
      ws().setActiveWorkspace(WATCH_WORKSPACE_ID)
      const tab = createTab({ kind: 'new-tab' })
      useTabStore.getState().addTab(tab)
      ws().insertTab(tab.id)
      expect(ws().workspaces.find((x) => x.id === UNSORTED_WORKSPACE_ID)!.tabs).toEqual([tab.id])
    })

    it('a watch tab may enter the area', () => {
      ws().addWorkspace('A')
      ws().ensureWatchWorkspace()
      ws().insertTab('watch-guardian', WATCH_WORKSPACE_ID)
      expect(ws().workspaces.find((x) => x.id === WATCH_WORKSPACE_ID)!.tabs).toEqual(['watch-guardian'])
    })
  })

  it('the return workspace follows the last non-watch active workspace', () => {
    const a = ws().addWorkspace('A')
    const b = ws().addWorkspace('B')
    ws().ensureWatchWorkspace()
    ws().setActiveWorkspace(b.id)
    ws().setActiveWorkspace(WATCH_WORKSPACE_ID)
    expect(getReturnWorkspaceId()).toBe(b.id)
    ws().removeWorkspace(b.id)
    expect(getReturnWorkspaceId()).toBe(a.id)
  })
})

describe('toggleLock', () => {
  beforeEach(() => useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] }))

  it('refuses a watch tab id, still toggles an ordinary tab', () => {
    const t = createTab({ kind: 'new-tab' })
    const w = { ...createTab({ kind: 'watch-events' }), id: 'watch-events', locked: true }
    useTabStore.getState().addTab(t)
    useTabStore.getState().addTab(w)
    useTabStore.getState().toggleLock('watch-events')
    expect(useTabStore.getState().tabs['watch-events'].locked).toBe(true)
    useTabStore.getState().toggleLock(t.id)
    expect(useTabStore.getState().tabs[t.id].locked).toBe(true)
  })
})
