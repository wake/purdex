import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'

export function useShortcuts(): void {
  useEffect(() => {
    if (!window.electronAPI?.onShortcut) return

    const cleanup = window.electronAPI.onShortcut(({ action }) => {
      const tabState = useTabStore.getState()
      const { tabOrder } = tabState

      if (action.startsWith('switch-tab-')) {
        if (action === 'switch-tab-last') {
          const lastId = tabOrder[tabOrder.length - 1]
          if (lastId) tabState.setActiveTab(lastId)
        } else {
          const index = parseInt(action.replace('switch-tab-', ''), 10) - 1
          const targetId = tabOrder[index]
          if (targetId) tabState.setActiveTab(targetId)
        }
        return
      }

      if (action === 'prev-tab' || action === 'next-tab') {
        if (tabOrder.length === 0) return
        const currentIdx = tabState.activeTabId
          ? tabOrder.indexOf(tabState.activeTabId)
          : -1
        const delta = action === 'next-tab' ? 1 : -1
        const nextIdx = (currentIdx + delta + tabOrder.length) % tabOrder.length
        tabState.setActiveTab(tabOrder[nextIdx])
        return
      }

      if (action === 'open-settings') {
        const tabId = tabState.openSingletonTab({ kind: 'settings', scope: 'global' })
        const wsId = useWorkspaceStore.getState().activeWorkspaceId
        if (wsId) {
          useWorkspaceStore.getState().addTabToWorkspace(wsId, tabId)
          useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, tabId)
        }
        return
      }

      if (action === 'open-history') {
        const tabId = tabState.openSingletonTab({ kind: 'history' })
        const wsId = useWorkspaceStore.getState().activeWorkspaceId
        if (wsId) {
          useWorkspaceStore.getState().addTabToWorkspace(wsId, tabId)
          useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, tabId)
        }
        return
      }

      if (action === 'reopen-closed-tab') {
        const tab = useHistoryStore.getState().reopenLast()
        if (tab) {
          tabState.addTab(tab)
          tabState.setActiveTab(tab.id)
        }
        return
      }
    })

    return cleanup
  }, [])
}
