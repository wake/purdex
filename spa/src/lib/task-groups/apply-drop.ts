// spa/src/lib/task-groups/apply-drop.ts — makes what a dropped row means (task groups spec §6.1; TG-3e): a reorder inside its group (reorder.ts, apply-reorder.ts) or a move to another one — a solo session is filed
// or taken out through session-actions.ts, a plain tab gets its own `group` (§5).
import { useTabStore } from '../../stores/useTabStore'
import { allGroupTabs, groupTabs, type GroupOrderCtx } from './group-order'
import { resolveDrop, type DragRowKind, type DraggedRow, type DropOn } from './drop-resolve'
import { teamKeyOfGroupKey } from './group-key'
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

/** Where a tab is shown NOW and what kind of row it is, or null when no group lists it. */
function rowNow(tabId: string, { ctx, sessionOf }: DropDeps): { groupKey: string; kind: DragRowKind } | null {
  for (const [groupKey, ids] of allGroupTabs(ctx)) {
    if (!ids.includes(tabId)) continue
    const kind: DragRowKind = ctx.index.seatByTabId.has(tabId) ? 'seat' : sessionOf(tabId) !== null && teamKeyOfGroupKey(groupKey) === null ? 'session' : 'plain'
    return { groupKey, kind }
  }
  return null
}

/** The drag began on a snapshot (its group, its kind); the drop acts on what is true when it lands: a row whose group or kind changed meanwhile (a roster or root update) is not written from the old picture. */
export function applyGroupDrop(active: DraggedRow, over: DropOn, deps: DropDeps): void {
  const { ctx, sessionOf, onResult } = deps
  const now = rowNow(active.tabId, deps)
  if (now === null || now.groupKey !== active.groupKey || now.kind !== active.kind) return
  if (over.kind === 'row') {
    const o = rowNow(over.tabId, deps)
    if (o === null) return
    over = { kind: 'row', tabId: over.tabId, groupKey: o.groupKey, seatId: over.seatId }
  }
  const resolved = resolveDrop(active, over, groupTabs(active.groupKey, ctx))
  if (resolved.kind === 'ignore') return
  if (resolved.kind === 'reorder') {
    if (over.kind !== 'row') return
    applyReorder(decideGroupDrop({ groupKey: resolved.groupKey, activeId: active.tabId, overId: resolved.overId, side: resolved.side, ctx, activeSeat: resolved.activeSeat, overSeat: resolved.overSeat }))
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
