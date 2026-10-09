// spa/src/lib/team/team-index.test.ts — the O(1) lookups the surfaces read instead of calling `teamOfTab` per tab (plan
// review #6–#8). The index must agree with `teamOfTab` for every tab.
import { describe, it, expect } from 'vitest'
import { buildTeamIndex } from './team-index'
import { selectTeamViews, teamOfTab, type TeamViewsInput } from './team-views'
import type { RosterMember, RosterSession, TeamRoster } from './roster'
import type { PaneLayout, Tab } from '../../types/tab'

const sess = (id: string, tmux?: string): RosterSession => ({
  session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true, ...(tmux ? { tmux_session: tmux } : {}),
})
const mem = (id: string, joined: number, tmux?: string): RosterMember => ({ ...sess(id, tmux), state: 'active', origin: 'spawned', joined_at: joined })
const team = (id: string, lead: RosterSession, members: RosterMember[] = []): TeamRoster => ({
  id, host_id: 'daemon', created_at: 1, team_name: '', team_label: '', lead, members,
})
let seq = 0
const leaf = (hostId: string, name: string): PaneLayout => ({
  type: 'leaf',
  pane: { id: `p${++seq}`, content: { kind: 'tmux-session', hostId, sessionCode: `code-${name}`, mode: 'terminal', cachedName: name, tmuxInstance: 'i' } },
})
const split = (...children: PaneLayout[]): PaneLayout => ({ type: 'split', id: `s${++seq}`, direction: 'h', children, sizes: children.map(() => 1) })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })

function fixture() {
  const tabs = [
    tab('lead1', leaf('h1', 'lead-tm')),
    tab('member1', leaf('h1', 'a-tm')),
    tab('plain', leaf('h1', 'not-a-team')),
    tab('both', split(leaf('h1', 'b-tm'), leaf('h2', 'x-tm'))), // two teams' sessions in one tab: the first pane's team
    tab('dup', leaf('h1', 'a-tm')), // the same member open twice
  ]
  const inp: TeamViewsInput = {
    rosterByHost: {
      h1: [team('t1', sess('L1', 'lead-tm'), [mem('A', 1, 'a-tm'), mem('B', 2, 'b-tm')])],
      h2: [team('t2', sess('L2', 'x-tm'))],
    },
    tabsById: Object.fromEntries(tabs.map((t) => [t.id, t])),
    workspaces: [{ id: 'w1', tabs: tabs.map((t) => t.id) }],
    activeWorkspaceId: 'w1',
    sessionsByHost: {},
  }
  return { inp, views: selectTeamViews(inp) }
}

describe('buildTeamIndex', () => {
  it('byTabId equals teamOfTab for a lead, a member, a non-team tab, two teams in one tab and a duplicate', () => {
    const { inp, views } = fixture()
    const index = buildTeamIndex(views, inp.tabsById, inp.sessionsByHost)
    for (const tabId of Object.keys(inp.tabsById)) {
      const want = teamOfTab({ views, tabId, tabsById: inp.tabsById, sessionsByHost: inp.sessionsByHost })
      expect(index.byTabId.get(tabId) ?? null, tabId).toEqual(want)
    }
    expect(index.byTabId.get('lead1')?.role).toBe('lead')
    expect(index.byTabId.get('member1')?.role).toBe('member')
    expect(index.byTabId.has('plain')).toBe(false)
    expect(index.byTabId.get('both')?.key).toBe(views[0].key) // the primary pane's team
    expect(index.byTabId.get('dup')?.seat.session.session_id).toBe('A')
  })

  it('byKey maps every team key to its view; bySession finds a seat by host and tmux name', () => {
    const { inp, views } = fixture()
    const index = buildTeamIndex(views, inp.tabsById, inp.sessionsByHost)
    expect([...index.byKey.keys()]).toEqual(views.map((v) => v.key))
    expect(index.byKey.get(views[1].key)).toBe(views[1])
    expect(index.bySession.get('h1\u0000a-tm')?.seat.session.session_id).toBe('A')
    expect(index.bySession.get('h2\u0000x-tm')?.key).toBe(views[1].key)
    expect(index.bySession.get('h2\u0000a-tm')).toBeUndefined() // same name on another host is another session
  })

  it('a session listed by the host (code → name) is matched by its listed name, not the cached one', () => {
    const { inp, views } = fixture()
    const renamed = { ...inp.tabsById, member1: tab('member1', leaf('h1', 'stale-cached')) }
    const sessionsByHost = { h1: [{ code: 'code-stale-cached', name: 'a-tm' }] }
    const index = buildTeamIndex(views, renamed, sessionsByHost)
    expect(index.byTabId.get('member1')?.seat.session.session_id).toBe('A')
  })

  it('a pinned tab is never grouped: it is absent from byTabId even when it shows a team session', () => {
    const { inp, views } = fixture()
    const pinned = { ...inp.tabsById, member1: { ...inp.tabsById.member1, pinned: true }, lead1: { ...inp.tabsById.lead1, pinned: true } }
    const index = buildTeamIndex(views, pinned, inp.sessionsByHost)
    expect(index.byTabId.has('member1')).toBe(false)
    expect(index.byTabId.has('lead1')).toBe(false)
    expect(index.byTabId.get('dup')?.role).toBe('member') // an unpinned tab on the same session is still grouped
    expect(index.bySession.get('h1\u0000a-tm')).toBeDefined() // the seat itself is still a seat
  })

  it('an empty input gives empty maps', () => {
    const index = buildTeamIndex([], {}, {})
    expect(index.byTabId.size + index.byKey.size + index.bySession.size).toBe(0)
  })
})
