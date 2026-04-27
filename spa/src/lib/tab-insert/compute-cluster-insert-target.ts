import type { PaneContent } from '../../types/tab'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { findInsertTarget } from './find-insert-target'

/**
 * Compute the `afterTabId` for a new clustering tab insertion within
 * the given workspace. The caller is expected to pass the same value
 * to BOTH `useTabStore.openSingletonTab({ afterTabId })` and
 * `useWorkspaceStore.insertTab(tabId, wsId, afterTabId)` — the TabBar
 * renders from `workspace.tabs`, so the two stores must agree on
 * placement or the clustering UX regresses to "appended at end".
 *
 * Returns `undefined` when there is no active tab, no workspace
 * context, or no matching tab to the right (in which case the caller's
 * subsequent `addTab` / `insertTab` calls naturally fall through to
 * append-at-end behavior).
 */
export function computeClusterInsertTarget(
  workspaceId: string | null,
  isSameKind: (content: PaneContent) => boolean,
): string | undefined {
  const tabState = useTabStore.getState()
  const activeTabId = tabState.activeTabId
  if (!activeTabId) return undefined

  const wsState = useWorkspaceStore.getState()
  const ws = workspaceId
    ? wsState.workspaces.find((w) => w.id === workspaceId)
    : null
  const visibleOrder = ws
    ? ws.tabs.filter((tid) => !!tabState.tabs[tid])
    : tabState.tabOrder
  return findInsertTarget(visibleOrder, activeTabId, tabState.tabs, isSameKind)
}
