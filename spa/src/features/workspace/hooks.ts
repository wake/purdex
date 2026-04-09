import { useState, useCallback } from 'react'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from './store'
import { createTab } from '../../types/tab'
import { getPrimaryPane } from '../../lib/pane-tree'
import { renameSession } from '../../lib/host-api'
import { destroyBrowserViewIfNeeded } from '../../lib/browser-cleanup'
import type { Tab } from '../../types/tab'
import type { ContextMenuAction } from '../../components/TabContextMenu'

export function useTabWorkspaceActions(displayTabs: Tab[]) {
  const [contextMenu, setContextMenu] = useState<{ tab: Tab; position: { x: number; y: number } } | null>(null)
  const [renameTarget, setRenameTarget] = useState<{ tabId: string; hostId: string; sessionCode: string; currentName: string; anchorRect: DOMRect } | null>(null)
  const [renameError, setRenameError] = useState<string | undefined>()

  // Tab store
  const tabs = useTabStore((s) => s.tabs)
  const setActiveTab = useTabStore((s) => s.setActiveTab)
  const addTab = useTabStore((s) => s.addTab)
  const reorderTabs = useTabStore((s) => s.reorderTabs)

  // Workspace store
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)
  const setActiveWorkspace = useWorkspaceStore((s) => s.setActiveWorkspace)
  const findWorkspaceByTab = useWorkspaceStore((s) => s.findWorkspaceByTab)
  const setWorkspaceActiveTab = useWorkspaceStore((s) => s.setWorkspaceActiveTab)
  const reorderWorkspaceTabs = useWorkspaceStore((s) => s.reorderWorkspaceTabs)

  const handleSelectWorkspace = useCallback((wsId: string) => {
    setActiveWorkspace(wsId)
    const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)
    const allTabs = useTabStore.getState().tabs
    if (ws?.activeTabId && allTabs[ws.activeTabId]) setActiveTab(ws.activeTabId)
    else if (ws?.tabs[0]) setActiveTab(ws.tabs[0])
  }, [setActiveWorkspace, setActiveTab])

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
    if (!tab || tab.locked) return
    destroyBrowserViewIfNeeded(tab)
    useWorkspaceStore.getState().closeTabInWorkspace(tabId)

    // Clear rename popover if the renamed tab was closed
    if (renameTarget?.tabId === tabId) {
      setRenameTarget(null)
      setRenameError(undefined)
    }
  }, [tabs, renameTarget])

  const handleAddTab = useCallback(() => {
    const tab = createTab({ kind: 'new-tab' })
    addTab(tab)
    setActiveTab(tab.id)
    useWorkspaceStore.getState().insertTab(tab.id)
  }, [addTab, setActiveTab])

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

  const handleContextAction = useCallback((action: ContextMenuAction, payload?: string) => {
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
      case 'rename': {
        const primary = getPrimaryPane(tab.layout)
        const c = primary.content
        if (c.kind !== 'tmux-session' || c.terminated) break
        const tabEl = document.querySelector(`[data-tab-id="${tab.id}"]`)
        if (!tabEl) break
        const rect = tabEl.getBoundingClientRect()
        setRenameTarget({
          tabId: tab.id,
          hostId: c.hostId,
          sessionCode: c.sessionCode,
          currentName: c.cachedName || c.sessionCode,
          anchorRect: rect,
        })
        setRenameError(undefined)
        break
      }
      case 'mergeToTab': {
        if (!payload) break
        const sourceTab = tabs[tab.id]
        const targetTab = tabs[payload]
        if (!sourceTab || !targetTab) break
        const sourcePrimary = getPrimaryPane(sourceTab.layout)
        const targetPrimary = getPrimaryPane(targetTab.layout)
        useTabStore.getState().splitPane(payload, targetPrimary.id, 'h', sourcePrimary.content)
        handleCloseTab(tab.id)
        break
      }
    }
  }, [contextMenu, tabs, displayTabs, handleCloseTab])

  const handleRenameConfirm = useCallback(async (name: string) => {
    if (!renameTarget) return
    try {
      const res = await renameSession(renameTarget.hostId, renameTarget.sessionCode, name)
      if (!res.ok) {
        const text = await res.text().catch(() => 'Unknown error')
        setRenameError(text)
        return
      }
      // Immediately update tab label (don't wait for WS session event)
      useTabStore.getState().updateSessionCache(renameTarget.hostId, renameTarget.sessionCode, name)
      setRenameTarget(null)
      setRenameError(undefined)
    } catch (err) {
      setRenameError(err instanceof Error ? err.message : 'Unknown error')
    }
  }, [renameTarget])

  const handleRenameCancel = useCallback(() => {
    setRenameTarget(null)
    setRenameError(undefined)
  }, [])

  const handleClearRenameError = useCallback(() => {
    setRenameError(undefined)
  }, [])

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
    renameTarget,
    renameError,
    handleRenameConfirm,
    handleRenameCancel,
    handleClearRenameError,
  }
}
