import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { UNSORTED_WORKSPACE_ID, useWorkspaceStore } from './store'
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

  it('clears activeTab when selecting an empty workspace while a tab of another workspace is active', () => {
    // (Was: "from a standalone tab". Every tab belongs to a workspace now — spec §4.3 — and the rule is the same.)
    const other = useWorkspaceStore.getState().addWorkspace('Other')
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.getState().addTab(tab)
    useWorkspaceStore.getState().insertTab(tab.id, other.id)
    useTabStore.getState().setActiveTab(tab.id)

    // Empty workspace.
    const emptyWs = useWorkspaceStore.getState().addWorkspace('Empty')

    const { result } = renderHook(() => useTabWorkspaceActions([tab]))

    act(() => { result.current.handleSelectWorkspace(emptyWs.id) })

    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(emptyWs.id)
    // Must drop the other workspace's tab so it stops masking the workspace selection.
    expect(useTabStore.getState().activeTabId).toBeNull()
    expect(useWorkspaceStore.getState().findWorkspaceByTab(tab.id)?.id).toBe(other.id)
  })
})

// Every tab belongs to exactly one workspace (Profile Sync spec §4.3): the `+` button is a producer.
describe('handleAddTab', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  const addOne = (): string => {
    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => { result.current.handleAddTab() })
    const { tabOrder, activeTabId } = useTabStore.getState()
    expect(tabOrder).toHaveLength(1)
    expect(activeTabId).toBe(tabOrder[0])
    return tabOrder[0]
  }

  it('puts the tab in the active workspace', () => {
    useWorkspaceStore.getState().addWorkspace('A')
    const b = useWorkspaceStore.getState().addWorkspace('B')
    useWorkspaceStore.getState().setActiveWorkspace(b.id)
    expect(useWorkspaceStore.getState().findWorkspaceByTab(addOne())?.id).toBe(b.id)
  })

  it('no active workspace → the first workspace', () => {
    const a = useWorkspaceStore.getState().addWorkspace('A')
    useWorkspaceStore.getState().addWorkspace('B')
    useWorkspaceStore.getState().setActiveWorkspace(null)
    expect(useWorkspaceStore.getState().findWorkspaceByTab(addOne())?.id).toBe(a.id)
  })

  it('no workspace at all → Unsorted is created, gets the tab and becomes active', () => {
    const tabId = addOne()
    const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
    expect(workspaces.map((w) => w.id)).toEqual([UNSORTED_WORKSPACE_ID])
    expect(workspaces[0].tabs).toEqual([tabId])
    expect(activeWorkspaceId).toBe(UNSORTED_WORKSPACE_ID)
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

  it('works without any workspace: the tab lands in a new Unsorted', () => {
    useWorkspaceStore.getState().reset()
    const { result } = renderHook(() => useTabWorkspaceActions([]))

    let tabId: string
    act(() => {
      tabId = result.current.openSingletonAndSelect({ kind: 'settings', scope: 'global' })
    })

    expect(useTabStore.getState().tabs[tabId!]).toBeDefined()
    expect(useTabStore.getState().activeTabId).toBe(tabId!)
    expect(useWorkspaceStore.getState().findWorkspaceByTab(tabId!)?.id).toBe(UNSORTED_WORKSPACE_ID)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(UNSORTED_WORKSPACE_ID)
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

  it('switches activeWorkspaceId to the target workspace', () => {
    const wsA = useWorkspaceStore.getState().addWorkspace('A')
    const wsB = useWorkspaceStore.getState().addWorkspace('B')
    useWorkspaceStore.getState().setActiveWorkspace(wsA.id)

    const { result } = renderHook(() => useTabWorkspaceActions([]))
    act(() => {
      result.current.handleAddTabToWorkspace(wsB.id)
    })

    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(wsB.id)
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

// The tab bar's own reorder. Every tab belongs to a workspace (Profile Sync spec §4.3): the bar lists the active
// workspace's tabs, and there is no list of workspace-less tabs for it to reorder.
describe('handleReorderTabs', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  function twoTabs(): [Tab, Tab] {
    const a = createTab({ kind: 'dashboard' })
    const b = createTab({ kind: 'hosts' })
    useTabStore.getState().addTab(a)
    useTabStore.getState().addTab(b)
    return [a, b]
  }

  it('reorders the active workspace\'s tabs, and leaves the global tabOrder alone', () => {
    const [a, b] = twoTabs()
    const ws = useWorkspaceStore.getState().addWorkspace('WS')
    useWorkspaceStore.getState().insertTab(a.id, ws.id)
    useWorkspaceStore.getState().insertTab(b.id, ws.id)
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)

    const { result } = renderHook(() => useTabWorkspaceActions([a, b]))
    act(() => { result.current.handleReorderTabs([b.id, a.id]) })

    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)!.tabs).toEqual([b.id, a.id])
    expect(useTabStore.getState().tabOrder).toEqual([a.id, b.id])
  })

  it('with no active workspace it changes nothing (was: reordered the standalone tabs in tabOrder)', () => {
    const [a, b] = twoTabs()
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()

    const { result } = renderHook(() => useTabWorkspaceActions([a, b]))
    act(() => { result.current.handleReorderTabs([b.id, a.id]) })

    expect(useTabStore.getState().tabOrder).toEqual([a.id, b.id])
    expect(useWorkspaceStore.getState().workspaces).toEqual([])
  })
})
