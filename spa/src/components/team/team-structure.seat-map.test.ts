// spa/src/components/team/team-structure.seat-map.test.ts — the inclusive seat map (task groups spec §3, TG-3a-1). A pinned tab that shows a team seat's session is the team's: the panel routing (`panelTeam`) reads the
// inclusive map and shows the team panel. Every OTHER reader stays on `byTabId`, which leaves a pinned tab out (a pinned tab is never drawn as grouped): the pin guard (`isTeamTab`), the tab marks and the
// lead's beads, the member collapse of the sidebar. Each is pinned here, so moving one of them onto the inclusive map is a visible change.
import { describe, it, expect } from 'vitest'
import { buildTeamIndex } from '../../lib/team/team-index'
import { selectTeamViews, type TeamViewsInput } from '../../lib/team/team-views'
import { buildTeamDisplay, structureSignature, type StructureInput } from './team-structure'
import type { RosterMember, RosterSession, TeamRoster } from '../../lib/team/roster'
import type { PaneLayout, Tab } from '../../types/tab'

const sess = (id: string, tmux?: string): RosterSession => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true, ...(tmux ? { tmux_session: tmux } : {}) })
const mem = (id: string, joined: number, tmux?: string): RosterMember => ({ ...sess(id, tmux), state: 'active', origin: 'spawned', joined_at: joined })
const team = (id: string, lead: RosterSession, members: RosterMember[] = []): TeamRoster => ({ id, host_id: 'daemon', created_at: 1, team_name: '', team_label: '', lead, members })
let seq = 0
const leaf = (name: string): PaneLayout => ({
  type: 'leaf', pane: { id: `p${++seq}`, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: `code-${name}`, mode: 'terminal', cachedName: name, tmuxInstance: 'i' } },
})
const tab = (id: string, layout: PaneLayout, pinned = false): Tab => ({ id, pinned, locked: false, createdAt: 0, layout })

function build(pinnedIds: string[], m1Session = 'a-tm') {
  const tabs = [tab('lead', leaf('lead-tm')), tab('m1', leaf(m1Session)), tab('m2', leaf('b-tm')), tab('plain', leaf('nobody'))].map((t) => (pinnedIds.includes(t.id) ? { ...t, pinned: true } : t))
  const inp: TeamViewsInput = {
    rosterByHost: { h1: [team('t1', sess('L', 'lead-tm'), [mem('A', 1, 'a-tm'), mem('B', 2, 'b-tm')])] },
    tabsById: Object.fromEntries(tabs.map((t) => [t.id, t])),
    workspaces: [{ id: 'w1', tabs: tabs.map((t) => t.id) }],
    activeWorkspaceId: 'w1',
    sessionsByHost: {},
  }
  const views = selectTeamViews(inp)
  const index = buildTeamIndex(views, inp.tabsById, inp.sessionsByHost)
  const structure: StructureInput = { views, index, workspaces: inp.workspaces, sessionsByHost: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {}, beadHost: false }
  return { structure, index, display: buildTeamDisplay(structure, { onToggleCollapse: () => {}, onOpenSeat: () => {}, onReorderMembers: () => {} }) }
}

describe('the inclusive seat map (TG-3a-1)', () => {
  it('a pinned seat tab routes to the team panel; so does an unpinned one; a plain tab does not', () => {
    const { display } = build(['m1'])
    expect(display.panelTeam('m1')).not.toBeNull()
    expect(display.panelTeam('m2')).not.toBeNull()
    expect(display.panelTeam('m1')?.teamKey).toBeDefined()
    expect(display.panelTeam('m1')?.teamKey).toBe(display.panelTeam('m2')?.teamKey)
    expect(display.panelTeam('plain')).toBeNull()
    expect(display.panelTeam(null)).toBeNull()
  })

  it('a pinned LEAD tab is the team\'s too, though the team has no group then', () => {
    const { display, index } = build(['lead'])
    expect(display.panelTeam('lead')).not.toBeNull()
    expect(index.byTabId.has('m1')).toBe(false) // a team whose lead tab is pinned has no group: its members are not drawn as grouped
    expect(display.panelTeam('m1')).not.toBeNull() // ... but they are still the team's for the panel
    expect(display.sidebarBeads('lead')).toBeNull() // no group: the pinned lead carries no beads
    expect(display.tabMark('lead')).toBeNull()
    expect(display.tabMark('m1')).toBeNull() // and its members are drawn as plain tabs
    expect(display.sidebarHidden(['lead', 'm1', 'm2', 'plain']).size).toBe(0)
  })

  it('byTabId still leaves a pinned tab out — the readers that stay on it (the pin guard, the team tab lifecycle, the run drawing)', () => {
    const { index } = build(['m1'])
    expect(index.byTabId.has('m1')).toBe(false)
    expect(index.byTabId.has('m2')).toBe(true)
    expect(index.seatByTabId.has('m1')).toBe(true)
  })

  it('the tab marks, the lead\'s beads and the sidebar\'s member collapse still read byTabId: a pinned member has no mark and is not hidden by a collapse', () => {
    const { display } = build(['m1'])
    expect(display.tabMark('m1')).toBeNull()
    expect(display.tabMark('m2')).not.toBeNull()
    expect(display.sidebarBeads('lead')).not.toBeNull()
    expect(display.sidebarBeads('m1')).toBeNull()
  })

  it('the structure signature moves when a seat tab gets pinned (the display is rebuilt only when it moves, and panelTeam must not go stale)', () => {
    const before = structureSignature(build([]).structure)
    const after = structureSignature(build(['m1']).structure)
    expect(after).not.toBe(before)
    // a pinned plain tab changes nothing the display reads
    expect(structureSignature(build(['plain']).structure)).toBe(before)
  })

  it('... and when a PINNED tab starts showing a seat\'s session — byTabId is the same either way, only the inclusive map moves', () => {
    const notSeat = build(['m1'], 'somewhere-else')
    const seat = build(['m1'])
    expect([...notSeat.index.byTabId.keys()]).toEqual([...seat.index.byTabId.keys()]) // the tab is pinned: neither shows it
    expect(structureSignature(seat.structure)).not.toBe(structureSignature(notSeat.structure))
    expect(notSeat.display.panelTeam('m1')).toBeNull()
    expect(seat.display.panelTeam('m1')).not.toBeNull()
  })

  it('... and when a pinned tab shows two teams\' seats, the pane order decides the team — swapping the panes moves the signature though no view and no byTabId entry changes', () => {
    const run = (order: 'ab' | 'ba') => {
      const [first, second] = order === 'ab' ? ['lead-tm', 'other-tm'] : ['other-tm', 'lead-tm']
      const tabs = [tab('two', leaf(first), true)]
      tabs[0].layout = { type: 'split', id: `s${++seq}`, direction: 'h', children: [leaf(first), leaf(second)], sizes: [1, 1] }
      const inp: TeamViewsInput = {
        rosterByHost: { h1: [team('t1', sess('L', 'lead-tm'), [mem('A', 1, 'a-tm')]), team('t2', sess('O', 'other-tm'), [mem('B', 1, 'b-tm')])] },
        tabsById: { two: tabs[0] }, workspaces: [{ id: 'w1', tabs: ['two'] }], activeWorkspaceId: 'w1', sessionsByHost: {},
      }
      const views = selectTeamViews(inp)
      const index = buildTeamIndex(views, inp.tabsById, inp.sessionsByHost)
      const structure: StructureInput = { views, index, workspaces: inp.workspaces, sessionsByHost: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {}, beadHost: false }
      return { structure, index, display: buildTeamDisplay(structure, { onToggleCollapse: () => {}, onOpenSeat: () => {}, onReorderMembers: () => {} }) }
    }
    const ab = run('ab')
    const ba = run('ba')
    expect(ab.index.seatByTabId.get('two')?.key).not.toBe(ba.index.seatByTabId.get('two')?.key)
    expect(ab.display.panelTeam('two')?.teamKey).not.toBe(ba.display.panelTeam('two')?.teamKey)
    expect(structureSignature(ab.structure)).not.toBe(structureSignature(ba.structure))
  })
})
