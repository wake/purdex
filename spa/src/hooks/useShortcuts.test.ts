import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

vi.mock('../features/workspace/generated/icon-loader', () => ({
  ALL_ICON_NAMES: [],
  iconLoaders: {},
}))

import { renderHook } from '@testing-library/react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab } from '../types/tab'
import { useShortcuts } from './useShortcuts'

function mockElectronAPI() {
  let shortcutCallback: ((payload: { action: string }) => void) | null = null
  const cleanup = vi.fn()
  ;(window as unknown as Record<string, unknown>).electronAPI = {
    onShortcut: (cb: (payload: { action: string }) => void) => {
      shortcutCallback = cb
      return cleanup
    },
    signalReady: () => {},
  }
  return {
    fire: (action: string) => shortcutCallback?.({ action }),
    cleanup,
  }
}

function seedTabs(count: number, { addToWorkspace = true } = {}) {
  const store = useTabStore.getState()
  const tabs = Array.from({ length: count }, () =>
    createTab({ kind: 'new-tab' }),
  )
  tabs.forEach((t) => store.addTab(t))
  store.setActiveTab(tabs[0].id)
  if (addToWorkspace) {
    const wsId = useWorkspaceStore.getState().activeWorkspaceId
    if (wsId) {
      tabs.forEach((t) => useWorkspaceStore.getState().addTabToWorkspace(wsId, t.id))
    }
  }
  return tabs
}

describe('useShortcuts', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useWorkspaceStore.getState().reset()
    // Create a default workspace for tests (since reset() now starts empty)
    useWorkspaceStore.getState().addWorkspace('Default')
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
  })

  it('does nothing when electronAPI is not available', () => {
    const { unmount } = renderHook(() => useShortcuts())
    unmount()
  })

  it('cleans up listener on unmount', () => {
    const { cleanup } = mockElectronAPI()
    const { unmount } = renderHook(() => useShortcuts())
    unmount()
    expect(cleanup).toHaveBeenCalled()
  })

  describe('switch-tab-{n}', () => {
    it('switches to tab by index', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(5)
      renderHook(() => useShortcuts())

      fire('switch-tab-3')
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
    })

    it('ignores out-of-range index', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(2)
      useTabStore.getState().setActiveTab(tabs[0].id)
      renderHook(() => useShortcuts())

      fire('switch-tab-5')
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })

    it('uses workspace tab order instead of global tabOrder', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(4, { addToWorkspace: false })
      // Add only tabs[2] and tabs[0] to workspace (reversed vs global order)
      const wsId = useWorkspaceStore.getState().activeWorkspaceId!
      useWorkspaceStore.getState().addTabToWorkspace(wsId, tabs[2].id)
      useWorkspaceStore.getState().addTabToWorkspace(wsId, tabs[0].id)
      useTabStore.getState().setActiveTab(tabs[2].id)
      renderHook(() => useShortcuts())

      // Cmd+1 should switch to workspace's first tab (tabs[2]), not global first (tabs[0])
      fire('switch-tab-1')
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
      // Cmd+2 should switch to workspace's second tab (tabs[0])
      fire('switch-tab-2')
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })
  })

  describe('switch-tab-last', () => {
    it('switches to the last tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(4)
      renderHook(() => useShortcuts())

      fire('switch-tab-last')
      expect(useTabStore.getState().activeTabId).toBe(tabs[3].id)
    })
  })

  describe('prev-tab / next-tab', () => {
    it('cycles to previous tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[1].id)
      renderHook(() => useShortcuts())

      fire('prev-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })

    it('wraps around from first to last', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[0].id)
      renderHook(() => useShortcuts())

      fire('prev-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
    })

    it('cycles to next tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[0].id)
      renderHook(() => useShortcuts())

      fire('next-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[1].id)
    })

    it('wraps around from last to first', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[2].id)
      renderHook(() => useShortcuts())

      fire('next-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })

    it('goes to first tab when activeTabId is not in visible tabs', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(null)
      renderHook(() => useShortcuts())

      fire('prev-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)

      useTabStore.getState().setActiveTab(null)
      fire('next-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })
  })

  describe('close-tab', () => {
    it('closes the active tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[1].id)
      renderHook(() => useShortcuts())

      fire('close-tab')
      expect(useTabStore.getState().tabs[tabs[1].id]).toBeUndefined()
    })

    it('does not close a locked tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(2)
      useTabStore.getState().toggleLock(tabs[0].id)
      useTabStore.getState().setActiveTab(tabs[0].id)
      renderHook(() => useShortcuts())

      fire('close-tab')
      expect(useTabStore.getState().tabs[tabs[0].id]).toBeDefined()
    })

    it('does not close tabs from another workspace', () => {
      const { fire } = mockElectronAPI()
      // Setup: WS A has 2 tabs, WS B has 0 tabs
      const tabsA = seedTabs(2)
      // Switch to a new empty workspace B
      const wsB = useWorkspaceStore.getState().addWorkspace('WS B')
      useWorkspaceStore.getState().setActiveWorkspace(wsB.id)
      // activeTabId still points to WS A's tab (stale)
      renderHook(() => useShortcuts())

      fire('close-tab')
      // WS A's tabs should be untouched
      expect(useTabStore.getState().tabs[tabsA[0].id]).toBeDefined()
      expect(useTabStore.getState().tabs[tabsA[1].id]).toBeDefined()
    })

    it('selects last-visited tab within workspace after closing', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      // seedTabs sets activeTab to tabs[0], then we switch to tabs[1]
      // visitHistory = [tabs[0]]
      useTabStore.getState().setActiveTab(tabs[1].id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(
        useWorkspaceStore.getState().activeWorkspaceId!,
        tabs[1].id,
      )
      renderHook(() => useShortcuts())

      fire('close-tab')
      const state = useTabStore.getState()
      const wsId = useWorkspaceStore.getState().activeWorkspaceId!
      const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!
      // Should go back to tabs[0] (last visited), not tabs[2] (adjacent)
      expect(state.activeTabId).toBe(tabs[0].id)
      expect(ws.tabs).toContain(state.activeTabId)
      expect(ws.activeTabId).toBe(tabs[0].id)
    })

    it('closes last tab in workspace → activeTabId null', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(1)
      useTabStore.getState().setActiveTab(tabs[0].id)
      renderHook(() => useShortcuts())

      fire('close-tab')
      expect(useTabStore.getState().activeTabId).toBeNull()
      expect(useTabStore.getState().tabs[tabs[0].id]).toBeUndefined()
    })
  })

  describe('new-tab', () => {
    it('creates a new tab and activates it', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      renderHook(() => useShortcuts())

      const beforeCount = useTabStore.getState().tabOrder.length
      fire('new-tab')
      const state = useTabStore.getState()
      expect(state.tabOrder.length).toBe(beforeCount + 1)
      const newTabId = state.tabOrder[state.tabOrder.length - 1]
      expect(state.activeTabId).toBe(newTabId)
    })

    it('adds new tab to active workspace', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      renderHook(() => useShortcuts())

      fire('new-tab')
      const wsId = useWorkspaceStore.getState().activeWorkspaceId
      const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)
      const newTabId = useTabStore.getState().activeTabId!
      expect(ws?.tabs).toContain(newTabId)
    })
  })

  describe('open-settings', () => {
    it('opens a settings singleton tab', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      renderHook(() => useShortcuts())

      fire('open-settings')
      const state = useTabStore.getState()
      const settingsTab = Object.values(state.tabs).find((t) => {
        const pane = t.layout
        return pane.type === 'leaf' && pane.pane.content.kind === 'settings'
      })
      expect(settingsTab).toBeDefined()
      expect(state.activeTabId).toBe(settingsTab!.id)
    })
  })

  describe('open-history', () => {
    it('opens a history singleton tab', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      renderHook(() => useShortcuts())

      fire('open-history')
      const state = useTabStore.getState()
      const historyTab = Object.values(state.tabs).find((t) => {
        const pane = t.layout
        return pane.type === 'leaf' && pane.pane.content.kind === 'history'
      })
      expect(historyTab).toBeDefined()
      expect(state.activeTabId).toBe(historyTab!.id)
    })
  })

  describe('reopen-closed-tab', () => {
    it('reopens the last closed tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(2)
      const closedTab = tabs[1]
      useHistoryStore.getState().recordClose(closedTab)
      useTabStore.getState().closeTab(closedTab.id)
      renderHook(() => useShortcuts())

      fire('reopen-closed-tab')
      expect(useTabStore.getState().tabs[closedTab.id]).toBeDefined()
      expect(useTabStore.getState().activeTabId).toBe(closedTab.id)
    })

    it('adds reopened tab to active workspace', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(2)
      const wsId = useWorkspaceStore.getState().activeWorkspaceId!
      useWorkspaceStore.getState().addTabToWorkspace(wsId, tabs[0].id)
      useWorkspaceStore.getState().addTabToWorkspace(wsId, tabs[1].id)

      const closedTab = tabs[1]
      useHistoryStore.getState().recordClose(closedTab)
      useTabStore.getState().closeTab(closedTab.id)
      renderHook(() => useShortcuts())

      fire('reopen-closed-tab')
      const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)
      expect(ws?.tabs).toContain(closedTab.id)
    })

    it('reopens tab into current workspace, not original workspace', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(2)
      const wsAId = useWorkspaceStore.getState().activeWorkspaceId!

      // Close a tab from WS A
      const closedTab = tabs[1]
      useHistoryStore.getState().recordClose(closedTab, wsAId)
      useWorkspaceStore.getState().removeTabFromWorkspace(wsAId, closedTab.id)
      useTabStore.getState().closeTab(closedTab.id)

      // Switch to WS B
      const wsB = useWorkspaceStore.getState().addWorkspace('WS B')
      useWorkspaceStore.getState().setActiveWorkspace(wsB.id)
      renderHook(() => useShortcuts())

      // Reopen — should go to WS B (current), not WS A (original)
      fire('reopen-closed-tab')
      const wsBState = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsB.id)
      const wsAState = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsAId)
      expect(wsBState?.tabs).toContain(closedTab.id)
      expect(wsAState?.tabs).not.toContain(closedTab.id)
    })
  })

  describe('switch-workspace-{n}', () => {
    it('switches to workspace by index', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
      useWorkspaceStore.getState().addTabToWorkspace(ws2.id, seedTabs(1, { addToWorkspace: false })[0].id)
      renderHook(() => useShortcuts())

      fire('switch-workspace-2')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
    })

    it('ignores out-of-range workspace index', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      const currentWsId = useWorkspaceStore.getState().activeWorkspaceId
      renderHook(() => useShortcuts())

      fire('switch-workspace-5')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(currentWsId)
    })

    it('does nothing with 0 workspaces', () => {
      const { fire } = mockElectronAPI()
      useWorkspaceStore.getState().reset()
      renderHook(() => useShortcuts())

      fire('switch-workspace-1')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
    })
  })

  describe('prev-workspace / next-workspace', () => {
    it('cycles to next workspace', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
      renderHook(() => useShortcuts())

      fire('next-workspace')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
    })

    it('wraps from last to first workspace', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      const ws1Id = useWorkspaceStore.getState().activeWorkspaceId!
      const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
      useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
      renderHook(() => useShortcuts())

      fire('next-workspace')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1Id)
    })

    it('cycles to prev workspace', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      const ws1Id = useWorkspaceStore.getState().activeWorkspaceId!
      const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
      useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
      renderHook(() => useShortcuts())

      fire('prev-workspace')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1Id)
    })

    it('does nothing with 0 workspaces', () => {
      const { fire } = mockElectronAPI()
      useWorkspaceStore.getState().reset()
      renderHook(() => useShortcuts())

      fire('next-workspace')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
    })
  })
})
