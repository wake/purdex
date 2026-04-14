import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { createTab } from '../types/tab'
import { findBrowserInsertTarget } from './find-browser-insert-target'

/**
 * Open a new browser tab with the given URL.
 * Inserts after the nearest browser tab to the right of the active tab.
 * Falls back to inserting after the active tab if no browser tab is found.
 */
export function openBrowserTab(url: string): void {
  const tab = createTab({ kind: 'browser', url })
  const tabState = useTabStore.getState()
  const wsState = useWorkspaceStore.getState()
  const activeTabId = tabState.activeTabId

  const wsId = wsState.activeWorkspaceId
  const ws = wsId ? wsState.workspaces.find((w) => w.id === wsId) : null
  const visibleOrder = ws ? ws.tabs.filter((id) => !!tabState.tabs[id]) : tabState.tabOrder

  const afterTabId = activeTabId
    ? findBrowserInsertTarget(visibleOrder, activeTabId, tabState.tabs)
    : undefined

  useTabStore.getState().addTab(tab, afterTabId)
  useTabStore.getState().setActiveTab(tab.id)

  if (wsId) {
    wsState.insertTab(tab.id, wsId, afterTabId)
  }
}
