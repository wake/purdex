// spa/src/lib/task-groups/view-model.ts — what the sidebar's task-group view lists (name pool v2 spec §7, L9, L10; NA-6b). Pure: the sidebar feeds it from the stores.
//   1. the team groups, in the order given (a team with its lead and members);
//   2. each host's manual groups, hosts in `hostOrder`, groups in creation order (a host that cannot be written to says so first);
//   3. 「未分組」 last: every other session, left out when there is none.
// A session in a live team shows under its team only; its manual membership is ignored until the team ends (L10). A manual group holds the sessions of ITS OWN host (L9) whose lineage root it lists, so a
// relay keeps the membership; a session whose root is not known yet (or whose pane has no agent session id) is 「未分組」 for the display — it is never guessed into a group.
import type { TaskGroup } from './api'
import type { Seat, TeamView } from '../team/team-views'

/** One open tab that shows a tmux session: the sidebar's session row. */
export interface SessionTab {
  tabId: string
  hostId: string
  sessionCode: string
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
export interface ManualSection { kind: 'manual'; key: string; hostId: string; group: TaskGroup; rows: GroupRow[] }
export interface UngroupedSection { kind: 'ungrouped'; key: 'ungrouped'; rows: GroupRow[] }
/** The host's groups cannot be written to (the daemon left rows out, or the value is not a list): said on that host's block. */
export interface UnsafeSection { kind: 'unsafe'; hostId: string; count: number; reasons: string[] }
export type GroupSection = TeamSection | ManualSection | UngroupedSection | UnsafeSection

export interface HostGroups { groups: TaskGroup[]; unsafe: { count: number; reasons: string[] } | null }

export interface GroupViewInput {
  tabs: readonly SessionTab[]
  teams: readonly TeamView[]
  /** Per host, what the store holds; a host absent here has no groups to show (no `task_groups.v1`, or not read yet). */
  manual: Record<string, HostGroups>
  /** The lineage root of an agent session id on a host, or null when it is not known (yet). */
  rootOf: (hostId: string, agentSessionId: string) => string | null
  hostOrder: readonly string[]
}

const seatRow = (view: TeamView, seat: Seat): GroupRow => ({ key: `seat:${view.key}:${seat.session.session_id}`, tabId: seat.groupTabId ?? seat.tabId, seatId: seat.session.session_id, seat })
const tabRow = (t: SessionTab): GroupRow => ({ key: `tab:${t.tabId}`, tabId: t.tabId })

export function buildGroupView(input: GroupViewInput): GroupSection[] {
  const out: GroupSection[] = []
  const claimed = new Set<string>()

  for (const view of input.teams) {
    const seats = [view.lead, ...view.members]
    for (const s of seats) {
      if (s.tabId) claimed.add(s.tabId)
      if (s.groupTabId) claimed.add(s.groupTabId)
    }
    out.push({ kind: 'team', key: `team:${view.key}`, view, lead: seatRow(view, view.lead), members: view.members.map((m) => seatRow(view, m)) })
  }

  const hosts = [...input.hostOrder, ...Object.keys(input.manual).filter((h) => !input.hostOrder.includes(h))]
  for (const hostId of hosts) {
    const entry = input.manual[hostId]
    if (!entry) continue
    if (entry.unsafe) out.push({ kind: 'unsafe', hostId, count: entry.unsafe.count, reasons: entry.unsafe.reasons })
    const groups = [...entry.groups].sort((a, b) => a.created_at - b.created_at || a.id.localeCompare(b.id))
    for (const group of groups) {
      const roots = new Set(group.roots)
      const rows: GroupRow[] = []
      for (const t of input.tabs) {
        if (t.hostId !== hostId || claimed.has(t.tabId) || t.agentSessionId === null) continue
        const root = input.rootOf(hostId, t.agentSessionId)
        if (root !== null && roots.has(root)) {
          rows.push(tabRow(t))
          claimed.add(t.tabId)
        }
      }
      out.push({ kind: 'manual', key: `manual:${hostId}:${group.id}`, hostId, group, rows })
    }
  }

  const rest = input.tabs.filter((t) => !claimed.has(t.tabId)).map(tabRow)
  if (rest.length > 0) out.push({ kind: 'ungrouped', key: 'ungrouped', rows: rest })
  return out
}
