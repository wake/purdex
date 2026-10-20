// spa/src/lib/task-groups/placement.ts — which group a tab is in (task groups spec §5). The first rule that hits wins:
//   1. the tab shows a live team seat (the inclusive seat map, so a pinned tab and a team whose lead is pinned count) → that team;
//   2. the tab's agent session (the first tmux-session pane, in tree order, that recorded an agent session id — live or ended) has a known lineage root, and (host, root) is a member of a manual group → that group (first in `order`);
//   3. `tab.group` names a group that exists → that group;
//   4. 「未分組」.
// A watcher-area tab is outside every group. Pure: callers hand in the team index, the manual groups and the root lookup.
import type { Tab } from '../../types/tab'
import type { ManualGroups } from '../../stores/useManualGroupsStore'
import { collectLeaves } from '../pane-tree'
import { isWatchTabId } from '../watch/ids'
import type { TeamIndex } from '../team/team-index'
import { manualGroupKey, parseGroupKey, teamGroupKey, UNGROUPED } from './group-key'

export type PlacedTab = Pick<Tab, 'id' | 'layout' | 'group'>

export interface PlacementCtx {
  index: Pick<TeamIndex, 'seatByTabId' | 'byKey'>
  manual: ManualGroups
  /** The lineage root of an agent session id on a host, or null when it is not known. */
  rootOf: (hostId: string, agentSessionId: string) => string | null
}

const memberKey = (host: string, root: string) => JSON.stringify([host, root])

/** (host, root) → the id of the manual group that lists it; a member two groups list belongs to the first in `order`. */
export function manualOwners(manual: ManualGroups): Map<string, string> {
  const owners = new Map<string, string>()
  for (const id of manual.order) {
    for (const m of manual.groups[id]?.members ?? []) {
      const k = memberKey(m.host, m.root)
      if (!owners.has(k)) owners.set(k, id)
    }
  }
  return owners
}

/** The host and agent session id of the tab's first pane (tree order) that recorded one. */
export function agentSessionOfTab(tab: Pick<Tab, 'layout'>): { hostId: string; sessionId: string } | null {
  for (const pane of collectLeaves(tab.layout)) {
    const c = pane.content
    if (c.kind !== 'tmux-session') continue
    const sessionId = c.rebuild?.agent?.sessionId
    if (sessionId) return { hostId: c.hostId, sessionId }
  }
  return null
}

/** `owners` is `manualOwners(ctx.manual)`, passed in so a caller placing many tabs builds it once. */
export function groupOfTab(tab: PlacedTab, ctx: PlacementCtx, owners: Map<string, string> = manualOwners(ctx.manual)): string | null {
  if (isWatchTabId(tab.id)) return null
  const seat = ctx.index.seatByTabId.get(tab.id)
  if (seat) return teamGroupKey(seat.key)
  const agent = agentSessionOfTab(tab)
  if (agent) {
    const root = ctx.rootOf(agent.hostId, agent.sessionId)
    const owner = root === null ? undefined : owners.get(memberKey(agent.hostId, root))
    if (owner !== undefined) return manualGroupKey(owner)
  }
  const named = tab.group === undefined ? null : parseGroupKey(tab.group)
  if (named?.kind === 'team' && ctx.index.byKey.has(named.teamKey)) return tab.group!
  if (named?.kind === 'manual' && ctx.manual.groups[named.id] !== undefined) return tab.group!
  return UNGROUPED
}
