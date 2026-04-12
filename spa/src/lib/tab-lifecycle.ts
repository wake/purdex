import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { destroyBrowserViewIfNeeded } from './browser-cleanup'

export function closeTab(tabId: string, opts?: { skipHistory?: boolean }): void {
  const tab = useTabStore.getState().tabs[tabId]
  if (!tab || tab.locked) return
  destroyBrowserViewIfNeeded(tab)
  useWorkspaceStore.getState().closeTabInWorkspace(tabId, opts)
}
