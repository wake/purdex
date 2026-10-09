// spa/src/lib/team/__tests__/team-fixture.ts — one team on one host over the REAL stores, for the action / shortcut /
// menu tests of the team interface (plan TI-1b…). It does NOT set the shown hosts (the import guard counts this file as
// source): a test that opens tabs does `useShownHostsStore.setState({ ids: [HOST] })` itself. Tab ids are what the test names them; a tab's session is a tmux
// session NAME (`lead-tm`, `a-tm`); a listed session has the code `code-<name>`.
import { useHostStore } from '../../../stores/useHostStore'
import { useSessionStore } from '../../../stores/useSessionStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useTeamRosterStore } from '../../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../../stores/useTeamUiStore'
import { useUndoToast } from '../../../stores/useUndoToast'
import { useWorkspaceStore } from '../../../features/workspace/store'
import type { RosterMember, RosterSession, TeamRoster } from '../roster'
import type { Tab } from '../../../types/tab'

export const HOST = 'h1'
export const TEAM = 't1'
export const KEY = `${HOST}\u0000${TEAM}`

export const sess = (id: string, tmux: string): RosterSession => ({
  session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, title: `title ${id}`, live: true, tmux_session: tmux,
})
export const member = (id: string, joined: number, tmux: string): RosterMember => ({
  ...sess(id, tmux), state: 'active', origin: 'spawned', joined_at: joined,
})

export const tabOn = (id: string, tmux: string | null): Tab => ({
  id, pinned: false, locked: false, createdAt: 0,
  layout: tmux === null
    ? { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'new-tab' } } }
    : { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: HOST, sessionCode: `code-${tmux}`, mode: 'terminal', cachedName: tmux, tmuxInstance: 'i' } } },
})

export interface Scene {
  /** roster: the lead's tmux name and the members' (id → tmux name), in join order */
  lead?: string
  members?: Array<[id: string, tmux: string]>
  /** tabs: id → the tmux name it shows (null = a tab that shows no session) */
  tabs: Array<[id: string, tmux: string | null]>
  workspaces: Array<{ id: string; tabs: string[] }>
  activeWorkspaceId?: string
  activeTabId?: string | null
  /** the tmux names the host's session list holds (default: every roster session) */
  listed?: string[]
  teamName?: string
  teamLabel?: string
}

export function seedScene(scene: Scene): void {
  const lead = scene.lead ?? 'lead-tm'
  const members = scene.members ?? []
  const roster: TeamRoster = {
    id: TEAM, host_id: 'daemon', created_at: 1, team_name: scene.teamName ?? '', team_label: scene.teamLabel ?? '',
    lead: sess('L', lead), members: members.map(([id, tm], i) => member(id, i + 1, tm)),
  }
  const listed = scene.listed ?? [lead, ...members.map(([, tm]) => tm)]
  useHostStore.setState({ hostOrder: [HOST] })
  useTeamRosterStore.setState({ byHost: { [HOST]: [roster] } })
  useSessionStore.setState({ sessions: { [HOST]: listed.map((name) => ({ code: `code-${name}`, name, mode: 'terminal', cwd: '~' })) as never } })
  const tabs = scene.tabs.map(([id, tm]) => tabOn(id, tm))
  useTabStore.setState({ tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), tabOrder: tabs.map((t) => t.id), activeTabId: scene.activeTabId ?? null })
  useWorkspaceStore.setState({
    workspaces: scene.workspaces.map((w) => ({ id: w.id, name: w.id, tabs: [...w.tabs], activeTabId: null, moduleConfig: {} })),
    activeWorkspaceId: scene.activeWorkspaceId ?? scene.workspaces[0]?.id ?? null,
  })
}

export function resetTeamStores(): void {
  useTeamRosterStore.getState().reset()
  useTeamUiStore.setState({ memberOrder: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {}, teamDrill: {}, workbookTabs: {}, panel: { width: 312, expanded: false }, teamBeadHost: true })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.getState().reset()
  useUndoToast.setState({ toast: null, notice: null })
}

export const wsTabs = (id: string): string[] => useWorkspaceStore.getState().workspaces.find((w) => w.id === id)?.tabs ?? []
export const tabShowing = (name: string): string | undefined =>
  Object.values(useTabStore.getState().tabs).find((t) => t.layout.type === 'leaf' && t.layout.pane.content.kind === 'tmux-session' && t.layout.pane.content.cachedName === name)?.id
