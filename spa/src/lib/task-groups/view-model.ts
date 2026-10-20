// spa/src/lib/task-groups/view-model.ts — what the sidebar's task-group view lists (task groups spec §6.1, TG-2b; name pool v2 spec §7, L10). Pure: the sidebar feeds it from the stores.
//   1. the team groups, in the order given (a team with its lead and members);
//   2. the manual groups of `useManualGroupsStore`, in its `order`;
//   3. 「未分組」 last: every other session, left out when there is none.
// A session in a live team shows under its team only; its manual membership is ignored until the team ends (L10). A manual group gathers sessions of ANY host: a member is a (host, lineage root), so a relay keeps
// the membership; a session whose root is not known yet (or whose pane has no agent session id) is 「未分組」 for the display — it is never guessed into a group. A session two groups name (two devices filed it
// differently) is shown in the first of them in `order`, once.
import type { ManualGroup, ManualGroups } from '../../stores/useManualGroupsStore'
import { seatLookup, sessionKey, type Seat, type TeamView } from '../team/team-views'

/** One open tab that shows a tmux session: the sidebar's session row. */
export interface SessionTab {
  tabId: string
  hostId: string
  sessionCode: string
  /** The tmux session name `sessionCode` stands for (the sessions store, else the pane's cached name); decides whether a team seat is this session. */
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

export interface TeamSection { kind: 'team'; key: string; view: TeamView; lead: GroupRow; members: GroupRow[] }
/** `rows` empty = nothing live in the group: the view collapses it. */
export interface ManualSection { kind: 'manual'; key: string; groupId: string; group: ManualGroup; rows: GroupRow[] }
export interface UngroupedSection { kind: 'ungrouped'; key: 'ungrouped'; rows: GroupRow[] }
export type GroupSection = TeamSection | ManualSection | UngroupedSection

export interface GroupViewInput {
  tabs: readonly SessionTab[]
  teams: readonly TeamView[]
  /** What `useManualGroupsStore` holds: the groups and their order. */
  manual: ManualGroups
  /** The lineage root of an agent session id on a host, or null when it is not known (yet). */
  rootOf: (hostId: string, agentSessionId: string) => string | null
}

const seatRow = (view: TeamView, seat: Seat): GroupRow => ({ key: `seat:${view.key}:${seat.session.session_id}`, tabId: seat.groupTabId ?? seat.tabId, seatId: seat.session.session_id, seat })
const tabRow = (t: SessionTab): GroupRow => ({ key: `tab:${t.tabId}`, tabId: t.tabId })
const memberKey = (host: string, root: string) => JSON.stringify([host, root])

export function buildGroupView(input: GroupViewInput): GroupSection[] {
  const out: GroupSection[] = []
  const claimed = new Set<string>()
  // Every tab that shows a team seat's session is the team's, not just the tab the seat was drawn from.
  const seatSessions = seatLookup(input.teams)
  for (const t of input.tabs) if (t.sessionName && seatSessions.has(sessionKey(t.hostId, t.sessionName))) claimed.add(t.tabId)

  for (const view of input.teams) {
    const seats = [view.lead, ...view.members]
    for (const s of seats) {
      if (s.tabId) claimed.add(s.tabId)
      if (s.groupTabId) claimed.add(s.groupTabId)
    }
    out.push({ kind: 'team', key: `team:${view.key}`, view, lead: seatRow(view, view.lead), members: view.members.map((m) => seatRow(view, m)) })
  }

  for (const groupId of input.manual.order) {
    const group = input.manual.groups[groupId]
    if (!group) continue
    const members = new Set(group.members.map((m) => memberKey(m.host, m.root)))
    const rows: GroupRow[] = []
    for (const t of input.tabs) {
      if (claimed.has(t.tabId) || t.agentSessionId === null) continue
      const root = input.rootOf(t.hostId, t.agentSessionId)
      if (root !== null && members.has(memberKey(t.hostId, root))) {
        rows.push(tabRow(t))
        claimed.add(t.tabId)
      }
    }
    out.push({ kind: 'manual', key: `manual:${groupId}`, groupId, group, rows })
  }

  const rest = input.tabs.filter((t) => !claimed.has(t.tabId)).map(tabRow)
  if (rest.length > 0) out.push({ kind: 'ungrouped', key: 'ungrouped', rows: rest })
  return out
}
