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
 * 3. Home mode (workspaces exist but activeWorkspaceId is null) → standalone tabs only
 * 4. No workspace system (0 workspaces) → fallback to tabOrder
 */
export function getVisibleTabIds(params: GetVisibleTabIdsParams): string[] {
  const { tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId } = params

  // Only apply workspace/standalone logic when the workspace system is active
  if (workspaces.length > 0) {
    // Home mode (no active workspace) — show all standalone tabs
    if (!activeWorkspaceId) {
      return tabOrder.filter((id) => isStandaloneTab(id, workspaces))
    }

    // Standalone tab selected while viewing a workspace — only that tab
    if (activeTabId && isStandaloneTab(activeTabId, workspaces)) {
      return [activeTabId]
    }

    // Active workspace — use its tab order
    const activeWs = workspaces.find((w) => w.id === activeWorkspaceId)
    if (activeWs) {
      return activeWs.tabs.filter((id) => !!tabs[id])
    }
  }

  // No workspace system — show all tabs
  return tabOrder
}
