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
