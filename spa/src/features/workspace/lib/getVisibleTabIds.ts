import type { Workspace } from '../../../types/tab'
import { WATCH_WORKSPACE_ID } from '../../../lib/watch/ids'

interface GetVisibleTabIdsParams {
  tabs: Record<string, unknown>
  tabOrder: string[]
  activeTabId: string | null
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  /** The task-group view's range (lib/task-groups/range.ts): the selected group's tabs; null/absent = the workspace rules. */
  groupRange?: string[] | null
}

/**
 * Get the tab IDs currently visible in the TabBar (workspace-aware). It is also the RANGE close-others /
 * close-right and the tab shortcuts act on, so it is never wider than one workspace while one exists.
 *
 * Rules:
 * 1. Active workspace → that workspace's tabs (filtered by existence in tab store)
 * 2. No active workspace, but workspaces exist — the moment before `adopt-standalone.ts` re-points an
 *    `activeWorkspaceId` of `null` (boot, an unsettled world) → the workspace that owns the active tab, else
 *    the first one: what that re-pointing will show, so the bar does not flash every tab, and "close others"
 *    cannot reach into another workspace.
 * 3. Zero workspaces → tabOrder (every tab is in the one range there is).
 * The task-group view (`groupRange`, task groups spec §6.2) takes the place of 1–3: the selected group's tabs. The watcher area keeps priority: while it is the active
 * workspace the range is ignored.
 *
 * There is no "Home" view of workspace-less tabs: every tab belongs to a workspace (Profile Sync spec §4.3). A
 * tab nobody has adopted yet is in no workspace's list, so it is not in the bar for that moment even when it is
 * the active tab; the content area shows it all the same.
 */
export function getVisibleTabIds(params: GetVisibleTabIdsParams): string[] {
  const { tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId, groupRange } = params
  if (groupRange != null && activeWorkspaceId !== WATCH_WORKSPACE_ID) return groupRange.filter((id) => !!tabs[id])
  if (workspaces.length === 0) return tabOrder

  const scope =
    workspaces.find((w) => w.id === activeWorkspaceId)
    ?? (activeTabId !== null ? workspaces.find((w) => w.tabs.includes(activeTabId)) : undefined)
    ?? workspaces.find((w) => w.id !== WATCH_WORKSPACE_ID) // a fall-back never lands in the watcher area
    ?? workspaces[0]
  return scope.tabs.filter((id) => !!tabs[id])
}
