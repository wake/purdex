// spa/src/lib/team/team-views.ts — the team views the interface PRs draw from (plan PL-2b′): the daemon's roster joined
// with this window's tabs. Data, not a layout: every member on the roster has a seat whether or not a tab shows it,
// and `tabId` says which open tab (if any) stands for the seat. Pure; `useTeamViews` (hooks/) feeds it from the stores.
//
// A tab shows a seat when a live `tmux-session` pane of its layout shows the seat's tmux session on the same host. The
// pane's session NAME is the host's session-list row with the pane's code, else the pane's `cachedName` (the list may
// not have arrived yet, or the session may be gone from it).
//
// The primary pane of a tab is `getPrimaryPane`'s: the first leaf in layout pre-order. `Seat.paneIndex` is the
// pre-order index of the pane that shows the seat in its chosen tab, so 0 is the primary pane.
import type { Tab } from '../../types/tab'
import type { Session } from '../host-api'
import { collectLeaves } from '../pane-tree'
import { fnv1a32 } from './fnv1a'
import type { RosterSession, TeamRoster } from './roster'

export interface Seat {
  role: 'lead' | 'member'
  session: RosterSession
  /** A member's roster state; the lead of a live team is `active`. */
  state: string
  /** How the member joined (`spawned` | `adopted`); null for the lead. */
  origin: string | null
  /** A member's `joined_at`; the lead's is its team's `created_at`. */
  joinedAt: number
  label: string
  /** The open tab standing for this seat, or null when none shows its session. */
  tabId: string | null
  workspaceId: string | null
  /** Pre-order index of the pane showing the seat in `tabId` (0 = the primary pane); null with `tabId`. */
  paneIndex: number | null
}

export interface TeamView {
  /** `<hostId>\0<teamId>` — see `teamKeyOf`. */
  key: string
  hostId: string
  teamId: string
  createdAt: number
  /** FNV-1a 32 of `teamId` mod 8: stable across windows and restarts. The palette belongs to the interface PRs. */
  colorIndex: number
  lead: Seat
  members: Seat[]
}

export interface TeamViewsInput {
  rosterByHost: Record<string, TeamRoster[]>
  tabsById: Record<string, Pick<Tab, 'layout'>>
  workspaces: ReadonlyArray<{ id: string; tabs: readonly string[] }>
  activeWorkspaceId: string | null
  sessionsByHost: Record<string, ReadonlyArray<Pick<Session, 'code' | 'name'>>>
  /** Per team key, the session ids in the order the person arranged. Absent → join order. */
  memberOrder?: Record<string, readonly string[]>
  /** Hosts listed here come first, in this order; the others follow. */
  hostOrder?: readonly string[]
}

export const COLOR_COUNT = 8

export function teamKeyOf(hostId: string, teamId: string): string {
  return `${hostId}\u0000${teamId}`
}

const sessionKey = (hostId: string, name: string) => `${hostId}\u0000${name}`

/** The seat's display name: title, else the name part of the address (after the last `/`), else the ref. */
function labelOf(s: RosterSession): string {
  if (s.title) return s.title
  const name = s.address.slice(s.address.lastIndexOf('/') + 1)
  return name || s.ref
}

interface Candidate {
  tabId: string
  workspaceId: string | null
  /** Position in the workspace's tab list (or, for tabs in no workspace, in `tabsById`). */
  pos: number
  paneIndex: number
}

/** Session key → the tabs showing it (one entry per tab: its first matching pane in pre-order). */
function indexTabs(input: TeamViewsInput): Map<string, Candidate[]> {
  const placement = new Map<string, { workspaceId: string | null; pos: number }>()
  for (const w of input.workspaces) {
    w.tabs.forEach((tabId, pos) => {
      if (!placement.has(tabId)) placement.set(tabId, { workspaceId: w.id, pos })
    })
  }
  const index = new Map<string, Candidate[]>()
  Object.keys(input.tabsById).forEach((tabId, order) => {
    const tab = input.tabsById[tabId]
    const where = placement.get(tabId) ?? { workspaceId: null, pos: order }
    const seen = new Set<string>()
    collectLeaves(tab.layout).forEach((pane, paneIndex) => {
      const c = pane.content
      if (c.kind !== 'tmux-session' || c.terminated) return
      const listed = input.sessionsByHost[c.hostId]?.find((r) => r.code === c.sessionCode)
      const key = sessionKey(c.hostId, listed?.name ?? c.cachedName)
      if (seen.has(key)) return
      seen.add(key)
      const list = index.get(key)
      const candidate = { tabId, ...where, paneIndex }
      if (list) list.push(candidate)
      else index.set(key, [candidate])
    })
  })
  return index
}

/**
 * Which of a seat's candidate tabs stands for it. The workspaces are tried in `preferred` order (a member: the lead's
 * tab's workspace, then the active one; the lead: the active one), then in workspace order, tabs held by no workspace
 * last. Inside the first workspace that has a candidate, a tab whose primary pane shows the session beats one where
 * only a secondary pane does, then the workspace's tab order.
 */
function chooseTab(
  candidates: Candidate[] | undefined,
  preferred: ReadonlyArray<string | null>,
  workspaceOrder: ReadonlyArray<string | null>,
): Candidate | null {
  if (!candidates?.length) return null
  for (const wsId of [...preferred.filter((w) => w !== null), ...workspaceOrder]) {
    const inWs = candidates.filter((c) => c.workspaceId === wsId)
    if (inWs.length === 0) continue
    inWs.sort((a, b) => (a.paneIndex === 0 ? 0 : 1) - (b.paneIndex === 0 ? 0 : 1) || a.pos - b.pos)
    return inWs[0]
  }
  return null
}

function orderMembers(team: TeamRoster, listed: readonly string[] | undefined) {
  const byId = new Map(team.members.map((m) => [m.session_id, m]))
  const first = new Set<string>()
  for (const id of listed ?? []) {
    if (byId.has(id) && id !== team.lead.session_id) first.add(id)
  }
  const rest = team.members
    .filter((m) => !first.has(m.session_id))
    .sort((a, b) => a.joined_at - b.joined_at) // stable: ties keep the daemon's join order
  return [...[...first].map((id) => byId.get(id)!), ...rest]
}

export function selectTeamViews(input: TeamViewsInput): TeamView[] {
  const index = indexTabs(input)
  const workspaceOrder: Array<string | null> = [...input.workspaces.map((w) => w.id), null]
  const hosts = Object.keys(input.rosterByHost)
  const ordered = [
    ...(input.hostOrder ?? []).filter((h) => hosts.includes(h)),
    ...hosts.filter((h) => !input.hostOrder?.includes(h)),
  ]

  const seat = (
    hostId: string, role: Seat['role'], session: RosterSession,
    extra: { origin: string | null; state: string; joinedAt: number }, preferred: ReadonlyArray<string | null>,
  ): Seat => {
    const found = session.tmux_session
      ? chooseTab(index.get(sessionKey(hostId, session.tmux_session)), preferred, workspaceOrder)
      : null
    return {
      role, session, ...extra, label: labelOf(session),
      tabId: found?.tabId ?? null, workspaceId: found?.workspaceId ?? null, paneIndex: found?.paneIndex ?? null,
    }
  }

  const views: TeamView[] = []
  for (const hostId of ordered) {
    for (const team of input.rosterByHost[hostId] ?? []) {
      const key = teamKeyOf(hostId, team.id)
      const lead = seat(hostId, 'lead', team.lead,
        { origin: null, state: 'active', joinedAt: team.created_at }, [input.activeWorkspaceId])
      const preferred = [lead.workspaceId, input.activeWorkspaceId]
      views.push({
        key, hostId, teamId: team.id, createdAt: team.created_at,
        colorIndex: fnv1a32(team.id) % COLOR_COUNT,
        lead,
        members: orderMembers(team, input.memberOrder?.[key]).map((m) =>
          seat(hostId, 'member', m, { origin: m.origin, state: m.state, joinedAt: m.joined_at }, preferred)),
      })
    }
  }
  return views
}

/**
 * The team and role a tab is drawn as, or null for a tab no seat chose. A tab showing panes of two teams belongs to the
 * one whose seat sits in the earliest pane — the primary pane's team, else the first matching pane in layout pre-order.
 */
export function teamOfTab(views: readonly TeamView[], tabId: string): { key: string; role: Seat['role']; seat: Seat } | null {
  let best: { key: string; role: Seat['role']; seat: Seat } | null = null
  for (const v of views) {
    for (const s of [v.lead, ...v.members]) {
      if (s.tabId !== tabId || s.paneIndex === null) continue
      if (best === null || s.paneIndex < best.seat.paneIndex!) best = { key: v.key, role: s.role, seat: s }
    }
  }
  return best
}
