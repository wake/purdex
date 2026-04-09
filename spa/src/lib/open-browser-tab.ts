import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { createTab } from '../types/tab'

/**
 * Open a new browser tab with the given URL.
 * Integrates with workspace: adds to active workspace and sets as active tab.
 * Can be called from anywhere (not a hook — uses store.getState() directly).
 */
export function openBrowserTab(url: string): void {
  const tab = createTab({ kind: 'browser', url })
  const activeTabId = useTabStore.getState().activeTabId
  useTabStore.getState().addTab(tab, activeTabId ?? undefined)
  useTabStore.getState().setActiveTab(tab.id)

  const wsId = useWorkspaceStore.getState().activeWorkspaceId
  if (wsId) {
    useWorkspaceStore.getState().addTabToWorkspace(wsId, tab.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, tab.id)
  }
}
