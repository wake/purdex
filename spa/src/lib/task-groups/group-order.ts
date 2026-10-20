// spa/src/lib/task-groups/group-order.ts — the tabs of one group, in the order its bar shows them (task groups spec §4.2, §5). Every tie goes through `compareGroupTabs` (rank, then createdAt, then tab id), so two windows
// that hold the same tabs list them the same way. Pure.
//   - a manual group and 「未分組」: pinned tabs first, then the others.
//   - a team: its lead's tabs first (pinned or not), then the other pinned tabs, then each member's tabs in the team's member order (the tab the seat chose first), then the plain tabs `tab.group` put in the team.
//     The member collapse is a drawing matter and does not apply here: every tab is listed.
import type { Tab } from '../../types/tab'
import type { TeamView } from '../team/team-views'
import { compareGroupTabs } from './rank'
import { groupOfTab, manualOwners, type PlacementCtx } from './placement'
import { teamKeyOfGroupKey } from './group-key'

export type OrderedTab = Pick<Tab, 'id' | 'layout' | 'group' | 'groupRank' | 'createdAt' | 'pinned'>

export interface GroupOrderCtx extends PlacementCtx {
  tabs: Readonly<Record<string, OrderedTab>>
}

/** Every group's tabs at once: group key → tab ids in bar order. Tabs outside every group (the watcher area) are not listed. */
export function allGroupTabs(ctx: GroupOrderCtx): Map<string, string[]> {
  const owners = manualOwners(ctx.manual)
  const byGroup = new Map<string, OrderedTab[]>()
  for (const tab of Object.values(ctx.tabs)) {
    const key = groupOfTab(tab, ctx, owners)
    if (key === null) continue
    const list = byGroup.get(key)
    if (list) list.push(tab)
    else byGroup.set(key, [tab])
  }
  const out = new Map<string, string[]>()
  for (const [key, list] of byGroup) out.set(key, orderGroup(key, list, ctx).map((t) => t.id))
  return out
}

/** The tab ids of one group, in bar order; empty when it holds none. */
export function groupTabs(groupKey: string, ctx: GroupOrderCtx): string[] {
  return allGroupTabs(ctx).get(groupKey) ?? []
}

function orderGroup(key: string, tabs: OrderedTab[], ctx: GroupOrderCtx): OrderedTab[] {
  const teamKey = teamKeyOfGroupKey(key)
  const view = teamKey === null ? undefined : ctx.index.byKey.get(teamKey)
  if (view === undefined) {
    const pinned = tabs.filter((t) => t.pinned).sort(compareGroupTabs)
    return [...pinned, ...tabs.filter((t) => !t.pinned).sort(compareGroupTabs)]
  }
  const s = teamSections(view, tabs, ctx)
  return [...s.lead, ...s.pinned, ...s.members.flat(), ...s.plain]
}

/** A team's tabs split the way the bar lays them out: the lead's, the other pinned ones, each member's (in member order), the plain rest. Each part is in bar order. */
export interface TeamSections {
  lead: OrderedTab[]
  pinned: OrderedTab[]
  members: OrderedTab[][]
  plain: OrderedTab[]
}

export function teamSections(view: Pick<TeamView, 'lead' | 'members'>, tabs: readonly OrderedTab[], ctx: Pick<GroupOrderCtx, 'index'>): TeamSections {
  const seatOf = (t: OrderedTab) => ctx.index.seatByTabId.get(t.id)?.seat
  const taken = new Set<string>()
  const take = (list: OrderedTab[]): OrderedTab[] => {
    const out = list.filter((t) => !taken.has(t.id)).sort(compareGroupTabs)
    for (const t of out) taken.add(t.id)
    return out
  }
  // a seat's tabs: the tab the seat chose first, then the rest by the common order
  const ofSeat = (sessionId: string, chosen: string | null): OrderedTab[] => {
    const mine = take(tabs.filter((t) => seatOf(t)?.session.session_id === sessionId))
    const first = mine.filter((t) => t.id === chosen)
    return [...first, ...mine.filter((t) => t.id !== chosen)]
  }
  // the chosen tab is the one the sidebar draws the seat as (`groupTabId`, else `tabId`): the same rule as the seat row
  const chosenOf = (seat: Pick<TeamView['lead'], 'tabId' | 'groupTabId'>) => seat.groupTabId ?? seat.tabId
  const lead = ofSeat(view.lead.session.session_id, chosenOf(view.lead))
  const pinned = take(tabs.filter((t) => t.pinned))
  const members = view.members.map((m) => ofSeat(m.session.session_id, chosenOf(m)))
  return { lead, pinned, members, plain: take([...tabs]) }
}
