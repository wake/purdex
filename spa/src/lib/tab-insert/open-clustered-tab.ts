import type { PaneContent } from '../../types/tab'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { computeClusterInsertTarget } from './compute-cluster-insert-target'

/**
 * Open or activate a singleton tab AND insert it into the given
 * workspace such that both `tabOrder` and `workspace.tabs` place the
 * new tab next to its same-kind neighbour. The TabBar renders from
 * `workspace.tabs`, so the two must agree.
 *
 * Use this for any "open a new tab clustered with same-kind" UX —
 * file openers (editor / image-preview / pdf-preview) and similar.
 * Settings / hosts / history flows stay on
 * `useTabStore.openSingletonTab` directly (append at end; no
 * clustering).
 */
export function openClusteredTab(
  content: PaneContent,
  isSameKind: (c: PaneContent) => boolean,
  workspaceId: string,
): string {
  const afterTabId = computeClusterInsertTarget(workspaceId, isSameKind)
  const tabId = useTabStore.getState().openSingletonTab(content, { afterTabId })
  useWorkspaceStore.getState().insertTab(tabId, workspaceId, afterTabId)
  return tabId
}
