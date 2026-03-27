import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab, isStandaloneTab } from '../types/tab'

/** Get the tab IDs currently visible in the TabBar (workspace-aware). */
function getVisibleTabIds(): string[] {
  const { tabs, tabOrder, activeTabId } = useTabStore.getState()
  const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()

  // Standalone tab selected — only that tab is visible
  if (activeTabId && isStandaloneTab(activeTabId, workspaces)) {
    return [activeTabId]
  }

  // Active workspace — use its tab order
  const activeWs = workspaces.find((w) => w.id === activeWorkspaceId)
  if (activeWs) {
    return activeWs.tabs.filter((id) => !!tabs[id])
  }

  // Fallback to global tabOrder
  return tabOrder
}

function addToActiveWorkspace(tabId: string): void {
  const wsId = useWorkspaceStore.getState().activeWorkspaceId
  if (wsId) {
    useWorkspaceStore.getState().addTabToWorkspace(wsId, tabId)
    useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, tabId)
  }
}

export function useShortcuts(): void {
  useEffect(() => {
    if (!window.electronAPI?.onShortcut) return

    const cleanup = window.electronAPI.onShortcut(({ action }) => {
      const tabState = useTabStore.getState()
      const visibleIds = getVisibleTabIds()

      if (action.startsWith('switch-tab-')) {
        if (action === 'switch-tab-last') {
          const lastId = visibleIds[visibleIds.length - 1]
          if (lastId) tabState.setActiveTab(lastId)
        } else {
          const index = parseInt(action.replace('switch-tab-', ''), 10) - 1
          const targetId = visibleIds[index]
          if (targetId) tabState.setActiveTab(targetId)
        }
        return
      }

      if (action === 'prev-tab' || action === 'next-tab') {
        if (visibleIds.length === 0) return
        const currentIdx = tabState.activeTabId
          ? visibleIds.indexOf(tabState.activeTabId)
          : -1
        if (currentIdx === -1) {
          // No valid active tab — go to first tab
          tabState.setActiveTab(visibleIds[0])
          return
        }
        const delta = action === 'next-tab' ? 1 : -1
        const nextIdx = (currentIdx + delta + visibleIds.length) % visibleIds.length
        tabState.setActiveTab(visibleIds[nextIdx])
        return
      }

      if (action === 'close-tab') {
        const { activeTabId, tabs } = tabState
        if (!activeTabId) return
        const tab = tabs[activeTabId]
        if (!tab || tab.locked) return
        const wsStore = useWorkspaceStore.getState()
        useHistoryStore.getState().recordClose(tab, wsStore.findWorkspaceByTab(activeTabId)?.id)
        const ws = wsStore.findWorkspaceByTab(activeTabId)
        if (ws) wsStore.removeTabFromWorkspace(ws.id, activeTabId)
        tabState.closeTab(activeTabId)
        return
      }

      if (action === 'new-tab') {
        const tab = createTab({ kind: 'new-tab' })
        tabState.addTab(tab)
        tabState.setActiveTab(tab.id)
        addToActiveWorkspace(tab.id)
        return
      }

      if (action === 'open-settings') {
        const tabId = tabState.openSingletonTab({ kind: 'settings', scope: 'global' })
        addToActiveWorkspace(tabId)
        return
      }

      if (action === 'open-history') {
        const tabId = tabState.openSingletonTab({ kind: 'history' })
        addToActiveWorkspace(tabId)
        return
      }

      if (action === 'reopen-closed-tab') {
        const tab = useHistoryStore.getState().reopenLast()
        if (tab) {
          tabState.addTab(tab)
          tabState.setActiveTab(tab.id)
          addToActiveWorkspace(tab.id)
        }
        return
      }

      if (import.meta.env.DEV) {
        console.warn(`[useShortcuts] unknown action: ${action}`)
      }
    })

    return cleanup
  }, [])
}
