import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab } from '../types/tab'
import { getVisibleTabIds as getVisibleTabIdsShared } from '../features/workspace'

export function useShortcuts(): void {
  useEffect(() => {
    if (!window.electronAPI?.onShortcut) return

    const cleanup = window.electronAPI.onShortcut(({ action }) => {
      const tabState = useTabStore.getState()
      const visibleIds = getVisibleTabIdsShared({
        tabs: tabState.tabs,
        tabOrder: tabState.tabOrder,
        activeTabId: tabState.activeTabId,
        workspaces: useWorkspaceStore.getState().workspaces,
        activeWorkspaceId: useWorkspaceStore.getState().activeWorkspaceId,
      })

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
        useWorkspaceStore.getState().insertTab(tab.id)
        return
      }

      if (action === 'open-settings') {
        const tabId = tabState.openSingletonTab({ kind: 'settings', scope: 'global' })
        useWorkspaceStore.getState().insertTab(tabId)
        return
      }

      if (action === 'open-history') {
        const tabId = tabState.openSingletonTab({ kind: 'history' })
        useWorkspaceStore.getState().insertTab(tabId)
        return
      }

      if (action === 'reopen-closed-tab') {
        const tab = useHistoryStore.getState().reopenLast()
        if (tab) {
          tabState.addTab(tab)
          tabState.setActiveTab(tab.id)
          useWorkspaceStore.getState().insertTab(tab.id)
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
