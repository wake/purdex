import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { renderHook } from '@testing-library/react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { WATCH_WORKSPACE_ID, __resetReturnWorkspaceForTest } from '../features/workspace/store'
import { openWatchArea } from '../features/workspace/lib/open-watch-area'
import { WATCH_GUARDIAN_TAB_ID } from '../lib/watch/ids'
import { createTab } from '../types/tab'
import { useShortcuts } from './useShortcuts'

function mockElectronAPI() {
  let cb: ((payload: { action: string }) => void) | null = null
  ;(window as unknown as Record<string, unknown>).electronAPI = {
    onShortcut: (f: (payload: { action: string }) => void) => { cb = f; return () => {} },
    signalReady: () => {},
  }
  return (action: string) => cb?.({ action })
}

const ws = () => useWorkspaceStore.getState()

describe('workspace shortcuts with the watcher area', () => {
  let a: string
  let b: string
  let c: string
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    ws().reset()
    __resetReturnWorkspaceForTest()
    a = ws().addWorkspace('A').id
    b = ws().addWorkspace('B').id
    c = ws().addWorkspace('C').id
    ws().ensureWatchWorkspace()
    ws().setActiveWorkspace(a)
  })
  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
  })

  it('switch-workspace-N indexes the visible list: the area is never slot N', () => {
    const fire = mockElectronAPI()
    renderHook(() => useShortcuts())
    fire('switch-workspace-3')
    expect(ws().activeWorkspaceId).toBe(c)
    fire('switch-workspace-4') // would be `watch` if the list counted it
    expect(ws().activeWorkspaceId).toBe(c)
  })

  it('next-workspace from C wraps to A, skipping the area', () => {
    const fire = mockElectronAPI()
    ws().setActiveWorkspace(c)
    renderHook(() => useShortcuts())
    fire('next-workspace')
    expect(ws().activeWorkspaceId).toBe(a)
  })

  it('prev / next from inside the area are relative to the return workspace', () => {
    const fire = mockElectronAPI()
    ws().setActiveWorkspace(b)
    ws().setActiveWorkspace(WATCH_WORKSPACE_ID)
    renderHook(() => useShortcuts())
    fire('next-workspace')
    expect(ws().activeWorkspaceId).toBe(c)
    ws().setActiveWorkspace(b)
    ws().setActiveWorkspace(WATCH_WORKSPACE_ID)
    fire('prev-workspace')
    expect(ws().activeWorkspaceId).toBe(a)
  })

  it('switch-workspace-home goes to the first visible workspace', () => {
    const fire = mockElectronAPI()
    ws().setActiveWorkspace(WATCH_WORKSPACE_ID)
    renderHook(() => useShortcuts())
    fire('switch-workspace-home')
    expect(ws().activeWorkspaceId).toBe(a)
    expect(b).toBeTruthy()
  })
})

describe('⌘W with the watcher area (journal v5 §3.7, U11)', () => {
  let a: string
  let t1: string
  let t2: string
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    ws().reset()
    __resetReturnWorkspaceForTest()
    a = ws().addWorkspace('A').id
    for (const name of ['t1', 't2'] as const) {
      const tab = createTab({ kind: 'new-tab' })
      useTabStore.getState().addTab(tab)
      ws().insertTab(tab.id, a)
      if (name === 't1') t1 = tab.id
      else t2 = tab.id
    }
    useTabStore.getState().setActiveTab(t1)
    ws().setActiveWorkspace(a)
    ws().setWorkspaceActiveTab(a, t1)
  })
  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
  })

  it('in the area it leaves the area for the workspace and tab it was entered from, and closes no tab', () => {
    const fire = mockElectronAPI()
    renderHook(() => useShortcuts())
    openWatchArea({ tab: 'guardian' })
    expect(useTabStore.getState().activeTabId).toBe(WATCH_GUARDIAN_TAB_ID)
    ws().setWorkspaceActiveTab(a, t2) // A's own active tab moved meanwhile (a sync): the way back is still t1
    const tabsBefore = Object.keys(useTabStore.getState().tabs).sort()
    const areaBefore = [...ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!.tabs]
    fire('close-tab')
    expect(ws().activeWorkspaceId).toBe(a)
    expect(useTabStore.getState().activeTabId).toBe(t1)
    expect(Object.keys(useTabStore.getState().tabs).sort()).toEqual(tabsBefore)
    expect(ws().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!.tabs).toEqual(areaBefore)
  })

  it('outside the area it closes the active tab as before, the area present or not', () => {
    const fire = mockElectronAPI()
    renderHook(() => useShortcuts())
    openWatchArea({ tab: 'guardian' })
    fire('close-tab') // leaves the area
    fire('close-tab') // now an ordinary ⌘W
    expect(useTabStore.getState().tabs[t1]).toBeUndefined()
    expect(ws().activeWorkspaceId).toBe(a)
  })
})
