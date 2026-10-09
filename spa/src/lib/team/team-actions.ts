// spa/src/lib/team/team-actions.ts — what a click on a team surface does (team interface spec R3, R8–R11, §4.5; plan TI-1b).
//   openTeamSeat       — a bead, a panel row or the session list opens a seat: switch to its tab, else open one in the group;
//   toggleTeamCollapse — collapse / expand, collapsing from a member switches to the lead (R9);
//   visibleTabIds      — the tab list left / right stepping and ⌘1–8 work on: members of a collapsed team are skipped (R8).
// Closing the lead is NOT here: it must hold for every close path, so it is TI-1c's subscriber.
import { useWorkspaceStore } from '../../features/workspace/store'
import { useI18nStore } from '../../stores/useI18nStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { collectLeaves } from '../pane-tree'
import { activateTab, activateTabPane, openSessionTabAt } from '../open-session-tab'
import { isRefShownNow } from '../shown-hosts'
import { currentTeamState } from './team-state'
import type { Seat, TeamView } from './team-views'

export type OpenSeatOutcome =
  | 'activated' // the seat's tab was shown
  | 'opened' // a tab was opened for it
  | 'unlisted' // its tmux session is not in the host's session list yet: toast, nothing opened
  | 'hidden' // its host is hidden in this workbench: nothing opened
  | 'unknown' // no such team / seat

export interface OpenSeatResult {
  outcome: OpenSeatOutcome
  tabId: string | null
}

const seatOf = (view: TeamView, sessionId: string): Seat | undefined =>
  view.lead.session.session_id === sessionId ? view.lead : view.members.find((m) => m.session.session_id === sessionId)

/**
 * Show a seat's existing tab: the pane that shows the seat takes the keyboard (the seat may sit in a secondary pane of a
 * split tab), and the workspace on screen follows when the tab is in another one.
 */
function showSeatTab(seat: Seat): string | null {
  const tab = seat.tabId === null ? undefined : useTabStore.getState().tabs[seat.tabId]
  if (seat.tabId === null || !tab) return null
  const leaves = collectLeaves(tab.layout)
  const pane = leaves[seat.paneIndex ?? 0] ?? leaves[0]
  if (pane) activateTabPane(seat.tabId, pane.id)
  else activateTab(seat.tabId)
  return seat.tabId
}

function notListed(): void {
  useUndoToast.getState().show(useI18nStore.getState().t('team.seat_not_listed'))
}

/** The seat's session as the host lists it (by tmux name), or null while the list does not hold it. */
function listedSession(view: TeamView, seat: Seat) {
  const name = seat.session.tmux_session
  if (!name) return null
  return useSessionStore.getState().sessions[view.hostId]?.find((s) => s.name === name) ?? null
}

/**
 * Open a team seat (R3, R10, §4.5). The group is expanded first when collapsed (R10). A seat with a tab is switched to
 * (never a second tab). Otherwise a tab is opened for its tmux session, right after the group's last tab in the LEAD
 * tab's workspace; a lead with no tab is opened first, in the ghost row's workspace (else the active one), and the
 * ghost entry cleared. A seat whose session the host's list does not hold yet opens nothing and says so.
 */
export function openTeamSeat(teamKey: string, sessionId: string): OpenSeatResult {
  const first = currentTeamState()
  const view = first.index.byKey.get(teamKey)
  const seat = view ? seatOf(view, sessionId) : undefined
  if (!view || !seat) return { outcome: 'unknown', tabId: null }

  const ui = useTeamUiStore.getState()
  if (ui.collapsed[teamKey] === true) ui.setCollapsed(teamKey, false)

  const shown = showSeatTab(seat)
  if (shown !== null) return { outcome: 'activated', tabId: shown }

  if (!isRefShownNow(view.hostId)) return { outcome: 'hidden', tabId: null }

  const workspaces = useWorkspaceStore.getState()

  // A member needs its lead's tab as the anchor of the group: open the lead first when it has none.
  if (seat.role === 'member' && view.lead.tabId === null) {
    const leadSession = listedSession(view, view.lead)
    if (!leadSession) {
      notListed()
      return { outcome: 'unlisted', tabId: null }
    }
    if (!listedSession(view, seat)) {
      notListed()
      return { outcome: 'unlisted', tabId: null }
    }
    const ghostWs = ui.ghostWorkspace[teamKey]
    const wsId = ghostWs !== undefined && workspaces.workspaces.some((w) => w.id === ghostWs) ? ghostWs : workspaces.activeWorkspaceId
    const leadTab = openSessionTabAt(view.hostId, leadSession, wsId === null ? undefined : { workspaceId: wsId })
    if (leadTab === null) return { outcome: 'hidden', tabId: null }
    useTeamUiStore.getState().setGhostWorkspace(teamKey, null)
  }

  const session = listedSession(view, seat)
  if (!session) {
    notListed()
    return { outcome: 'unlisted', tabId: null }
  }

  // Where it goes: the lead tab's workspace (re-read: the lead may have just been opened), after the group's last tab.
  const now = currentTeamState()
  const fresh = now.index.byKey.get(teamKey) ?? view
  let place: { workspaceId: string; afterTabId?: string } | undefined
  if (seat.role === 'lead') {
    const ghostWs = ui.ghostWorkspace[teamKey]
    const wsId = ghostWs !== undefined && workspaces.workspaces.some((w) => w.id === ghostWs) ? ghostWs : workspaces.activeWorkspaceId
    if (wsId !== null) place = { workspaceId: wsId }
  } else if (fresh.lead.tabId !== null) {
    const ws = useWorkspaceStore.getState().findWorkspaceByTab(fresh.lead.tabId)
    if (ws) {
      const group = ws.tabs.filter((id) => now.index.byTabId.get(id)?.key === teamKey)
      place = { workspaceId: ws.id, afterTabId: group[group.length - 1] ?? fresh.lead.tabId }
    }
  }
  const tabId = openSessionTabAt(view.hostId, session, place)
  if (tabId === null) return { outcome: 'hidden', tabId: null }
  if (seat.role === 'lead') useTeamUiStore.getState().setGhostWorkspace(teamKey, null)
  const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
  if (ws && ws.id !== useWorkspaceStore.getState().activeWorkspaceId) useWorkspaceStore.getState().setActiveWorkspace(ws.id)
  return { outcome: 'opened', tabId }
}

/**
 * Collapse or expand a team's group (R8). Collapsing while one of its member tabs is the active tab switches to the lead
 * (R9): the person's tab is about to disappear from the bar.
 */
export function toggleTeamCollapse(teamKey: string): void {
  const ui = useTeamUiStore.getState()
  if (ui.collapsed[teamKey] === true) {
    ui.setCollapsed(teamKey, false)
    return
  }
  const { index } = currentTeamState()
  const view = index.byKey.get(teamKey)
  const activeId = useTabStore.getState().activeTabId
  const activeHit = activeId === null ? undefined : index.byTabId.get(activeId)
  ui.setCollapsed(teamKey, true)
  if (view && activeHit?.key === teamKey && activeHit.role === 'member') showSeatTab(view.lead)
}

/**
 * `tabIds` without the member tabs of collapsed teams (R8): the list stepping and ⌘1–8 / ⌘9 act on, so a hidden member
 * is never landed on. `teamOfTab` answers (key, role) for a tab id, `null` for a tab in no team.
 */
export function visibleTabIds(
  tabIds: readonly string[],
  collapsed: Record<string, boolean>,
  teamOfTab: (tabId: string) => { key: string; role: 'lead' | 'member' } | null | undefined,
): string[] {
  return tabIds.filter((id) => {
    const hit = teamOfTab(id)
    return !(hit && hit.role === 'member' && collapsed[hit.key] === true)
  })
}
