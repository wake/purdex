// spa/src/components/team/TeamDisplayProvider.test.tsx — the structure the team surfaces read (plan TI-1a): marks per tab,
// the sidebar fold, the panel's team, and that live readings do not churn the structure.
import { describe, it, expect, beforeEach } from 'vitest'
import { act, render } from '@testing-library/react'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { useTeamDisplay, type TeamDisplay, moveInOrder, teamColor } from './team-display'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTabStore } from '../../stores/useTabStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { teamKeyOf } from '../../lib/team/team-views'
import { fnv1a32 } from '../../lib/team/fnv1a'
import type { RosterMember, RosterSession, TeamRoster } from '../../lib/team/roster'
import type { PaneLayout, Tab } from '../../types/tab'

const sess = (id: string, tmux: string, extra: Partial<RosterSession> = {}): RosterSession => ({
  session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, title: `title ${id}`, live: true, tmux_session: tmux, ...extra,
})
const mem = (id: string, joined: number, tmux: string, extra: Partial<RosterSession> = {}): RosterMember => ({
  ...sess(id, tmux, extra), state: 'active', origin: 'spawned', joined_at: joined,
})
const team = (members: RosterMember[], over: Partial<TeamRoster> = {}): TeamRoster => ({
  id: 't1', host_id: 'daemon', created_at: 1, team_name: '', team_label: '', lead: sess('L', 'lead-tm'), members, ...over,
})
const leaf = (name: string): PaneLayout => ({
  type: 'leaf',
  pane: { id: `p-${name}`, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: `code-${name}`, mode: 'terminal', cachedName: name, tmuxInstance: 'i' } },
})
const tab = (id: string, name: string): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout: leaf(name) })
const KEY = teamKeyOf('h1', 't1')

let display: TeamDisplay | null = null
let renders = 0
function Probe() {
  display = useTeamDisplay()
  renders++
  return null
}

function seed(teams: TeamRoster[], tabs: Tab[]) {
  act(() => {
    useTeamRosterStore.setState({ byHost: { h1: teams } })
    useTabStore.setState({ tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), tabOrder: tabs.map((t) => t.id), activeTabId: null })
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w1', name: 'W', tabs: tabs.map((t) => t.id), activeTabId: null } as never], activeWorkspaceId: 'w1',
    })
    useSessionStore.setState({
      sessions: { h1: [{ code: 'code-lead-tm', name: 'lead-tm', mode: 'terminal', cwd: '~' }, { code: 'code-a-tm', name: 'a-tm', mode: 'terminal', cwd: '~' }] as never },
    })
  })
}

beforeEach(() => {
  display = null
  renders = 0
  useTeamUiStore.setState({ memberOrder: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {}, teamBeadHost: true })
  useTeamRosterStore.getState().reset()
})

const tabs = [tab('lead', 'lead-tm'), tab('ma', 'a-tm'), tab('mb', 'b-tm'), tab('plain', 'other')]
const roster = () => team([mem('A', 1, 'a-tm'), mem('B', 2, 'b-tm'), mem('C', 3, 'c-tm')])

describe('TeamDisplayProvider — marks, fold, panel', () => {
  it('tabMark: the lead and members of a team get its colour, label and role; a non-team tab none', () => {
    render(<TeamDisplayProvider><Probe /></TeamDisplayProvider>)
    seed([roster()], tabs)
    const lead = display!.tabMark('lead')!
    expect(lead).toMatchObject({ teamKey: KEY, role: 'lead', color: teamColor(fnv1a32('t1') % 8), label: 'title L', collapsed: false, hidden: false, hiddenCount: 0 })
    expect(display!.tabMark('ma')).toMatchObject({ role: 'member', teamKey: KEY })
    expect(display!.tabMark('plain')).toBeNull()
    expect([display!.tabMark('lead')!.first, display!.tabMark('lead')!.last]).toEqual([true, false])
    expect([display!.tabMark('ma')!.first, display!.tabMark('ma')!.last]).toEqual([false, false])
    expect([display!.tabMark('mb')!.first, display!.tabMark('mb')!.last]).toEqual([false, true])
  })

  it('the team label / name win over the lead title; the tooltip carries both', () => {
    render(<TeamDisplayProvider><Probe /></TeamDisplayProvider>)
    seed([team([mem('A', 1, 'a-tm')], { team_name: 'Release train', team_label: '發版' })], tabs)
    expect(display!.tabMark('lead')).toMatchObject({ label: '發版', tooltip: 'Release train (發版)' })
    expect(display!.panelTeam('lead')).toMatchObject({ name: 'Release train', unnamed: false })
  })

  it('a collapsed team hides its member tabs: they are marked hidden, the lead counts them, first/last skip them', () => {
    render(<TeamDisplayProvider><Probe /></TeamDisplayProvider>)
    seed([roster()], tabs)
    act(() => useTeamUiStore.getState().setCollapsed(KEY, true))
    expect(display!.tabMark('ma')).toMatchObject({ hidden: true, collapsed: true })
    expect(display!.tabMark('lead')).toMatchObject({ collapsed: true, hiddenCount: 2, first: true, last: true })
  })

  it('sidebarHidden is true for member tabs only', () => {
    render(<TeamDisplayProvider><Probe /></TeamDisplayProvider>)
    seed([roster()], tabs)
    expect(['lead', 'ma', 'mb', 'plain'].map((id) => display!.sidebarHidden(id))).toEqual([false, true, true, false])
  })

  it('sidebarBeads: the lead tab gets every roster member in team order (opened or not); other tabs none', () => {
    render(<TeamDisplayProvider><Probe /></TeamDisplayProvider>)
    seed([roster()], tabs)
    act(() => useTeamUiStore.getState().setMemberOrder(KEY, ['C', 'A']))
    const beads = display!.sidebarBeads('lead')!
    expect(beads.members.map((m) => [m.sessionId, m.tabId])).toEqual([['C', null], ['A', 'ma'], ['B', 'mb']])
    expect(beads.members[1]).toMatchObject({ title: 'title A', hostId: 'h1', sessionCode: 'code-a-tm', role: 'member' })
    expect(display!.sidebarBeads('ma')).toBeNull()
    expect(display!.sidebarBeads('plain')).toBeNull()
  })

  it('panelTeam: a member tab resolves to its team, with the mode; a non-team tab and null to null', () => {
    render(<TeamDisplayProvider><Probe /></TeamDisplayProvider>)
    seed([roster()], tabs)
    expect(display!.panelTeam('ma')).toMatchObject({ teamKey: KEY, name: 'title L', unnamed: true, mode: 'full' })
    expect(display!.panelTeam('ma')!.lead.sessionId).toBe('L')
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    expect(display!.panelTeam('lead')!.mode).toBe('line')
    expect(display!.panelTeam('plain')).toBeNull()
    expect(display!.panelTeam(null)).toBeNull()
  })

  it('ghostLeads: a team whose lead has no tab is listed in its remembered workspace only', () => {
    render(<TeamDisplayProvider><Probe /></TeamDisplayProvider>)
    seed([roster()], [tab('ma', 'a-tm'), tab('plain', 'other')]) // the lead tab is closed
    expect(display!.ghostLeads('w1')).toEqual([])
    act(() => useTeamUiStore.getState().setGhostWorkspace(KEY, 'w1'))
    const ghosts = display!.ghostLeads('w1')
    expect(ghosts).toHaveLength(1)
    expect(ghosts[0]).toMatchObject({ teamKey: KEY, lead: { sessionId: 'L', tabId: null }, label: 'title L' })
    expect(ghosts[0].members.map((m) => m.sessionId)).toEqual(['A', 'B', 'C'])
    expect(display!.ghostLeads('w2')).toEqual([])
    seed([roster()], tabs) // the lead has a tab again: no ghost
    expect(display!.ghostLeads('w1')).toEqual([])
  })

  it('the bead setting is exposed', () => {
    render(<TeamDisplayProvider><Probe /></TeamDisplayProvider>)
    seed([roster()], tabs)
    expect(display!.beadHost).toBe(true)
    act(() => useTeamUiStore.getState().setTeamBeadHost(false))
    expect(display!.beadHost).toBe(false)
  })

  it('without a provider there is no display (the surfaces draw as before)', () => {
    render(<Probe />)
    expect(display).toBeNull()
  })
})

describe('TeamDisplayProvider — the structure value is stable', () => {
  it('a roster frame that only changes a seat\'s model / effort / context does not re-render a structure consumer', () => {
    render(<TeamDisplayProvider><Probe /></TeamDisplayProvider>)
    seed([roster()], tabs)
    const first = display
    const before = renders
    const live = roster()
    live.members[0] = { ...live.members[0], model: 'opus', effort: 'high', context: { used_percentage: 40, window: 200000, at: 9 } }
    live.lead = { ...live.lead, model: 'sonnet' }
    act(() => useTeamRosterStore.getState().apply('h1', [live]))
    expect(display).toBe(first)
    expect(renders).toBe(before)
  })

  it('a membership change (a member joins), a label change, a collapse, an order change and a tab change do re-render', () => {
    render(<TeamDisplayProvider><Probe /></TeamDisplayProvider>)
    seed([roster()], tabs)
    const seen = [display]
    const step = (f: () => void) => {
      act(f)
      expect(display).not.toBe(seen[seen.length - 1])
      seen.push(display)
    }
    step(() => useTeamRosterStore.getState().apply('h1', [team([...roster().members, mem('D', 4, 'd-tm')])]))
    step(() => useTeamRosterStore.getState().apply('h1', [team([...roster().members, mem('D', 4, 'd-tm')], { team_label: '新' })]))
    step(() => useTeamUiStore.getState().setCollapsed(KEY, true))
    step(() => useTeamUiStore.getState().setMemberOrder(KEY, ['B', 'A']))
    step(() => { // member C gets a tab
      useTabStore.setState({ tabs: { ...useTabStore.getState().tabs, extra: tab('extra', 'c-tm') } })
      useWorkspaceStore.setState({ workspaces: [{ ...useWorkspaceStore.getState().workspaces[0], tabs: [...useWorkspaceStore.getState().workspaces[0].tabs, 'extra'] }] as never })
    })
  })

  it('an unrelated store write (the active tab) leaves the structure alone', () => {
    render(<TeamDisplayProvider><Probe /></TeamDisplayProvider>)
    seed([roster()], tabs)
    const first = display
    act(() => useTabStore.setState({ activeTabId: 'ma' }))
    expect(display).toBe(first)
  })
})

describe('moveInOrder', () => {
  it('moves an id before or after a target and ignores unknown targets', () => {
    expect(moveInOrder(['a', 'b', 'c'], 'c', 'a', false)).toEqual(['c', 'a', 'b'])
    expect(moveInOrder(['a', 'b', 'c'], 'a', 'c', true)).toEqual(['b', 'c', 'a'])
    expect(moveInOrder(['a', 'b'], 'a', 'zzz', true)).toEqual(['a', 'b'])
    expect(moveInOrder(['a', 'b'], 'a', 'a', true)).toEqual(['a', 'b'])
  })
})

describe('structureSignature cost', () => {
  it('a large roster (20 teams x 10 seats, 300 sessions on the host) is signed in a few milliseconds', async () => {
    const { structureSignature } = await import('./team-structure')
    const { selectTeamViews } = await import('../../lib/team/team-views')
    const { buildTeamIndex } = await import('../../lib/team/team-index')
    const teams: TeamRoster[] = Array.from({ length: 20 }, (_, i) => ({
      ...team(Array.from({ length: 9 }, (_, j) => mem(`M${i}-${j}`, j, `m-${i}-${j}`))), id: `t${i}`, lead: sess(`L${i}`, `l-${i}`),
    }))
    const sessions = Array.from({ length: 300 }, (_, i) => ({ code: `c${i}`, name: i < 200 ? `m-${i % 20}-${i % 9}` : `other-${i}` }))
    const sessionsByHost = { h1: sessions }
    const views = selectTeamViews({ rosterByHost: { h1: teams }, tabsById: {}, workspaces: [], activeWorkspaceId: null, sessionsByHost })
    const input = { views, index: buildTeamIndex(views, {}, sessionsByHost), workspaces: [], sessionsByHost, collapsed: {}, panelMode: {}, ghostWorkspace: {}, beadHost: true }
    structureSignature(input) // warm
    const t0 = performance.now()
    for (let i = 0; i < 20; i++) structureSignature(input)
    const perCall = (performance.now() - t0) / 20
    expect(perCall).toBeLessThan(10)
  })
})

describe('TeamDisplayProvider — the actions are wired (TI-1b)', () => {
  it('onToggleCollapse / onOpenSeat / onReorderMembers act on the stores', () => {
    render(<TeamDisplayProvider><Probe /></TeamDisplayProvider>)
    seed([roster()], tabs)
    act(() => display!.onToggleCollapse(KEY))
    expect(useTeamUiStore.getState().collapsed[KEY]).toBe(true)
    act(() => display!.onOpenSeat(KEY, 'A')) // expands and switches to A's tab
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
    expect(useTabStore.getState().activeTabId).toBe('ma')
    act(() => display!.onReorderMembers(KEY, ['C', 'B', 'A']))
    expect(useTeamUiStore.getState().memberOrder[KEY]).toEqual(['C', 'B', 'A'])
  })
})
