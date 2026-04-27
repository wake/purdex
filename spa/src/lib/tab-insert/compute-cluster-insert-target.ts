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
 * Anchor selection is workspace-scoped: when `workspaceId` is supplied
 * the anchor is `ws.activeTabId`, NOT the global
 * `useTabStore.activeTabId`. This protects async terminal-link opens
 * — if the user switches workspaces between click and resolve, the
 * global active tab can belong to another workspace, and anchoring on
 * it would return an `afterTabId` that is absent from the target
 * `workspace.tabs`, breaking the two-store agreement (`addTab` would
 * insert after the foreign tab in `tabOrder`, while `insertTab` would
 * append because the anchor is not present in `ws.tabs`).
 *
 * Returns `undefined` when no usable anchor exists (no workspace
 * active tab, anchor not in visible order, or no match to the right);
 * the caller's `addTab` / `insertTab` calls then fall through to
 * append-at-end behaviour.
 */
export function computeClusterInsertTarget(
  workspaceId: string | null,
  isSameKind: (content: PaneContent) => boolean,
): string | undefined {
  const tabState = useTabStore.getState()
  const wsState = useWorkspaceStore.getState()
  const ws = workspaceId
    ? wsState.workspaces.find((w) => w.id === workspaceId)
    : null
  const anchorTabId = ws ? ws.activeTabId : tabState.activeTabId
  if (!anchorTabId) return undefined

  const visibleOrder = ws
    ? ws.tabs.filter((tid) => !!tabState.tabs[tid])
    : tabState.tabOrder
  // Defensive: anchor must live in visibleOrder. If it does not (e.g.
  // ws.activeTabId pointing to a tab that has been removed from
  // ws.tabs but not yet re-set), bail rather than risk a divergent
  // afterTabId.
  if (!visibleOrder.includes(anchorTabId)) return undefined

  return findInsertTarget(visibleOrder, anchorTabId, tabState.tabs, isSameKind)
}
