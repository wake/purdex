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
  /** The SPA host id where the seat's tmux session lives: the team's host for a local seat; for a remote member (cross-host
   *  team) the configured host whose `daemonId` is the roster's `host_id`; null when this Mac has no such host (or two).
   *  A seat with a null host matches no tab. NOT the team key's host (`TeamView.hostId` is the lead's). */
  hostId: string | null
  /** The alias of the host a remote member lives on (the roster's `host_alias`); '' for a local seat. */
  hostAlias: string
  /** The open tab standing for this seat, or null when none shows its session. */
  tabId: string | null
  workspaceId: string | null
  /** Pre-order index of the pane showing the seat in its chosen `tabId` (0 = the primary pane); null with `tabId`.
   *  About the chosen tab only — another tab may show the seat too (`teamOfTab` reads each tab's own panes). */
  paneIndex: number | null
}

export interface TeamView {
  /** `<hostId>\0<teamId>` — see `teamKeyOf`. */
  key: string
  hostId: string
  teamId: string
  createdAt: number
  /** The team's current name (`team_name`); '' = unnamed. */
  name: string
  /** The team's short label (`team_label`); '' = none (the lead's title stands in, see team-names.ts). */
  label: string
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
  /** Wire (daemon) id → SPA host id, for the members of a cross-host team that live on another host. Absent = none. */
  hostIdByDaemonId?: Record<string, string>
}

export const COLOR_COUNT = 8

/** Wire (daemon) id → SPA host id over the configured hosts. A host without a `daemonId`, or one in `excluded` (the
 *  runtime saw another daemon answer there), is ignored; an id two hosts claim is left out (unmapped) rather than guessed. */
export function daemonIdMap(
  hosts: Record<string, { daemonId?: string }>, excluded?: (hostId: string) => boolean,
): Record<string, string> {
  const map: Record<string, string> = {}
  const clash = new Set<string>()
  for (const [hostId, h] of Object.entries(hosts)) {
    if (excluded?.(hostId)) continue
    const d = h.daemonId
    if (!d) continue
    if (d in map) clash.add(d)
    else map[d] = hostId
  }
  for (const d of clash) delete map[d]
  return map
}

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

interface ShownSession {
  key: string
  /** Pre-order index of the (first) pane showing the session — counted over all leaves, so 0 is the primary pane. */
  paneIndex: number
}

/**
 * The sessions a tab's layout shows, in layout pre-order, one entry per session (its first pane). Only live
 * `tmux-session` panes count. The single place that decides "which session does this pane show": `indexTabs` (which tab
 * stands for a seat) and `teamOfTab` (which seat does a tab's pane show) both read it, so they cannot disagree.
 */
export function shownSessions(
  layout: Pick<Tab, 'layout'>['layout'],
  sessionsByHost: TeamViewsInput['sessionsByHost'],
): ShownSession[] {
  const shown: ShownSession[] = []
  const seen = new Set<string>()
  collectLeaves(layout).forEach((pane, paneIndex) => {
    const c = pane.content
    if (c.kind !== 'tmux-session' || c.terminated) return
    const listed = sessionsByHost[c.hostId]?.find((r) => r.code === c.sessionCode)
    const key = sessionKey(c.hostId, listed?.name ?? c.cachedName)
    if (seen.has(key)) return
    seen.add(key)
    shown.push({ key, paneIndex })
  })
  return shown
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
    const where = placement.get(tabId) ?? { workspaceId: null, pos: order }
    for (const { key, paneIndex } of shownSessions(input.tabsById[tabId].layout, input.sessionsByHost)) {
      const candidate = { tabId, ...where, paneIndex }
      const list = index.get(key)
      if (list) list.push(candidate)
      else index.set(key, [candidate])
    }
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
    team: TeamRoster, leadHostId: string, role: Seat['role'], session: RosterSession,
    extra: { origin: string | null; state: string; joinedAt: number }, preferred: ReadonlyArray<string | null>,
  ): Seat => {
    // A member whose `host_id` is another host's lives there: the SPA host that daemon id maps to, else null (not configured).
    const remote = !!session.host_id && session.host_id !== team.host_id
    const hostId = remote ? input.hostIdByDaemonId?.[session.host_id!] ?? null : leadHostId
    const found = session.tmux_session && hostId !== null
      ? chooseTab(index.get(sessionKey(hostId, session.tmux_session)), preferred, workspaceOrder)
      : null
    return {
      role, session, ...extra, label: labelOf(session), hostId, hostAlias: remote ? session.host_alias ?? '' : '',
      tabId: found?.tabId ?? null, workspaceId: found?.workspaceId ?? null, paneIndex: found?.paneIndex ?? null,
    }
  }

  const views: TeamView[] = []
  for (const hostId of ordered) {
    for (const team of input.rosterByHost[hostId] ?? []) {
      const key = teamKeyOf(hostId, team.id)
      const lead = seat(team, hostId, 'lead', team.lead,
        { origin: null, state: 'active', joinedAt: team.created_at }, [input.activeWorkspaceId])
      const preferred = [lead.workspaceId, input.activeWorkspaceId]
      views.push({
        key, hostId, teamId: team.id, createdAt: team.created_at,
        name: team.team_name ?? '', label: team.team_label ?? '',
        colorIndex: fnv1a32(team.id) % COLOR_COUNT,
        lead,
        members: orderMembers(team, input.memberOrder?.[key]).map((m) =>
          seat(team, hostId, 'member', m, { origin: m.origin, state: m.state, joinedAt: m.joined_at }, preferred)),
      })
    }
  }
  return views
}

export interface SeatHit {
  key: string
  role: Seat['role']
  seat: Seat
}

/**
 * Session key (`<the SEAT's hostId>\0<tmux session name>`; a remote member's host, not the view's) → the seat that session is, over every view. A session two views claim
 * belongs to the first view (view order). One pass over the seats: `teamOfTab` reads it per call and `buildTeamIndex`
 * (team-index.ts) once for every tab.
 */
export function seatLookup(views: readonly TeamView[]): Map<string, SeatHit> {
  const seats = new Map<string, SeatHit>()
  for (const v of views) {
    for (const seat of [v.lead, ...v.members]) {
      const name = seat.session.tmux_session
      if (!name || seat.hostId === null) continue // a seat on a host this Mac lacks shows in no tab
      const k = sessionKey(seat.hostId, name)
      if (!seats.has(k)) seats.set(k, { key: v.key, role: seat.role, seat })
    }
  }
  return seats
}

export interface TeamOfTabInput {
  views: readonly TeamView[]
  tabId: string
  tabsById: TeamViewsInput['tabsById']
  sessionsByHost: TeamViewsInput['sessionsByHost']
}

/**
 * The team and role a tab is drawn as, decided from the tab's OWN panes — not from the tab each seat was chosen for, so
 * the same session open in two tabs, or a split tab whose secondary pane shows another team's member, still resolves.
 * A tab showing panes of two teams belongs to the one whose seat sits in the earliest pane: the primary pane's team (the
 * first leaf), else the first matching pane in layout pre-order — the same rule. `seat` is the roster seat that pane
 * shows; its `tabId`/`paneIndex` describe the seat's chosen tab, which may be another one. Null when no pane matches.
 */
export function teamOfTab(
  { views, tabId, tabsById, sessionsByHost }: TeamOfTabInput,
): SeatHit | null {
  const tab = tabsById[tabId]
  if (!tab) return null
  const seats = seatLookup(views)
  for (const { key } of shownSessions(tab.layout, sessionsByHost)) {
    const hit = seats.get(key)
    if (hit) return hit
  }
  return null
}
