// spa/src/lib/task-groups/place-last.ts — after a tab is moved into a group from the personal panel it is the group's LAST tab (task groups spec §5 user writes, TG-5c; design R18). It is a drag to the end of the group's bar:
// the same pure decision as TG-3e (`decideGroupDrop`), so the unranked tabs before the drop point are ranked too, and an old `groupRank` of the tab cannot put it somewhere in the middle of the new group.
import { currentGroupOrderCtx } from './current'
import { groupTabs, type GroupOrderCtx } from './group-order'
import { groupOfTab } from './placement'
import { decideGroupDrop } from './reorder'
import { applyReorder } from './apply-reorder'

/** `ctx`: the placement inputs AFTER the move was written (default: read from the stores now). */
export function placeLastInGroup(tabId: string, ctx: GroupOrderCtx = currentGroupOrderCtx()): void {
  const tab = ctx.tabs[tabId]
  if (tab === undefined) return
  const groupKey = groupOfTab(tab, ctx)
  if (groupKey === null) return
  // the tab's own zone (pinned tabs sort before the others): the last tab there other than itself
  const last = groupTabs(groupKey, ctx).filter((id) => id !== tabId && Boolean(ctx.tabs[id]?.pinned) === Boolean(tab.pinned)).at(-1)
  if (last === undefined) return
  applyReorder(decideGroupDrop({ groupKey, activeId: tabId, overId: last, side: 'after', ctx }))
}
