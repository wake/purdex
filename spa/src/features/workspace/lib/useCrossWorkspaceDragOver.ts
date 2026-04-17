import { useCallback, useRef } from 'react'
import type { DragOverEvent } from '@dnd-kit/core'
import { useWorkspaceStore } from '../store'
import type { DragData } from './computeDragEndAction'

/**
 * Optimistic cross-workspace make-way effect: when a tab drags over a tab in a
 * different workspace, move it into the target workspace immediately so that
 * workspace's SortableContext measures the new child and animates space for it.
 *
 * Edge cases:
 *  - Pinned tabs are forbidden from leaving their workspace (see #404), so we
 *    skip them here and let computeDragEndAction return noop on drop.
 *  - A single drag session may fire onDragOver hundreds of times with the same
 *    target; we debounce by remembering the last (active, overWs, overTab)
 *    triple we acted on.
 *  - If the drag is cancelled outside any drop target, the optimistic move is
 *    not reverted — matching dnd-kit's official multi-list example and VS
 *    Code's tab-drag UX.
 *  - Standalone ↔ workspace transitions (toWs=null or fromWs=null) are left to
 *    computeDragEndAction at drop time; this hook only handles ws↔ws.
 */
export function useCrossWorkspaceDragOver() {
  const lastKeyRef = useRef<string | null>(null)

  return useCallback((event: DragOverEvent) => {
    const { active, over } = event
    if (!over) {
      lastKeyRef.current = null
      return
    }
    const activeData = active.data.current as DragData | undefined
    const overData = over.data.current as DragData | undefined
    if (!activeData || activeData.type !== 'tab') return
    if (!overData || overData.type !== 'tab') return
    if (activeData.isPinned) return

    const fromWs = activeData.sourceWsId
    const toWs = overData.sourceWsId
    if (fromWs === toWs) return
    if (toWs === null || fromWs === null) return

    const key = `${activeData.tabId}:${toWs}:${overData.tabId}`
    if (lastKeyRef.current === key) return
    lastKeyRef.current = key

    useWorkspaceStore.getState().removeTabFromWorkspace(fromWs, activeData.tabId)
    // Re-read state after the mutation so subsequent lookups see the post-
    // removal workspaces array (Zustand set() produces a new state object;
    // reusing the pre-mutation snapshot returns stale tabs — see PR #392/#419
    // stale-closure fixes).
    const postRemoval = useWorkspaceStore.getState()
    const targetWs = postRemoval.workspaces.find((w) => w.id === toWs)
    const overIdx = targetWs?.tabs.indexOf(overData.tabId) ?? -1
    const beforeTabId = overIdx > 0 ? (targetWs!.tabs[overIdx - 1] ?? null) : null
    postRemoval.insertTab(activeData.tabId, toWs, beforeTabId)
    // Update the active drag data's sourceWsId so subsequent onDragOver events
    // see the tab's new home. dnd-kit passes data.current by reference, so
    // mutating it propagates.
    ;(active.data.current as DragData).sourceWsId = toWs as never
  }, [])
}
