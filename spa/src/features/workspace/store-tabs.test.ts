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

  // === insertTab edge cases ===

  it('insertTab with nonexistent wsId does not orphan the tab from its source workspace', () => {
    // Regression for concurrent-delete race: caller drags tab from ws1 to
    // ws2, but ws2 was deleted in another session before the drop lands.
    // insertTab must abort entirely rather than running the dedup branch,
    // which would remove the tab from ws1 and leave it in no workspace.
    const ws1 = useWorkspaceStore.getState().addWorkspace('W1')
    useWorkspaceStore.getState().insertTab('tab-1', ws1.id)
    useWorkspaceStore.getState().insertTab('tab-1', 'deleted-ws')
    const after = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws1.id)!
    expect(after.tabs).toEqual(['tab-1'])
  })

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

  // === closeTabInWorkspace ===

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
      useTabStore.setState({ visitHistory: [] }) // clear to test adjacent fallback

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
      useTabStore.setState({ visitHistory: [] }) // clear to test adjacent fallback

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
      useTabStore.setState({ visitHistory: [] }) // clear to test adjacent fallback

      useWorkspaceStore.getState().closeTabInWorkspace(tabs[1].id)

      expect(useTabStore.getState().tabs[tabs[1].id]).toBeUndefined()
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
    })

    it('closing last standalone tab sets activeTabId to null (does not leak to workspace tabs)', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('WS')
      const wsTab = makeTab()
      useTabStore.getState().addTab(wsTab)
      useWorkspaceStore.getState().addTabToWorkspace(ws.id, wsTab.id)

      const standalone = makeTab()
      useTabStore.getState().addTab(standalone)
      useTabStore.getState().setActiveTab(standalone.id)
      useTabStore.setState({ visitHistory: [] })

      useWorkspaceStore.getState().closeTabInWorkspace(standalone.id)

      // Should be null — not the workspace tab
      expect(useTabStore.getState().activeTabId).toBeNull()
    })

    it('visitHistory skips workspace tabs when closing standalone tab', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('WS')
      const wsTab = makeTab()
      useTabStore.getState().addTab(wsTab)
      useWorkspaceStore.getState().addTabToWorkspace(ws.id, wsTab.id)

      const s1 = makeTab()
      const s2 = makeTab()
      useTabStore.getState().addTab(s1)
      useTabStore.getState().addTab(s2)
      useTabStore.getState().setActiveTab(s2.id)
      // visitHistory has workspace tab more recently than standalone tab
      useTabStore.setState({ visitHistory: [s1.id, wsTab.id] })

      useWorkspaceStore.getState().closeTabInWorkspace(s2.id)

      // Should pick s1 (standalone) from visitHistory, skipping wsTab
      expect(useTabStore.getState().activeTabId).toBe(s1.id)
    })

    it('prefers visitHistory over adjacent when selecting next tab', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tabs = [makeTab(), makeTab(), makeTab()]
      tabs.forEach((t) => {
        useTabStore.getState().addTab(t)
        useWorkspaceStore.getState().addTabToWorkspace(ws.id, t.id)
      })
      // Visit order: tabs[0] → tabs[2] → tabs[1] (tabs[1] is active)
      useTabStore.getState().setActiveTab(tabs[0].id)
      useTabStore.getState().setActiveTab(tabs[2].id)
      useTabStore.getState().setActiveTab(tabs[1].id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabs[1].id)

      useWorkspaceStore.getState().closeTabInWorkspace(tabs[1].id)

      // Should go to tabs[2] (last visited in workspace), not tabs[2] by adjacent
      // In this case the result is the same, but verify the mechanism:
      // visitHistory = [tabs[0], tabs[2]] → tabs[2] is most recent in scope
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
    })

    it('falls back to adjacent when visitHistory has no in-scope tabs', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tabs = [makeTab(), makeTab(), makeTab()]
      tabs.forEach((t) => {
        useTabStore.getState().addTab(t)
        useWorkspaceStore.getState().addTabToWorkspace(ws.id, t.id)
      })
      // Set active directly without building visitHistory
      useTabStore.setState({ visitHistory: [] })
      useTabStore.getState().setActiveTab(tabs[1].id)
      // visitHistory now only has one entry (the previous null→tabs[1] doesn't push)
      // Clear it explicitly to test fallback
      useTabStore.setState({ visitHistory: [] })
      useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabs[1].id)

      useWorkspaceStore.getState().closeTabInWorkspace(tabs[1].id)

      // No visitHistory → fallback to adjacent (tabs[2])
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
    })

    it('visitHistory skips tabs from other workspaces', () => {
      const wsA = useWorkspaceStore.getState().addWorkspace('WS A')
      const wsB = useWorkspaceStore.getState().addWorkspace('WS B')
      const tabA1 = makeTab()
      const tabA2 = makeTab()
      const tabB1 = makeTab()
      ;[tabA1, tabA2, tabB1].forEach((t) => useTabStore.getState().addTab(t))
      useWorkspaceStore.getState().addTabToWorkspace(wsA.id, tabA1.id)
      useWorkspaceStore.getState().addTabToWorkspace(wsA.id, tabA2.id)
      useWorkspaceStore.getState().addTabToWorkspace(wsB.id, tabB1.id)

      // Visit: tabA1 → tabB1 → tabA2 (tabA2 is active, in WS A)
      useTabStore.getState().setActiveTab(tabA1.id)
      useTabStore.getState().setActiveTab(tabB1.id)
      useTabStore.getState().setActiveTab(tabA2.id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(wsA.id, tabA2.id)

      useWorkspaceStore.getState().closeTabInWorkspace(tabA2.id)

      // visitHistory = [tabA1, tabB1]; scoped to WS A = [tabA1]
      // Should select tabA1, NOT tabB1
      expect(useTabStore.getState().activeTabId).toBe(tabA1.id)
    })

    it('skips already-closed tabs in visitHistory', () => {
      // Standalone — no workspace
      const tabs = [makeTab(), makeTab(), makeTab()]
      tabs.forEach((t) => useTabStore.getState().addTab(t))
      // Visit: tabs[0] → tabs[1] → tabs[2]
      useTabStore.getState().setActiveTab(tabs[1].id)
      useTabStore.getState().setActiveTab(tabs[2].id)
      // Close tabs[1] (not active) — removed from store but was in history
      useWorkspaceStore.getState().closeTabInWorkspace(tabs[1].id)
      // Close tabs[2] (active) — history has tabs[0] (tabs[1] already cleaned)
      useWorkspaceStore.getState().closeTabInWorkspace(tabs[2].id)
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })

    it('traverses full history stack on repeated closes', () => {
      // Standalone — no workspace
      const tabs = [makeTab(), makeTab(), makeTab(), makeTab()]
      tabs.forEach((t) => useTabStore.getState().addTab(t))
      // Visit: tabs[0] → tabs[1] → tabs[2] → tabs[3]
      useTabStore.getState().setActiveTab(tabs[1].id)
      useTabStore.getState().setActiveTab(tabs[2].id)
      useTabStore.getState().setActiveTab(tabs[3].id)
      // Close tabs[3] → tabs[2], close tabs[2] → tabs[1], close tabs[1] → tabs[0]
      useWorkspaceStore.getState().closeTabInWorkspace(tabs[3].id)
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
      useWorkspaceStore.getState().closeTabInWorkspace(tabs[2].id)
      expect(useTabStore.getState().activeTabId).toBe(tabs[1].id)
      useWorkspaceStore.getState().closeTabInWorkspace(tabs[1].id)
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })
  })

  // === insertTab with afterTabId ===

  describe('insertTab with afterTabId', () => {
    it('inserts tab after specified tab in workspace', () => {
      useWorkspaceStore.getState().addWorkspace('test')
      const wsId = useWorkspaceStore.getState().workspaces[0].id
      useWorkspaceStore.getState().insertTab('a', wsId)
      useWorkspaceStore.getState().insertTab('b', wsId)
      useWorkspaceStore.getState().insertTab('c', wsId)
      // Insert 'x' after 'a'
      useWorkspaceStore.getState().insertTab('x', wsId, 'a')
      const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!
      expect(ws.tabs).toEqual(['a', 'x', 'b', 'c'])
    })

    it('appends if afterTabId not found in workspace', () => {
      useWorkspaceStore.getState().addWorkspace('test')
      const wsId = useWorkspaceStore.getState().workspaces[0].id
      useWorkspaceStore.getState().insertTab('a', wsId)
      useWorkspaceStore.getState().insertTab('x', wsId, 'missing')
      const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!
      expect(ws.tabs).toEqual(['a', 'x'])
    })

    it('afterTabId=null prepends to front', () => {
      useWorkspaceStore.getState().addWorkspace('test')
      const wsId = useWorkspaceStore.getState().workspaces[0].id
      useWorkspaceStore.getState().insertTab('a', wsId)
      useWorkspaceStore.getState().insertTab('b', wsId)
      useWorkspaceStore.getState().insertTab('x', wsId, null)
      const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!
      expect(ws.tabs).toEqual(['x', 'a', 'b'])
    })

    it('afterTabId=undefined still appends (existing behavior)', () => {
      useWorkspaceStore.getState().addWorkspace('test')
      const wsId = useWorkspaceStore.getState().workspaces[0].id
      useWorkspaceStore.getState().insertTab('a', wsId)
      useWorkspaceStore.getState().insertTab('x', wsId)
      const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!
      expect(ws.tabs).toEqual(['a', 'x'])
    })
  })
})
