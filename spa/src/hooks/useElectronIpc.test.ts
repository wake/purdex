// A tab received from another window (tear-off / merge) is a tab producer: every tab belongs to exactly one
// workspace (Profile Sync spec §4.3), so the test says where it went in each of the three situations.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useTabStore } from '../stores/useTabStore'
import { UNSORTED_WORKSPACE_ID, useWorkspaceStore } from '../features/workspace/store'
import { createTab } from '../types/tab'
import { useElectronIpc } from './useElectronIpc'

let receive: ((tabJson: string, replace: boolean) => void) | null = null

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useWorkspaceStore.getState().reset()
  receive = null
  ;(window as unknown as Record<string, unknown>).electronAPI = {
    signalReady: () => {},
    onTabReceived: (cb: (tabJson: string, replace: boolean) => void) => {
      receive = cb
      return () => {}
    },
  }
})

afterEach(() => {
  delete (window as unknown as Record<string, unknown>).electronAPI
})

function receiveTab(replace = false): string {
  renderHook(() => useElectronIpc())
  const tab = createTab({ kind: 'new-tab' })
  receive!(JSON.stringify(tab), replace)
  expect(useTabStore.getState().activeTabId).toBe(tab.id)
  return tab.id
}

describe('useElectronIpc — a received tab always lands in a workspace', () => {
  it('the active workspace', () => {
    useWorkspaceStore.getState().addWorkspace('A')
    const b = useWorkspaceStore.getState().addWorkspace('B')
    useWorkspaceStore.getState().setActiveWorkspace(b.id)
    expect(useWorkspaceStore.getState().findWorkspaceByTab(receiveTab())?.id).toBe(b.id)
  })

  it('no active workspace → the first workspace', () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    useWorkspaceStore.getState().addWorkspace('B')
    useWorkspaceStore.getState().setActiveWorkspace(null)
    expect(useWorkspaceStore.getState().findWorkspaceByTab(receiveTab())?.id).toBe(a.id)
  })

  it('a new window with no workspace (tear-off, replace) → Unsorted is created and gets the tab', () => {
    const tabId = receiveTab(true)
    const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
    expect(workspaces.map((w) => w.id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(workspaces[0].tabs).toEqual([tabId])
    expect(activeWorkspaceId).toBe(UNSORTED_WORKSPACE_ID)
  })
})
