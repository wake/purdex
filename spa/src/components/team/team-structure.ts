// spa/src/components/team/team-structure.ts — the pure half of the team display (plan TI-1a): `structureSignature` says
// whether the STRUCTURE of the teams changed, `buildTeamDisplay` turns one structure into the O(1) readers the surfaces
// call. The provider rebuilds only when the signature does, so a roster frame that moves a seat's model / effort /
// context (live readings, not structure) re-renders nothing.
import type { Session } from '../../lib/host-api'
import { groupLabel, panelName, tooltipOf } from '../../lib/team/team-names'
import type { TeamIndex } from '../../lib/team/team-index'
import { runMemberIds } from '../../lib/team/team-runs'
import type { Seat, TeamView } from '../../lib/team/team-views'
import type { PanelMode } from '../../stores/useTeamUiStore'
import {
  teamColor, type TeamBeads, type TeamDisplay, type TeamGhostLead, type TeamPanelTeam, type TeamSeatView, type TeamTabMark,
} from './team-display'

export interface StructureInput {
  views: readonly TeamView[]
  index: TeamIndex
  workspaces: ReadonlyArray<{ id: string; tabs: readonly string[] }>
  sessionsByHost: Record<string, ReadonlyArray<Pick<Session, 'code' | 'name'>>>
  collapsed: Record<string, boolean>
  panelMode: Record<string, PanelMode>
  ghostWorkspace: Record<string, string>
  beadHost: boolean
}

/** Per host, tmux session name → code (the first row of a name wins, as a scan would). Built once per call. */
type CodeLookup = (hostId: string, name: string) => string

function codeLookup(sessionsByHost: StructureInput['sessionsByHost']): CodeLookup {
  const byHost = new Map<string, Map<string, string>>()
  return (hostId, name) => {
    let names = byHost.get(hostId)
    if (!names) {
      names = new Map()
      for (const r of sessionsByHost[hostId] ?? []) if (!names.has(r.name)) names.set(r.name, r.code)
      byHost.set(hostId, names)
    }
    return names.get(name) ?? ''
  }
}

function seatView(seat: Seat, codeOf: CodeLookup): TeamSeatView {
  const name = seat.session.tmux_session
  // The seat's own host (a remote member lives elsewhere); '' while this Mac has no such host, so no light is keyed to it.
  const hostId = seat.hostId ?? ''
  const code = name && seat.hostId !== null ? codeOf(seat.hostId, name) : ''
  return { sessionId: seat.session.session_id, title: seat.label, hostId, sessionCode: code, role: seat.role, tabId: seat.tabId, state: seat.state, hostAlias: seat.hostAlias }
}

/**
 * Everything the readers depend on and nothing they do not: team keys, seat ids / roles / titles / tabs / session codes,
 * label / name / colour, the order of each workspace's team tabs, collapse, panel mode, ghost workspaces, the bead
 * setting. NOT a seat's model, effort, context or liveness.
 */
export function structureSignature(input: StructureInput): string {
  const { views, index, workspaces, sessionsByHost } = input
  const codeOf = codeLookup(sessionsByHost)
  return JSON.stringify([
    views.map((v) => [
      v.key, v.name, v.label, v.colorIndex,
      [v.lead, ...v.members].map((s) => {
        const sv = seatView(s, codeOf)
        return [sv.sessionId, sv.role, sv.title, sv.tabId, sv.sessionCode, s.session.tmux_session ?? '', s.hostId, s.hostAlias, s.state]
      }),
    ]),
    workspaces.map((w) => [w.id, w.tabs.filter((id) => index.byTabId.has(id)).map((id) => { const h = index.byTabId.get(id)!; return `${id}\u0000${h.key}\u0000${h.role}` })]),
    Object.keys(input.collapsed).sort(),
    Object.entries(input.panelMode).sort(),
    Object.entries(input.ghostWorkspace).sort(),
    input.beadHost,
  ])
}

const NOOP = () => {}

export interface TeamActions {
  onToggleCollapse: (teamKey: string) => void
  onOpenSeat: (teamKey: string, sessionId: string) => void
  onReorderMembers: (teamKey: string, sessionIds: string[]) => void
}

const NOOP_ACTIONS: TeamActions = { onToggleCollapse: NOOP, onOpenSeat: NOOP, onReorderMembers: NOOP }

export function buildTeamDisplay(input: StructureInput, actions: TeamActions = NOOP_ACTIONS): TeamDisplay {
  const { views, index, workspaces, sessionsByHost, collapsed, panelMode, ghostWorkspace } = input

  const codeOf = codeLookup(sessionsByHost)
  const seatsOf = (v: TeamView) => ({
    lead: seatView(v.lead, codeOf),
    members: v.members.map((m) => seatView(m, codeOf)),
  })

  // Marks: for each team tab, where it sits among the VISIBLE tabs of its group in its workspace.
  const marks = new Map<string, TeamTabMark>()
  const workspaceOf = new Map<string, readonly string[]>()
  for (const w of workspaces) for (const id of w.tabs) if (!workspaceOf.has(id)) workspaceOf.set(id, w.tabs)
  for (const [tabId, hit] of index.byTabId) {
    const view = index.byKey.get(hit.key)
    if (!view) continue
    const isCollapsed = collapsed[hit.key] === true
    const inWorkspace = workspaceOf.get(tabId) ?? [tabId]
    const group = inWorkspace.filter((id) => index.byTabId.get(id)?.key === hit.key)
    const visible = group.filter((id) => !(isCollapsed && index.byTabId.get(id)?.role === 'member'))
    const label = groupLabel(view)
    marks.set(tabId, {
      teamKey: hit.key, color: teamColor(view.colorIndex), label: label.text, full: label.full, truncated: label.truncated,
      tooltip: tooltipOf(view), role: hit.role, seatState: hit.seat.state, hostAlias: hit.seat.hostAlias,
      first: visible[0] === tabId, last: visible[visible.length - 1] === tabId,
      collapsed: isCollapsed, hidden: isCollapsed && hit.role === 'member',
      hiddenCount: isCollapsed ? group.filter((id) => index.byTabId.get(id)?.role === 'member').length : 0,
    })
  }

  const beads = new Map<string, TeamBeads>()
  for (const [tabId, hit] of index.byTabId) {
    if (hit.role !== 'lead') continue
    const view = index.byKey.get(hit.key)
    if (!view) continue
    beads.set(tabId, { teamKey: hit.key, color: teamColor(view.colorIndex), collapsed: collapsed[hit.key] === true, members: seatsOf(view).members })
  }

  const panels = new Map<string, TeamPanelTeam>()
  const panelOf = (key: string): TeamPanelTeam | null => {
    const cached = panels.get(key)
    if (cached) return cached
    const view = index.byKey.get(key)
    if (!view) return null
    const name = panelName(view)
    const { lead, members } = seatsOf(view)
    const t: TeamPanelTeam = {
      teamKey: key, color: teamColor(view.colorIndex), name: name.text, unnamed: name.unnamed, tooltip: tooltipOf(view),
      mode: panelMode[key] ?? 'full', lead, members,
    }
    panels.set(key, t)
    return t
  }

  const ghosts = new Map<string, TeamGhostLead[]>()
  for (const v of views) {
    const at = ghostWorkspace[v.key]
    if (at === undefined || v.lead.tabId !== null) continue
    const label = groupLabel(v)
    const { lead, members } = seatsOf(v)
    const list = ghosts.get(at) ?? []
    list.push({ teamKey: v.key, color: teamColor(v.colorIndex), label: label.text, full: label.full, collapsed: collapsed[v.key] === true, lead, members })
    ghosts.set(at, list)
  }

  return {
    beadHost: input.beadHost,
    tabMark: (tabId) => marks.get(tabId) ?? null,
    sidebarHidden: (tabIds) => runMemberIds(tabIds, (id) => index.byTabId.get(id)),
    sidebarBeads: (tabId) => beads.get(tabId) ?? null,
    ghostLeads: (workspaceId) => (workspaceId === null ? [] : ghosts.get(workspaceId) ?? []),
    panelTeam: (activeTabId) => {
      const hit = activeTabId === null ? undefined : index.byTabId.get(activeTabId)
      return hit ? panelOf(hit.key) : null
    },
    ...actions,
  }
}
