import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab } from '../types/tab'
import { getVisibleTabIds as getVisibleTabIdsShared } from '../features/workspace'
import { destroyBrowserViewIfNeeded } from '../lib/browser-cleanup'
import { getTabShortcutHandler } from '../lib/tab-shortcut-registry'
import { getPrimaryPane } from '../lib/pane-tree'

export function useShortcuts(): void {
  useEffect(() => {
    if (!window.electronAPI?.onShortcut) return

    const cleanup = window.electronAPI.onShortcut(({ action }) => {
      const tabState = useTabStore.getState()

      // Set active tab and sync workspace activeTabId in one step
      const activateTab = (tabId: string) => {
        tabState.setActiveTab(tabId)
        const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
        if (ws && ws.activeTabId !== tabId) useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabId)
      }

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
          if (lastId) activateTab(lastId)
        } else {
          const index = parseInt(action.replace('switch-tab-', ''), 10) - 1
          const targetId = visibleIds[index]
          if (targetId) activateTab(targetId)
        }
        return
      }

      if (action === 'prev-tab' || action === 'next-tab') {
        if (visibleIds.length === 0) return
        const currentIdx = tabState.activeTabId
          ? visibleIds.indexOf(tabState.activeTabId)
          : -1
        if (currentIdx === -1) {
          activateTab(visibleIds[0])
          return
        }
        const delta = action === 'next-tab' ? 1 : -1
        const nextIdx = (currentIdx + delta + visibleIds.length) % visibleIds.length
        activateTab(visibleIds[nextIdx])
        return
      }

      if (action === 'close-tab') {
        const { activeTabId, tabs } = tabState
        if (!activeTabId || !visibleIds.includes(activeTabId)) {
          // No active/visible tab — ask to close the window if it's empty
          const allEmpty = tabState.tabOrder.length === 0
            || useWorkspaceStore.getState().workspaces.every((ws) => ws.tabs.length === 0)
          if (allEmpty) window.electronAPI?.closeWindow()
          return
        }
        const tab = tabs[activeTabId]
        if (!tab || tab.locked) return
        destroyBrowserViewIfNeeded(tab)
        useWorkspaceStore.getState().closeTabInWorkspace(activeTabId)
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

      if (action === 'open-hosts') {
        const tabId = tabState.openSingletonTab({ kind: 'hosts' })
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

      if (action.startsWith('switch-workspace-')) {
        const workspaces = useWorkspaceStore.getState().workspaces
        if (workspaces.length === 0) return
        const index = parseInt(action.replace('switch-workspace-', ''), 10) - 1
        const targetWs = workspaces[index]
        if (!targetWs) return
        useWorkspaceStore.getState().setActiveWorkspace(targetWs.id)
        const activeTab = targetWs.activeTabId ?? targetWs.tabs[0]
        if (activeTab) activateTab(activeTab)
        return
      }

      if (action === 'prev-workspace' || action === 'next-workspace') {
        const workspaces = useWorkspaceStore.getState().workspaces
        if (workspaces.length === 0) return
        const currentWsId = useWorkspaceStore.getState().activeWorkspaceId
        const currentIdx = workspaces.findIndex((w) => w.id === currentWsId)
        const delta = action === 'next-workspace' ? 1 : -1
        const nextIdx = currentIdx === -1
          ? (delta > 0 ? 0 : workspaces.length - 1)
          : (currentIdx + delta + workspaces.length) % workspaces.length
        const targetWs = workspaces[nextIdx]
        useWorkspaceStore.getState().setActiveWorkspace(targetWs.id)
        const activeTab = targetWs.activeTabId ?? targetWs.tabs[0]
        if (activeTab) activateTab(activeTab)
        return
      }

      // Tab-level shortcut dispatch via registry
      const { activeTabId, tabs } = tabState
      if (activeTabId) {
        const tab = tabs[activeTabId]
        if (tab) {
          const pane = getPrimaryPane(tab.layout)
          const handler = getTabShortcutHandler(pane.content.kind, action)
          if (handler) {
            handler(tab, pane)
            return
          }
        }
      }

      if (import.meta.env.DEV) {
        console.warn(`[useShortcuts] unknown action: ${action}`)
      }
    })

    return cleanup
  }, [])
}
