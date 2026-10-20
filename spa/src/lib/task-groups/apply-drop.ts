// spa/src/lib/task-groups/apply-drop.ts — makes what a dropped row means (task groups spec §6.1; TG-3e): a reorder inside its group (reorder.ts, apply-reorder.ts) or a move to another one — a solo session is filed
// or taken out through session-actions.ts, a plain tab gets its own `group` (§5).
import { useTabStore } from '../../stores/useTabStore'
import { groupTabs, type GroupOrderCtx } from './group-order'
import { resolveDrop, type DraggedRow, type DropOn } from './drop-resolve'
import { decideGroupDrop } from './reorder'
import { applyReorder } from './apply-reorder'
import { manualGroupKey } from './group-key'
import { fileSession, takeSessionOut, type SessionActionResult, type SessionRef } from './session-actions'

export interface DropDeps {
  ctx: GroupOrderCtx
  /** The solo session a tab row stands for now, or null (not a session). */
  sessionOf: (tabId: string) => SessionRef | null
  onResult: (r: SessionActionResult) => void
}

export function applyGroupDrop(active: DraggedRow, over: DropOn, { ctx, sessionOf, onResult }: DropDeps): void {
  const resolved = resolveDrop(active, over, groupTabs(active.groupKey, ctx))
  if (resolved.kind === 'ignore') return
  if (resolved.kind === 'reorder') {
    if (over.kind !== 'row') return
    applyReorder(decideGroupDrop({ groupKey: resolved.groupKey, activeId: active.tabId, overId: resolved.overId, side: resolved.side, ctx }))
    return
  }
  const { target } = resolved
  if (active.kind === 'plain') {
    useTabStore.getState().setTabGroup(active.tabId, target.kind === 'group' ? manualGroupKey(target.groupId) : undefined)
    return
  }
  const session = sessionOf(active.tabId)
  if (session === null) return
  // `resolveDrop` has already run `acceptsDrag`, so the move is a put or an out
  void (target.kind === 'group' ? fileSession(session, target.groupId) : takeSessionOut(session)).then(onResult)
}
