import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab } from '../types/tab'
import { getVisibleTabIds as getVisibleTabIdsShared } from '../features/workspace'
import { closeTab } from '../lib/tab-lifecycle'
import { getTabShortcutHandler } from '../lib/tab-shortcut-registry'
import { collectLeaves, getPrimaryPane } from '../lib/pane-tree'
import { useHostStore } from '../stores/useHostStore'
import { hostRefOf, landOnHostsPageIfHidden } from '../lib/shown-hosts'
import { hasLocalSlaves, useProfileSwitcherStore } from '../stores/useProfileSwitcherStore'

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
        const { activeTabId } = tabState
        if (!activeTabId || !tabState.tabs[activeTabId]) return
        // In the bar — or in NO bar: a tab nobody has adopted yet (adopt-standalone.ts waits 500 ms) has no
        // close button anywhere, so this shortcut is the only way out. A stale pointer at a tab of ANOTHER
        // workspace is still left alone. `closeTab` refuses a locked tab.
        const ownedElsewhere = !visibleIds.includes(activeTabId)
          && useWorkspaceStore.getState().findWorkspaceByTab(activeTabId) !== null
        if (ownedElsewhere) return
        closeTab(activeTabId)
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
        // Host ownership H2d-3: reopening opens the closed tab's hosts. Peek at the record `reopenLast` would take
        // (the latest not yet reopened) WITHOUT consuming it: a host-bearing pane on a host hidden in this workbench →
        // the Hosts page on that host, nothing reopened or focused, and the record stays for when the host is shown.
        const { closedTabs } = useHistoryStore.getState()
        const next = closedTabs.findLast((r) => r.reopenedAt === undefined)
        if (next) {
          const { hostOrder } = useHostStore.getState()
          for (const pane of collectLeaves(next.tab.layout)) {
            const ref = hostRefOf(pane.content, hostOrder)
            if (ref !== null && landOnHostsPageIfHidden(ref)) return
          }
        }
        const tab = useHistoryStore.getState().reopenLast()
        if (tab) {
          tabState.addTab(tab)
          tabState.setActiveTab(tab.id)
          useWorkspaceStore.getState().insertTab(tab.id)
        }
        return
      }

      // Home is the profile switcher on a device that has a local profile (a slave): the shortcut opens the
      // Home button's menu, which takes focus. Otherwise Home = the first workspace, as ever — there is no
      // "no workspace" view: every tab belongs to a workspace (Profile Sync spec §4.3).
      if (action === 'switch-workspace-home') {
        if (hasLocalSlaves()) {
          useProfileSwitcherStore.getState().setOpen(true)
          return
        }
        const first = useWorkspaceStore.getState().workspaces[0]
        if (!first) return
        useWorkspaceStore.getState().setActiveWorkspace(first.id)
        const nextTab = first.activeTabId && tabState.tabs[first.activeTabId] ? first.activeTabId : first.tabs[0]
        if (nextTab) activateTab(nextTab)
        else tabState.setActiveTab(null)
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
        const wsStore = useWorkspaceStore.getState()
        const workspaces = wsStore.workspaces
        if (workspaces.length === 0) return
        // Block navigation when in Home mode
        if (wsStore.activeWorkspaceId === null) return
        const currentIdx = workspaces.findIndex((w) => w.id === wsStore.activeWorkspaceId)
        const delta = action === 'next-workspace' ? 1 : -1
        const nextIdx = (currentIdx + delta + workspaces.length) % workspaces.length
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
