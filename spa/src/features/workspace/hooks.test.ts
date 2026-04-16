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

  it('inserts tab into explicit wsId even when a different workspace is active', () => {
    const wsA = useWorkspaceStore.getState().addWorkspace('WS-A')
    const wsB = useWorkspaceStore.getState().addWorkspace('WS-B')
    useWorkspaceStore.getState().setActiveWorkspace(wsA.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))

    let tabId: string
    act(() => {
      tabId = result.current.openSingletonAndSelect(
        { kind: 'settings', scope: { workspaceId: wsB.id } },
        wsB.id,
      )
    })

    // Tab should be inserted into wsB, NOT the active wsA
    const updatedWsB = useWorkspaceStore.getState().workspaces.find(w => w.id === wsB.id)
    const updatedWsA = useWorkspaceStore.getState().workspaces.find(w => w.id === wsA.id)
    expect(updatedWsB!.tabs).toContain(tabId!)
    expect(updatedWsA!.tabs).not.toContain(tabId!)
  })
})

describe('handleAddTabToWorkspace', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('creates a tab, adds to tab store, and inserts into given workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('A')

    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => {
      result.current.handleAddTabToWorkspace(ws.id)
    })

    const updated = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)!
    expect(updated.tabs.length).toBe(1)
    const newTabId = updated.tabs[0]
    expect(updated.activeTabId).toBe(newTabId)
    expect(useTabStore.getState().tabs[newTabId]).toBeDefined()
    expect(useTabStore.getState().activeTabId).toBe(newTabId)
  })
})

describe('handleReorderWorkspaceTabs', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('delegates to workspace store reorderWorkspaceTabs', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('A')
    const t1 = createTab({ kind: 'new-tab' })
    const t2 = createTab({ kind: 'new-tab' })
    const t3 = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(t1)
    useTabStore.getState().addTab(t2)
    useTabStore.getState().addTab(t3)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t2.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t3.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => {
      result.current.handleReorderWorkspaceTabs(ws.id, [t2.id, t1.id, t3.id])
    })

    const updated = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)!
    expect(updated.tabs).toEqual([t2.id, t1.id, t3.id])
  })
})
