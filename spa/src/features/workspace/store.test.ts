import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore } from './store'
import { useTabStore } from '../../stores/useTabStore'
import { useHistoryStore } from '../../stores/useHistoryStore'
import { createTab } from '../../types/tab'

function makeTab() {
  return createTab({ kind: 'new-tab' })
}

describe('useWorkspaceStore', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
  })

  // === 全自由制基礎 ===

  it('initializes with empty workspaces and null activeWorkspaceId', () => {
    const state = useWorkspaceStore.getState()
    expect(state.workspaces).toEqual([])
    expect(state.activeWorkspaceId).toBeNull()
  })

  it('setActiveWorkspace accepts null', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws.id)
    useWorkspaceStore.getState().setActiveWorkspace(null)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  // === Workspace CRUD ===

  it('adds a workspace and auto-activates first', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('New WS')
    expect(ws.name).toBe('New WS')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws.id)
  })

  it('adds second workspace without changing active', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(2)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
    expect(ws2.name).toBe('WS2')
  })

  it('removes a workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('To Remove')
    useWorkspaceStore.getState().removeWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
    expect(useWorkspaceStore.getState().workspaces[0].id).toBe(ws1.id)
  })

  it('removes the last workspace and sets activeWorkspaceId to null', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Only')
    useWorkspaceStore.getState().removeWorkspace(ws.id)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(0)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('removeWorkspace on nonexistent id does nothing', () => {
    useWorkspaceStore.getState().addWorkspace('WS')
    useWorkspaceStore.getState().removeWorkspace('nonexistent')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
  })

  it('removeWorkspace on empty list does nothing', () => {
    useWorkspaceStore.getState().removeWorkspace('nonexistent')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(0)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('switches active workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
    useWorkspaceStore.getState().setActiveWorkspace(ws1.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
  })

  it('switches activeWorkspaceId when removing active workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
    useWorkspaceStore.getState().removeWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
  })

  // === Tab operations ===

  it('adds a tab to workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tab-1')
    const updated = useWorkspaceStore.getState().workspaces[0]
    expect(updated.tabs).toContain('tab-1')
  })

  it('removes a tab from workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tab-1')
    useWorkspaceStore.getState().removeTabFromWorkspace(ws.id, 'tab-1')
    const updated = useWorkspaceStore.getState().workspaces[0]
    expect(updated.tabs).not.toContain('tab-1')
  })

  it('does not add duplicate tab to workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tab-1')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tab-1')
    const updated = useWorkspaceStore.getState().workspaces[0]
    expect(updated.tabs).toEqual(['tab-1'])
  })

  it('sets workspace active tab', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tab-1')
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, 'tab-1')
    const updated = useWorkspaceStore.getState().workspaces.find(w => w.id === ws.id)!
    expect(updated.activeTabId).toBe('tab-1')
  })

  it('finds workspace containing a tab', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tab-1')
    expect(useWorkspaceStore.getState().findWorkspaceByTab('tab-1')?.id).toBe(ws.id)
    expect(useWorkspaceStore.getState().findWorkspaceByTab('tab-unknown')).toBeNull()
  })

  // === insertTab ===

  it('insertTab with no workspace — tab stays standalone', () => {
    useWorkspaceStore.getState().insertTab('tab-1')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(0)
  })

  it('insertTab with active workspace adds to that workspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    useWorkspaceStore.getState().insertTab('tab-1')
    const updated = useWorkspaceStore.getState().workspaces.find(w => w.id === ws.id)!
    expect(updated.tabs).toContain('tab-1')
    expect(updated.activeTabId).toBe('tab-1')
  })

  it('insertTab with explicit wsId adds to specified workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().setActiveWorkspace(ws1.id)
    useWorkspaceStore.getState().insertTab('tab-1', ws2.id)
    const updated1 = useWorkspaceStore.getState().workspaces.find(w => w.id === ws1.id)!
    const updated2 = useWorkspaceStore.getState().workspaces.find(w => w.id === ws2.id)!
    expect(updated1.tabs).not.toContain('tab-1')
    expect(updated2.tabs).toContain('tab-1')
    expect(updated2.activeTabId).toBe('tab-1')
  })

  it('insertTab with explicit null forces standalone', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    useWorkspaceStore.getState().insertTab('tab-1', null)
    const updated = useWorkspaceStore.getState().workspaces.find(w => w.id === ws.id)!
    expect(updated.tabs).not.toContain('tab-1')
  })

  // === addWorkspace options ===

  it('addWorkspace passes icon to createWorkspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WithIcon', { icon: 'R' })
    expect(ws.icon).toBe('R')
  })

  // === Workspace settings ===

  it('renameWorkspace updates workspace name', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Old Name')
    useWorkspaceStore.getState().renameWorkspace(ws.id, 'New Name')
    expect(useWorkspaceStore.getState().workspaces[0].name).toBe('New Name')
  })

  it('setWorkspaceIcon updates workspace icon', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setWorkspaceIcon(ws.id, 'R')
    expect(useWorkspaceStore.getState().workspaces[0].icon).toBe('R')
  })

  // === insertTab edge cases ===

  it('insertTab with nonexistent wsId is a no-op', () => {
    useWorkspaceStore.getState().addWorkspace('WS')
    useWorkspaceStore.getState().insertTab('tab-1', 'deleted-ws')
    expect(useWorkspaceStore.getState().workspaces[0].tabs).not.toContain('tab-1')
  })

  it('insertTab removes tab from previous workspace (singleton dedup)', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().insertTab('tab-1', ws1.id)
    // Move to ws2
    useWorkspaceStore.getState().insertTab('tab-1', ws2.id)
    const updated1 = useWorkspaceStore.getState().workspaces.find(w => w.id === ws1.id)!
    const updated2 = useWorkspaceStore.getState().workspaces.find(w => w.id === ws2.id)!
    expect(updated1.tabs).not.toContain('tab-1')
    expect(updated2.tabs).toContain('tab-1')
    expect(updated2.activeTabId).toBe('tab-1')
  })

  it('insertTab dedup clears activeTabId in source workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().insertTab('tab-1', ws1.id)
    expect(useWorkspaceStore.getState().workspaces.find(w => w.id === ws1.id)!.activeTabId).toBe('tab-1')
    // Move to ws2 — ws1.activeTabId should clear
    useWorkspaceStore.getState().insertTab('tab-1', ws2.id)
    expect(useWorkspaceStore.getState().workspaces.find(w => w.id === ws1.id)!.activeTabId).toBeNull()
  })

  describe('closeTabInWorkspace', () => {
    beforeEach(() => {
      useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
      useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
    })

    it('closes middle tab and selects right-adjacent tab', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tabs = [makeTab(), makeTab(), makeTab()]
      tabs.forEach((t) => {
        useTabStore.getState().addTab(t)
        useWorkspaceStore.getState().addTabToWorkspace(ws.id, t.id)
      })
      useTabStore.getState().setActiveTab(tabs[1].id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabs[1].id)

      useWorkspaceStore.getState().closeTabInWorkspace(tabs[1].id)

      expect(useTabStore.getState().tabs[tabs[1].id]).toBeUndefined()
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
      const updatedWs = useWorkspaceStore.getState().workspaces[0]
      expect(updatedWs.tabs).toEqual([tabs[0].id, tabs[2].id])
      expect(updatedWs.activeTabId).toBe(tabs[2].id)
    })

    it('closes last-index tab and selects left-adjacent tab', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tabs = [makeTab(), makeTab(), makeTab()]
      tabs.forEach((t) => {
        useTabStore.getState().addTab(t)
        useWorkspaceStore.getState().addTabToWorkspace(ws.id, t.id)
      })
      useTabStore.getState().setActiveTab(tabs[2].id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabs[2].id)

      useWorkspaceStore.getState().closeTabInWorkspace(tabs[2].id)

      expect(useTabStore.getState().activeTabId).toBe(tabs[1].id)
      const updatedWs = useWorkspaceStore.getState().workspaces[0]
      expect(updatedWs.activeTabId).toBe(tabs[1].id)
    })

    it('closes only tab in workspace → activeTabId null', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tab = makeTab()
      useTabStore.getState().addTab(tab)
      useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab.id)
      useTabStore.getState().setActiveTab(tab.id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tab.id)

      useWorkspaceStore.getState().closeTabInWorkspace(tab.id)

      expect(useTabStore.getState().activeTabId).toBeNull()
      const updatedWs = useWorkspaceStore.getState().workspaces[0]
      expect(updatedWs.tabs).toEqual([])
      expect(updatedWs.activeTabId).toBeNull()
    })

    it('does not close locked tab', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tab = makeTab()
      useTabStore.getState().addTab(tab)
      useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab.id)
      useTabStore.getState().toggleLock(tab.id)
      useTabStore.getState().setActiveTab(tab.id)

      useWorkspaceStore.getState().closeTabInWorkspace(tab.id)

      expect(useTabStore.getState().tabs[tab.id]).toBeDefined()
    })

    it('no-op for nonexistent tab', () => {
      useWorkspaceStore.getState().addWorkspace('Test')
      useWorkspaceStore.getState().closeTabInWorkspace('nonexistent')
      // Should not throw
    })

    it('records close in history store', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tab = makeTab()
      useTabStore.getState().addTab(tab)
      useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab.id)
      useTabStore.getState().setActiveTab(tab.id)

      useWorkspaceStore.getState().closeTabInWorkspace(tab.id)

      const { closedTabs } = useHistoryStore.getState()
      expect(closedTabs).toHaveLength(1)
      expect(closedTabs[0].tab.id).toBe(tab.id)
      expect(closedTabs[0].fromWorkspaceId).toBe(ws.id)
    })

    it('skipHistory option skips recording to history store', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tab = makeTab()
      useTabStore.getState().addTab(tab)
      useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab.id)
      useTabStore.getState().setActiveTab(tab.id)

      useWorkspaceStore.getState().closeTabInWorkspace(tab.id, { skipHistory: true })

      const { closedTabs } = useHistoryStore.getState()
      expect(closedTabs).toHaveLength(0)
      // Tab should still be closed
      expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    })

    it('does not change activeTabId when closing non-active tab', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      // 3 tabs: active is tabs[1] (middle), we close tabs[2] (last)
      // Without the wasActive guard, nextTabId for tabs[2] is tabs[1] — no-op coincidence.
      // Use tabs[0] as active, close tabs[2] (last): nextTabId = tabs[1] which ≠ tabs[0].
      // This exposes the bug: ws.activeTabId gets overwritten to tabs[1].
      const tabs = [makeTab(), makeTab(), makeTab()]
      tabs.forEach((t) => {
        useTabStore.getState().addTab(t)
        useWorkspaceStore.getState().addTabToWorkspace(ws.id, t.id)
      })
      // tabs[0] is active in both tabStore and workspace
      useTabStore.getState().setActiveTab(tabs[0].id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabs[0].id)

      // Close tabs[2] (non-active); nextTabId = tabs[1] ≠ tabs[0]
      useWorkspaceStore.getState().closeTabInWorkspace(tabs[2].id)

      // tabStore.activeTabId must remain tabs[0]
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
      // ws.activeTabId must also remain tabs[0] — not be overwritten to tabs[1]
      const updatedWs = useWorkspaceStore.getState().workspaces[0]
      expect(updatedWs.activeTabId).toBe(tabs[0].id)
    })

    it('closes standalone tab with global tabOrder adjacency', () => {
      // No workspace — standalone tab
      const tabs = [makeTab(), makeTab(), makeTab()]
      tabs.forEach((t) => useTabStore.getState().addTab(t))
      useTabStore.getState().setActiveTab(tabs[1].id)

      useWorkspaceStore.getState().closeTabInWorkspace(tabs[1].id)

      expect(useTabStore.getState().tabs[tabs[1].id]).toBeUndefined()
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
    })
  })
})
