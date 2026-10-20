// spa/src/lib/task-groups/reorder.ts — what a drag of a tab onto another tab INSIDE a group writes (task groups spec §4.2, §6.1, §6.2; TG-3e). Pure: the view/bar gives the dragged tab, the tab it was dropped on and the side, and gets
// back the decision; `apply-reorder.ts` makes the writes.
//   - Manual group / 「未分組」: pinned tabs first, then the others, each zone by `groupRank`. A tab moves inside its own zone.
//   - Team: the lead (never moves, nothing drops before it), then the other pinned tabs, then each seat with ALL its tabs, then the plain tabs. A seat moves as a unit and writes this device's member order; pinned and plain tabs
//     move by rank inside their own zone; nothing crosses a zone.
//   - A rank is written only when the user moves a tab. A drop among UNRANKED tabs also ranks the unranked tabs before the drop point, in their shown order (the first reorder there writes those few tabs once). When no string fits
//     between the neighbours (the 32-character cap, equal ranks from two workspaces), the whole zone is re-ranked evenly.
import { rankBetween, rebalance } from './rank'
import { teamKeyOfGroupKey } from './group-key'
import { groupTabs, teamSections, type GroupOrderCtx, type OrderedTab } from './group-order'

export type DropSide = 'before' | 'after'
export interface RankWrite { tabId: string; rank: string }

export type ReorderDecision =
  | { kind: 'ignore' }
  /** `writes`: the tabs whose `groupRank` is set. */
  | { kind: 'rank'; writes: RankWrite[] }
  /** `order`: the team's member session ids in their new order (this device's `memberOrder`). */
  | { kind: 'members'; teamKey: string; order: string[] }

const IGNORE: ReorderDecision = { kind: 'ignore' }

/**
 * The rank writes that put `activeId` before/after `overId` in `zone` (the zone's tabs in their shown order), or null when the order does not change. Ranked tabs come first in a zone, so the drop point is inside the ranked prefix
 * or among the unranked tabs; the second case ranks the unranked tabs before it as well.
 */
export function rankZoneMove(zone: ReadonlyArray<Pick<OrderedTab, 'id' | 'groupRank'>>, activeId: string, overId: string, side: DropSide): RankWrite[] | null {
  const active = zone.find((t) => t.id === activeId)
  if (active === undefined || activeId === overId) return null
  const rest = zone.filter((t) => t.id !== activeId)
  const overAt = rest.findIndex((t) => t.id === overId)
  if (overAt < 0) return null
  const at = side === 'before' ? overAt : overAt + 1
  const before = rest.slice(0, at)
  const after = rest.slice(at)
  const order = [...before, active, ...after]
  if (order.every((t, i) => t.id === zone[i].id)) return null

  const ranked = before.filter((t) => t.groupRank !== undefined)
  const unrankedBefore = before.filter((t) => t.groupRank === undefined)
  const lo = ranked.at(-1)?.groupRank
  // Without unranked tabs before the drop point only the dragged tab is ranked, between its neighbours (the next one's rank only counts when it has one). Otherwise the unranked tabs before the point and the dragged tab
  // are ranked in a chain after the last ranked tab; they all sort before the unranked tabs that stay behind.
  const writes: RankWrite[] = []
  if (unrankedBefore.length === 0) {
    const r = rankBetween(lo, after[0]?.groupRank)
    if (r !== null) writes.push({ tabId: activeId, rank: r })
    else return rerankZone(order)
  } else {
    let prev = lo
    for (const t of [...unrankedBefore, active]) {
      const r = rankBetween(prev, undefined)
      if (r === null) return rerankZone(order)
      writes.push({ tabId: t.id, rank: r })
      prev = r
    }
  }
  return writes.filter((w) => zone.find((t) => t.id === w.tabId)?.groupRank !== w.rank)
}

/** Every tab of the zone ranked evenly in `order`. */
function rerankZone(order: ReadonlyArray<Pick<OrderedTab, 'id'>>): RankWrite[] {
  const ranks = rebalance(order)
  return order.map((t, i) => ({ tabId: t.id, rank: ranks[i] }))
}

/** What dropping `activeId` on `overId` (the `side` of it) writes inside the group `groupKey`; `ignore` when the drag means nothing there. */
export function decideGroupDrop(p: { groupKey: string; activeId: string; overId: string; side: DropSide; ctx: GroupOrderCtx; activeSeat?: string; overSeat?: string }): ReorderDecision {
  const { groupKey, activeId, overId, side, ctx, activeSeat, overSeat } = p
  const ids = groupTabs(groupKey, ctx)
  if ((activeId === overId && (activeSeat === undefined || activeSeat === overSeat)) || !ids.includes(activeId) || !ids.includes(overId)) return IGNORE
  const tabs = ids.map((id) => ctx.tabs[id])
  const rank = (zone: OrderedTab[]): ReorderDecision => {
    const writes = rankZoneMove(zone, activeId, overId, side)
    return writes === null || writes.length === 0 ? IGNORE : { kind: 'rank', writes }
  }
  const inZone = (zone: readonly OrderedTab[], id: string) => zone.some((t) => t.id === id)

  const teamKey = teamKeyOfGroupKey(groupKey)
  const view = teamKey === null ? undefined : ctx.index.byKey.get(teamKey)
  if (view === undefined) {
    // a manual group or 「未分組」: the pinned zone and the rest
    const pinned = tabs.filter((t) => t.pinned)
    const others = tabs.filter((t) => !t.pinned)
    if (inZone(pinned, activeId) && inZone(pinned, overId)) return rank(pinned)
    if (inZone(others, activeId) && inZone(others, overId)) return rank(others)
    return IGNORE
  }

  const sections = teamSections(view, tabs, ctx)
  const memberIds = view.members.map((m) => m.session.session_id)
  // rows named by their seat (a split tab carrying several seats): the seats are moved by identity
  if (activeSeat !== undefined && memberIds.includes(activeSeat)) {
    // a row of another tab of a seat (no seat of its own) names its seat through the tab
    let target = overSeat
    if (target === undefined) {
      if (inZone(sections.lead, overId)) target = view.lead.session.session_id
      else { const to = sections.members.findIndex((m) => inZone(m, overId)); if (to >= 0) target = memberIds[to] }
    }
    if (target === view.lead.session.session_id) return moveSeat(view.key, memberIds, activeSeat, { lead: true }, side)
    if (target === undefined || !memberIds.includes(target)) return IGNORE
    // the side is the direction of travel among the seats (the rows of one split tab share a tab, so the tabs cannot tell)
    return moveSeat(view.key, memberIds, activeSeat, { seat: target }, memberIds.indexOf(activeSeat) < memberIds.indexOf(target) ? 'after' : 'before')
  }
  // the lead never moves and nothing goes before it
  if (inZone(sections.lead, activeId)) return IGNORE
  if (inZone(sections.pinned, activeId)) return inZone(sections.pinned, overId) ? rank(sections.pinned) : IGNORE
  if (inZone(sections.plain, activeId)) return inZone(sections.plain, overId) ? rank(sections.plain) : IGNORE

  // a seat's tab: the seat moves, with all its tabs, relative to the seat of the tab it was dropped on
  const from = sections.members.findIndex((m) => inZone(m, activeId))
  if (from < 0) return IGNORE
  if (inZone(sections.lead, overId)) return moveSeat(view.key, memberIds, memberIds[from], { lead: true }, side)
  const to = sections.members.findIndex((m) => inZone(m, overId))
  if (to < 0) return IGNORE
  return moveSeat(view.key, memberIds, memberIds[from], { seat: memberIds[to] }, side)
}

/** The member order with seat `mine` put before/after `anchor` (the lead = right after it, the first seat), or ignore. */
function moveSeat(teamKey: string, memberIds: string[], mine: string, anchor: { lead: true } | { seat: string }, side: DropSide): ReorderDecision {
  const rest = memberIds.filter((id) => id !== mine)
  let at: number
  if ('lead' in anchor) {
    if (side === 'before') return IGNORE // nothing before the lead
    at = 0
  } else {
    const target = rest.indexOf(anchor.seat)
    if (target < 0) return IGNORE // dropped on its own seat
    at = side === 'before' ? target : target + 1
  }
  const order = [...rest.slice(0, at), mine, ...rest.slice(at)]
  if (order.every((id, i) => id === memberIds[i])) return IGNORE
  return { kind: 'members', teamKey, order }
}
