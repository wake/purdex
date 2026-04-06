import { isStandaloneTab, type Workspace } from '../../../types/tab'

interface GetVisibleTabIdsParams {
  tabs: Record<string, unknown>
  tabOrder: string[]
  activeTabId: string | null
  workspaces: Workspace[]
  activeWorkspaceId: string | null
}

/**
 * Get the tab IDs currently visible in the TabBar (workspace-aware).
 *
 * Rules:
 * 1. Active standalone tab selected → only that tab
 * 2. Active workspace → that workspace's tabs (filtered by existence in tab store)
 * 3. No workspace (0 workspaces or null activeWorkspaceId) → fallback to tabOrder
 */
export function getVisibleTabIds(params: GetVisibleTabIdsParams): string[] {
  const { tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId } = params

  // Only apply workspace/standalone logic when the workspace system is active
  if (workspaces.length > 0) {
    // Standalone tab selected — only that tab is visible
    if (activeTabId && isStandaloneTab(activeTabId, workspaces)) {
      return [activeTabId]
    }

    // Active workspace — use its tab order
    const activeWs = workspaces.find((w) => w.id === activeWorkspaceId)
    if (activeWs) {
      return activeWs.tabs.filter((id) => !!tabs[id])
    }
  }

  // Fallback to global tabOrder (no workspaces, or no activeWorkspaceId)
  return tabOrder
}
