// spa/src/lib/team/team-state.ts — the team views and index over the CURRENT stores, for code that is not a React render:
// actions (team-actions.ts), keyboard shortcuts, the pin guard. A render reads `TeamDisplayProvider`'s memoized value; an
// action runs once per click and may afford one pass over the seats and tabs.
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore, selectDaemonIdMismatch } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { buildTeamIndex, type TeamIndex } from './team-index'
import { daemonIdMap, selectTeamViews, type TeamView } from './team-views'

/** Wire daemon id → SPA host id for team seats, from host config AND runtime: a host where the runtime saw ANOTHER daemon
 *  answer (`selectDaemonIdMismatch`) is left out, so a seat of daemon B never lands on a same-named session of daemon C.
 *  A host that has merely not connected yet keeps its stored id. The one function both the render path (useTeamViews) and
 *  the action path (currentTeamState) call. */
export function teamHostMap(state: Pick<ReturnType<typeof useHostStore.getState>, 'hosts' | 'runtime'>): Record<string, string> {
  return daemonIdMap(state.hosts, (id) => selectDaemonIdMismatch(state, id) !== undefined)
}

export interface TeamState {
  views: TeamView[]
  index: TeamIndex
}

export function currentTeamState(): TeamState {
  const rosterByHost = useTeamRosterStore.getState().byHost
  // Nothing on any roster: no tab can belong to a team, and the common case costs nothing.
  if (Object.values(rosterByHost).every((teams) => teams.length === 0)) {
    return { views: [], index: buildTeamIndex([], {}, {}) }
  }
  const tabsById = useTabStore.getState().tabs
  const sessionsByHost = useSessionStore.getState().sessions
  const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
  const views = selectTeamViews({
    rosterByHost, tabsById, workspaces, activeWorkspaceId, sessionsByHost, hostIdByDaemonId: teamHostMap(useHostStore.getState()),
    memberOrder: useTeamUiStore.getState().memberOrder, hostOrder: useHostStore.getState().hostOrder,
  })
  return { views, index: buildTeamIndex(views, tabsById, sessionsByHost) }
}

/** Whether `tabId` is drawn as a lead or a member of some team (spec R12: such a tab cannot be pinned). */
export function isTeamTab(tabId: string): boolean {
  return currentTeamState().index.byTabId.has(tabId)
}
