import { useState, useCallback } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'
import type { Tab } from '../types/tab'
import type { ContextMenuAction } from '../components/TabContextMenu'

export function useTabWorkspaceActions(displayTabs: Tab[]) {
  const [contextMenu, setContextMenu] = useState<{ tab: Tab; position: { x: number; y: number } } | null>(null)

  // Tab store
  const tabs = useTabStore((s) => s.tabs)
  const setActiveTab = useTabStore((s) => s.setActiveTab)
  const addTab = useTabStore((s) => s.addTab)
  const closeTab = useTabStore((s) => s.closeTab)
  const reorderTabs = useTabStore((s) => s.reorderTabs)

  // Workspace store
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)
  const setActiveWorkspace = useWorkspaceStore((s) => s.setActiveWorkspace)
  const removeTabFromWorkspace = useWorkspaceStore((s) => s.removeTabFromWorkspace)
  const findWorkspaceByTab = useWorkspaceStore((s) => s.findWorkspaceByTab)
  const setWorkspaceActiveTab = useWorkspaceStore((s) => s.setWorkspaceActiveTab)
  const reorderWorkspaceTabs = useWorkspaceStore((s) => s.reorderWorkspaceTabs)

  const handleSelectWorkspace = useCallback((wsId: string) => {
    setActiveWorkspace(wsId)
    const ws = workspaces.find((w) => w.id === wsId)
    if (ws?.activeTabId) setActiveTab(ws.activeTabId)
    else if (ws?.tabs[0]) setActiveTab(ws.tabs[0])
  }, [workspaces, setActiveWorkspace, setActiveTab])

  const handleSelectTab = useCallback((tabId: string) => {
    setActiveTab(tabId)
    const ws = findWorkspaceByTab(tabId)
    if (ws) {
      setActiveWorkspace(ws.id)
      setWorkspaceActiveTab(ws.id, tabId)
    }

    // markRead is handled by the cross-store subscription in active-session.ts
  }, [setActiveTab, findWorkspaceByTab, setActiveWorkspace, setWorkspaceActiveTab])

  const handleCloseTab = useCallback((tabId: string) => {
    const tab = tabs[tabId]
    if (!tab || tab.locked) return // locked guard
    useHistoryStore.getState().recordClose(tab, findWorkspaceByTab(tabId)?.id)
    const ws = findWorkspaceByTab(tabId)
    if (ws) removeTabFromWorkspace(ws.id, tabId)
    closeTab(tabId)
  }, [tabs, findWorkspaceByTab, removeTabFromWorkspace, closeTab])

  const handleAddTab = useCallback(() => {
    const tab = createTab({ kind: 'new-tab' })
    addTab(tab)
    setActiveTab(tab.id)
    if (activeWorkspaceId) {
      useWorkspaceStore.getState().addTabToWorkspace(activeWorkspaceId, tab.id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(activeWorkspaceId, tab.id)
    }
  }, [addTab, setActiveTab, activeWorkspaceId])

  const handleReorderTabs = useCallback((order: string[]) => {
    if (activeWorkspaceId) {
      reorderWorkspaceTabs(activeWorkspaceId, order)
    } else {
      // Standalone tabs — update global order
      reorderTabs(order)
    }
  }, [reorderTabs, activeWorkspaceId, reorderWorkspaceTabs])

  const handleContextMenu = useCallback((e: React.MouseEvent, tabId: string) => {
    e.preventDefault()
    const tab = tabs[tabId]
    if (tab) setContextMenu({ tab, position: { x: e.clientX, y: e.clientY } })
  }, [tabs])

  const handleMiddleClick = useCallback((tabId: string) => {
    const tab = tabs[tabId]
    if (tab && !tab.locked) handleCloseTab(tabId)
  }, [tabs, handleCloseTab])

  const handleContextAction = useCallback((action: ContextMenuAction) => {
    if (!contextMenu) return
    const { tab } = contextMenu
    const store = useTabStore.getState()
    const primaryPaneId = getPrimaryPane(tab.layout).id
    switch (action) {
      case 'viewMode-terminal': store.setViewMode(tab.id, primaryPaneId, 'terminal'); break
      case 'viewMode-stream': store.setViewMode(tab.id, primaryPaneId, 'stream'); break
      case 'lock': case 'unlock': store.toggleLock(tab.id); break
      case 'pin': case 'unpin': store.togglePin(tab.id); break
      case 'close': handleCloseTab(tab.id); break
      case 'closeOthers': {
        const displayIds = displayTabs.map((t) => t.id)
        const toClose = displayIds.filter((id) => id !== tab.id && !tabs[id]?.locked)
        toClose.forEach((id) => handleCloseTab(id))
        break
      }
      case 'closeRight': {
        const displayIds = displayTabs.map((t) => t.id)
        const idx = displayIds.indexOf(tab.id)
        if (idx === -1) break
        const toClose = displayIds.slice(idx + 1).filter((id) => !tabs[id]?.locked)
        toClose.forEach((id) => handleCloseTab(id))
        break
      }
      case 'tearOff': {
        if (!window.electronAPI) break
        const tabData = tabs[tab.id]
        if (!tabData) break
        // Must remove tab BEFORE IPC to avoid duplication if locked
        handleCloseTab(tab.id)
        // Only send to new window if tab was actually removed
        if (!useTabStore.getState().tabs[tab.id]) {
          window.electronAPI.tearOffTab(JSON.stringify(tabData))
        }
        break
      }
    }
  }, [contextMenu, tabs, displayTabs, handleCloseTab])

  // Context menu derived state
  const contextMenuHasRightUnlocked = (() => {
    if (!contextMenu) return false
    const ids = displayTabs.map((t) => t.id)
    const idx = ids.indexOf(contextMenu.tab.id)
    return idx !== -1 && ids.slice(idx + 1).some((id) => !tabs[id]?.locked)
  })()

  return {
    contextMenu,
    setContextMenu,
    contextMenuHasRightUnlocked,
    handleSelectWorkspace,
    handleSelectTab,
    handleCloseTab,
    handleAddTab,
    handleReorderTabs,
    handleContextMenu,
    handleMiddleClick,
    handleContextAction,
  }
}
