import type { Workspace } from '../../../types/tab'

interface GetVisibleTabIdsParams {
  tabs: Record<string, unknown>
  tabOrder: string[]
  workspaces: Workspace[]
  activeWorkspaceId: string | null
}

/**
 * Get the tab IDs currently visible in the TabBar (workspace-aware).
 *
 * Rules:
 * 1. Active workspace → that workspace's tabs (filtered by existence in tab store)
 * 2. No active workspace → fallback to tabOrder. That is zero workspaces, or the moment before
 *    `adopt-standalone.ts` re-points an `activeWorkspaceId` of `null` — there is no "Home" view of
 *    workspace-less tabs any more: every tab belongs to a workspace (Profile Sync spec §4.3).
 *
 * A tab nobody has adopted yet is in no workspace's list, so it is not in the bar for that moment
 * even when it is the active tab; the content area shows it all the same.
 */
export function getVisibleTabIds(params: GetVisibleTabIdsParams): string[] {
  const { tabs, tabOrder, workspaces, activeWorkspaceId } = params

  // Active workspace — use its tab order
  const activeWs = activeWorkspaceId ? workspaces.find((w) => w.id === activeWorkspaceId) : undefined
  if (activeWs) {
    return activeWs.tabs.filter((id) => !!tabs[id])
  }

  // No active workspace — show all tabs
  return tabOrder
}
