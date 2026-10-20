// spa/src/lib/task-groups/view-model.ts — what the sidebar's task-group view lists (task groups spec §6.1, TG-2b, TG-3a-2b; name pool v2 spec §7, L10). Pure: the sidebar feeds it from the stores.
//   1. the team groups, in the order given (a team with its lead and members, then the other tabs the team holds);
//   2. the manual groups of `useManualGroupsStore`, in its `order`;
//   3. 「未分組」 last: every other tab, left out when there is none.
// Which group a tab is in is `groupOfTab` (placement.ts) and the order inside a group is `groupTabs` (group-order.ts); this file only turns them into rows. A group lists ALL its tabs — a tab that is no session, a tab whose
// agent has ended, a second tab of a seat. A session in a live team shows under its team only (L10); a session whose root is not known yet is 「未分組」 for the display, never guessed into a group.
import type { ManualGroup } from '../../stores/useManualGroupsStore'
import type { Seat, TeamView } from '../team/team-views'
import { allGroupTabs, type GroupOrderCtx } from './group-order'
import { manualGroupKey, teamGroupKey, UNGROUPED } from './group-key'

/** One open tab that shows a live tmux session: what a row's right-click menu and drag act on. */
export interface SessionTab {
  tabId: string
  hostId: string
  sessionCode: string
  /** The tmux session name `sessionCode` stands for (the sessions store, else the pane's cached name). */
  sessionName?: string | null
  /** The agent session id the pane recorded (`rebuild.agent.sessionId`); null when it recorded none. */
  agentSessionId: string | null
  workspaceId: string | null
}

export interface GroupRow {
  key: string
  /** The tab a click switches to; null for a team seat no open tab shows (listed, not openable). */
  tabId: string | null
  /** Set for a team seat: its roster session id. */
  seatId?: string
  seat?: Seat
}

/** `extra`: the team's other tabs — a second tab of a seat, a pinned member's tab, a plain tab `tab.group` filed under the team. */
export interface TeamSection { kind: 'team'; key: string; view: TeamView; lead: GroupRow; members: GroupRow[]; extra: GroupRow[] }
/** `rows` empty = nothing live in the group: the view collapses it. */
export interface ManualSection { kind: 'manual'; key: string; groupId: string; group: ManualGroup; rows: GroupRow[] }
export interface UngroupedSection { kind: 'ungrouped'; key: 'ungrouped'; rows: GroupRow[] }
export type GroupSection = TeamSection | ManualSection | UngroupedSection

/** The placement inputs (`tabs`, the team index, `manual`, `rootOf`) plus the team views in the order to list them. */
export interface GroupViewInput extends GroupOrderCtx {
  teams: readonly TeamView[]
}

const seatRow = (view: TeamView, seat: Seat): GroupRow => ({ key: `seat:${view.key}:${seat.session.session_id}`, tabId: seat.groupTabId ?? seat.tabId, seatId: seat.session.session_id, seat })
const tabRow = (tabId: string): GroupRow => ({ key: `tab:${tabId}`, tabId })

export function buildGroupView(input: GroupViewInput): GroupSection[] {
  const out: GroupSection[] = []
  const placed = allGroupTabs(input)

  for (const view of input.teams) {
    const lead = seatRow(view, view.lead)
    const members = view.members.map((m) => seatRow(view, m))
    const shown = new Set([lead, ...members].flatMap((r) => (r.tabId === null ? [] : [r.tabId])))
    const extra = (placed.get(teamGroupKey(view.key)) ?? []).filter((id) => !shown.has(id)).map(tabRow)
    out.push({ kind: 'team', key: `team:${view.key}`, view, lead, members, extra })
  }

  for (const groupId of input.manual.order) {
    const group = input.manual.groups[groupId]
    if (!group) continue
    out.push({ kind: 'manual', key: `manual:${groupId}`, groupId, group, rows: (placed.get(manualGroupKey(groupId)) ?? []).map(tabRow) })
  }

  const rest = (placed.get(UNGROUPED) ?? []).map(tabRow)
  if (rest.length > 0) out.push({ kind: 'ungrouped', key: 'ungrouped', rows: rest })
  return out
}
