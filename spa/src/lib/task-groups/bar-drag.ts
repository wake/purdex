// spa/src/lib/task-groups/bar-drag.ts — dragging a tab in the top bar of the task-group view (task groups spec §6.2, TG-4c). The bar lists `groupTabs(selectedGroup)`; a drop there is the same pure decision as in the sidebar
// (`decideGroupDrop`, reorder.ts: lead never moves and nothing goes before it, a seat moves with all its tabs and writes this device's member order, pinned and plain tabs by `groupRank`, no zone crossed). Pure.
import { teamKeyOfGroupKey } from './group-key'
import { groupTabs, teamSections, type GroupOrderCtx } from './group-order'
import { decideGroupDrop, type ReorderDecision } from './reorder'

/** The tabs of the group's bar that cannot be picked up: a team's lead tab(s). */
export function barLockedTabs(groupKey: string, ctx: GroupOrderCtx): Set<string> {
  const teamKey = teamKeyOfGroupKey(groupKey)
  const view = teamKey === null ? undefined : ctx.index.byKey.get(teamKey)
  if (view === undefined) return new Set()
  const tabs = groupTabs(groupKey, ctx).map((id) => ctx.tabs[id])
  return new Set(teamSections(view, tabs, ctx).lead.map((t) => t.id))
}

/** What dropping `activeId` on `overId` in the bar writes; the side is the direction of travel along the bar (the tab ends up where the one it was dropped on was). */
export function decideBarDrop(groupKey: string, activeId: string, overId: string, ctx: GroupOrderCtx): ReorderDecision {
  const order = groupTabs(groupKey, ctx)
  const from = order.indexOf(activeId)
  const to = order.indexOf(overId)
  if (from < 0 || to < 0 || from === to) return { kind: 'ignore' }
  // dropped on the lead: right after it (nothing goes before it) — the first seat
  const side = barLockedTabs(groupKey, ctx).has(overId) ? 'after' : from < to ? 'after' : 'before'
  return decideGroupDrop({ groupKey, activeId, overId, side, ctx })
}
