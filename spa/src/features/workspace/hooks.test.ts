import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useWorkspaceStore } from './store'
import { useTabStore } from '../../stores/useTabStore'
import { useTabWorkspaceActions } from './hooks'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'

describe('workspace tab recall', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('handleSelectWorkspace uses latest store state, not stale closure', () => {
    const tab1 = createTab({ kind: 'dashboard' })
    const tab2 = createTab({ kind: 'hosts' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().addTabToWorkspace(ws1.id, tab1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws2.id, tab2.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws1.id, tab1.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws2.id, tab2.id)

    const displayTabs = [tab1, tab2]
    const { result } = renderHook(() => useTabWorkspaceActions(displayTabs))

    act(() => { result.current.handleSelectWorkspace(ws2.id) })
    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)

    act(() => { useWorkspaceStore.getState().setWorkspaceActiveTab(ws1.id, tab1.id) })

    act(() => { result.current.handleSelectWorkspace(ws1.id) })
    expect(useTabStore.getState().activeTabId).toBe(tab1.id)
  })

  it('falls back to first tab when activeTabId points to closed tab', () => {
    const tab1 = createTab({ kind: 'dashboard' })
    const tab2 = createTab({ kind: 'hosts' })
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    const ws = useWorkspaceStore.getState().addWorkspace('WS')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab2.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tab1.id)

    const displayTabs = [tab1, tab2] as Tab[]
    const { result } = renderHook(() => useTabWorkspaceActions(displayTabs))

    useWorkspaceStore.getState().removeTabFromWorkspace(ws.id, tab1.id)
    useTabStore.getState().closeTab(tab1.id)

    act(() => { result.current.handleSelectWorkspace(ws.id) })
    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
  })
})

describe('openSingletonAndSelect', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('creates singleton tab, inserts into active workspace, and selects it', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WS1')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))

    let tabId: string
    act(() => {
      tabId = result.current.openSingletonAndSelect({ kind: 'hosts' })
    })

    // Tab was created
    expect(useTabStore.getState().tabs[tabId!]).toBeDefined()
    // Tab is active
    expect(useTabStore.getState().activeTabId).toBe(tabId!)
    // Tab is in workspace
    const updatedWs = useWorkspaceStore.getState().workspaces.find(w => w.id === ws.id)
    expect(updatedWs!.tabs).toContain(tabId!)
    // Workspace active tab is set
    expect(updatedWs!.activeTabId).toBe(tabId!)
  })

  it('reuses existing singleton tab instead of creating duplicate', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WS1')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))

    let tabId1: string
    let tabId2: string
    act(() => {
      tabId1 = result.current.openSingletonAndSelect({ kind: 'hosts' })
    })
    act(() => {
      tabId2 = result.current.openSingletonAndSelect({ kind: 'hosts' })
    })

    expect(tabId1!).toBe(tabId2!)
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(1)
  })

  it('works without active workspace (standalone tabs)', () => {
    const { result } = renderHook(() => useTabWorkspaceActions([]))

    let tabId: string
    act(() => {
      tabId = result.current.openSingletonAndSelect({ kind: 'settings', scope: 'global' })
    })

    expect(useTabStore.getState().tabs[tabId!]).toBeDefined()
    expect(useTabStore.getState().activeTabId).toBe(tabId!)
  })
})
