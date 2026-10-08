// spa/src/lib/team/team-views.test.ts — the team views the interface PRs draw from (plan PL-2b′): who is on each team,
// in which order, and which open tab (if any) shows each seat.
import { describe, it, expect } from 'vitest'
import { selectTeamViews, teamOfTab, teamKeyOf, type TeamViewsInput } from './team-views'
import { fnv1a32 } from './fnv1a'
import type { RosterMember, RosterSession, TeamRoster } from './roster'
import type { PaneLayout, Tab } from '../../types/tab'

const sess = (id: string, tmux?: string, extra: Partial<RosterSession> = {}): RosterSession => ({
  session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, live: true, ...(tmux ? { tmux_session: tmux } : {}), ...extra,
})
const mem = (id: string, joined: number, tmux?: string, extra: Partial<RosterSession> = {}): RosterMember => ({
  ...sess(id, tmux, extra), state: 'active', origin: 'spawned', joined_at: joined,
})
const team = (id: string, lead: RosterSession, members: RosterMember[] = [], createdAt = 100): TeamRoster => ({
  id, host_id: 'daemon', created_at: createdAt, lead, members,
})

let paneSeq = 0
const leaf = (hostId: string, name: string, extra: object = {}): PaneLayout => ({
  type: 'leaf',
  pane: {
    id: `p${++paneSeq}`,
    content: { kind: 'tmux-session', hostId, sessionCode: `code-${name}`, mode: 'terminal', cachedName: name, tmuxInstance: 'i', ...extra },
  },
})
const split = (...children: PaneLayout[]): PaneLayout => ({ type: 'split', id: `s${++paneSeq}`, direction: 'h', children, sizes: children.map(() => 1) })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })

/** Input with sensible empties; each test names only what it cares about. */
function input(over: Partial<TeamViewsInput> & { tabs?: Tab[] } = {}): TeamViewsInput {
  const { tabs = [], ...rest } = over
  return {
    rosterByHost: {},
    tabsById: Object.fromEntries(tabs.map((t) => [t.id, t])),
    workspaces: [],
    activeWorkspaceId: null,
    sessionsByHost: {},
    ...rest,
  }
}
const ws = (id: string, tabs: string[]) => ({ id, tabs })

describe('selectTeamViews — shape and order', () => {
  it('gives each team a key, its created_at, a stable colour index, the lead first and members by joined_at', () => {
    const t = team('t1', sess('L'), [mem('B', 30), mem('A', 10), mem('C', 20)], 777)
    const [v] = selectTeamViews(input({ rosterByHost: { h1: [t] } }))
    expect(v.key).toBe(`h1\u0000t1`)
    expect(v.key).toBe(teamKeyOf('h1', 't1'))
    expect([v.hostId, v.teamId, v.createdAt]).toEqual(['h1', 't1', 777])
    expect(v.lead.role).toBe('lead')
    expect(v.lead.session.session_id).toBe('L')
    expect(v.members.map((m) => [m.role, m.session.session_id, m.joinedAt])).toEqual([['member', 'A', 10], ['member', 'C', 20], ['member', 'B', 30]])
  })

  it('colorIndex is FNV-1a 32 of the team id mod 8: in 0..7, the same on every call, host-independent', () => {
    const views = selectTeamViews(input({ rosterByHost: { h1: [team('alpha', sess('L1')), team('beta', sess('L2'))], h2: [team('alpha', sess('L3'))] } }))
    expect(views.map((v) => v.colorIndex)).toEqual([fnv1a32('alpha') % 8, fnv1a32('beta') % 8, fnv1a32('alpha') % 8])
    for (const v of views) expect(v.colorIndex).toBeGreaterThanOrEqual(0)
    for (const v of views) expect(v.colorIndex).toBeLessThanOrEqual(7)
    const again = selectTeamViews(input({ rosterByHost: { h1: [team('alpha', sess('L1'))] } }))
    expect(again[0].colorIndex).toBe(views[0].colorIndex)
  })

  it('teams stay in roster order per host; hosts follow hostOrder, unlisted hosts after', () => {
    const rosterByHost = {
      c: [team('c1', sess('L5'))],
      a: [team('a2', sess('L1')), team('a1', sess('L2'))],
      b: [team('b1', sess('L3'))],
    }
    expect(selectTeamViews(input({ rosterByHost, hostOrder: ['b', 'a'] })).map((v) => v.key.replace('\u0000', '/')))
      .toEqual(['b/b1', 'a/a2', 'a/a1', 'c/c1'])
  })

  it('a host with no teams contributes nothing', () => {
    expect(selectTeamViews(input({ rosterByHost: { h1: [] } }))).toEqual([])
  })

  it('label: title, else the name part of the address, else the ref', () => {
    const t = team('t', sess('L', undefined, { title: 'Boss', address: 'mlab/boss-aa' }), [
      mem('A', 1, undefined, { address: 'mlab/worker-bb' }),
      mem('B', 2, undefined, { address: '', ref: '_cccccc' }),
      mem('C', 3, undefined, { title: '', address: 'mlab/' , ref: '_dddddd' }),
    ])
    const [v] = selectTeamViews(input({ rosterByHost: { h1: [t] } }))
    expect(v.lead.label).toBe('Boss')
    expect(v.members.map((m) => m.label)).toEqual(['worker-bb', '_cccccc', '_dddddd'])
  })

  it('a seat carries its session whole (model, effort, context) plus state, origin and joined_at', () => {
    const context = { used_percentage: 12, window: 200000, at: 5 }
    const m = mem('A', 7, undefined, { model: 'opus', effort: 'high', context })
    const [v] = selectTeamViews(input({ rosterByHost: { h1: [team('t', sess('L'), [{ ...m, origin: 'adopted' }])] } }))
    expect(v.members[0].session).toMatchObject({ model: 'opus', effort: 'high', context })
    expect([v.members[0].state, v.members[0].origin, v.members[0].joinedAt]).toEqual(['active', 'adopted', 7])
  })
})

describe('selectTeamViews — memberOrder', () => {
  const t = team('t', sess('L'), [mem('A', 10), mem('B', 20), mem('C', 30), mem('D', 40)])
  const key = teamKeyOf('h1', 't')
  const ids = (o?: Record<string, string[]>) =>
    selectTeamViews(input({ rosterByHost: { h1: [t] }, memberOrder: o }))[0].members.map((m) => m.session.session_id)

  it('listed members come first in that order; the rest follow by joined_at', () => {
    expect(ids({ [key]: ['C', 'A'] })).toEqual(['C', 'A', 'B', 'D'])
  })

  it('a new member (not listed) is appended by joined_at; a stale id is ignored', () => {
    expect(ids({ [key]: ['D', 'GONE', 'B'] })).toEqual(['D', 'B', 'A', 'C'])
  })

  it('the lead is never moved or doubled, even when the order lists it', () => {
    const [v] = selectTeamViews(input({ rosterByHost: { h1: [t] }, memberOrder: { [key]: ['B', 'L', 'A'] } }))
    expect(v.lead.session.session_id).toBe('L')
    expect(v.members.map((m) => m.session.session_id)).toEqual(['B', 'A', 'C', 'D'])
  })

  it('an order listing an id twice uses its first place; no order for the team changes nothing', () => {
    expect(ids({ [key]: ['B', 'B', 'A'] })).toEqual(['B', 'A', 'C', 'D'])
    expect(ids({ other: ['D'] })).toEqual(['A', 'B', 'C', 'D'])
    expect(ids()).toEqual(['A', 'B', 'C', 'D'])
  })
})

describe('selectTeamViews — which tab shows a seat', () => {
  it('an unopened member has no tab; a session outside tmux has none either', () => {
    const t = team('t', sess('L', 'lead-tm'), [mem('A', 1, 'a-tm'), mem('B', 2)])
    const [v] = selectTeamViews(input({
      rosterByHost: { h1: [t] },
      tabs: [tab('t1', leaf('h1', 'lead-tm'))],
      workspaces: [ws('w1', ['t1'])],
    }))
    expect([v.lead.tabId, v.lead.workspaceId]).toEqual(['t1', 'w1'])
    expect(v.members.map((m) => [m.tabId, m.workspaceId])).toEqual([[null, null], [null, null]])
  })

  it('a tab shows a seat through a secondary pane of a split too', () => {
    const t = team('t', sess('L', 'lead-tm'), [mem('A', 1, 'a-tm')])
    const [v] = selectTeamViews(input({
      rosterByHost: { h1: [t] },
      tabs: [tab('t1', split(leaf('h1', 'lead-tm'), split(leaf('h1', 'x'), leaf('h1', 'a-tm'))))],
      workspaces: [ws('w1', ['t1'])],
    }))
    expect(v.members[0].tabId).toBe('t1')
    expect(v.lead.tabId).toBe('t1')
  })

  it('does not match another host\'s session of the same name, or a terminated pane', () => {
    const t = team('t', sess('L', 'lead-tm'), [mem('A', 1, 'a-tm')])
    const [v] = selectTeamViews(input({
      rosterByHost: { h1: [t] },
      tabs: [tab('t1', leaf('h2', 'a-tm')), tab('t2', leaf('h1', 'a-tm', { terminated: 'session-closed' }))],
      workspaces: [ws('w1', ['t1', 't2'])],
    }))
    expect(v.members[0].tabId).toBeNull()
  })

  it('the session list names the pane\'s session by its code; an unknown code falls back to cachedName', () => {
    const t = team('t', sess('L', 'renamed'), [mem('A', 1, 'a-tm')])
    const [v] = selectTeamViews(input({
      rosterByHost: { h1: [t] },
      // pane t1 was cached as "old-name", the host now calls that code "renamed"; t2's code is not in the list at all
      tabs: [tab('t1', leaf('h1', 'old-name', { sessionCode: 'c1' })), tab('t2', leaf('h1', 'a-tm', { sessionCode: 'gone' }))],
      workspaces: [ws('w1', ['t1', 't2'])],
      sessionsByHost: { h1: [{ code: 'c1', name: 'renamed', cwd: '', mode: 'terminal' }] },
    }))
    expect(v.lead.tabId).toBe('t1')
    expect(v.members[0].tabId).toBe('t2')
  })

  it('two tabs show one member: the lead\'s workspace wins', () => {
    const t = team('t', sess('L', 'lead-tm'), [mem('A', 1, 'a-tm')])
    const [v] = selectTeamViews(input({
      rosterByHost: { h1: [t] },
      tabs: [tab('tl', leaf('h1', 'lead-tm')), tab('ta1', leaf('h1', 'a-tm')), tab('ta2', leaf('h1', 'a-tm')), tab('tx', leaf('h1', 'x'))],
      // the lead and ta2 live in w2; ta1 is first in tab order and w1 is the active workspace
      workspaces: [ws('w1', ['tx', 'ta1']), ws('w2', ['tl', 'ta2'])],
      activeWorkspaceId: 'w1',
    }))
    expect(v.lead.workspaceId).toBe('w2')
    expect([v.members[0].tabId, v.members[0].workspaceId]).toEqual(['ta2', 'w2'])
  })

  it('...else the active workspace, else the first in tab order', () => {
    const t = team('t', sess('L', 'lead-tm'), [mem('A', 1, 'a-tm')])
    const base = {
      rosterByHost: { h1: [t] },
      tabs: [tab('ta1', leaf('h1', 'a-tm')), tab('ta2', leaf('h1', 'a-tm')), tab('ta3', leaf('h1', 'a-tm'))],
    }
    // the lead has no tab: the active workspace (w2) wins over the earlier w1
    const active = selectTeamViews(input({ ...base, workspaces: [ws('w1', ['ta1']), ws('w2', ['ta2', 'ta3'])], activeWorkspaceId: 'w2' }))[0]
    expect(active.members[0].tabId).toBe('ta2')
    // the active workspace shows none of them: the first in tab order (workspace order, then the workspace's tabs)
    const order = selectTeamViews(input({ ...base, workspaces: [ws('w1', ['ta3', 'ta1']), ws('w2', ['ta2']), ws('w3', [])], activeWorkspaceId: 'w3' }))[0]
    expect(order.members[0].tabId).toBe('ta3')
  })

  it('the lead: the active workspace first, then the first in tab order', () => {
    const t = team('t', sess('L', 'lead-tm'))
    const base = { rosterByHost: { h1: [t] }, tabs: [tab('t1', leaf('h1', 'lead-tm')), tab('t2', leaf('h1', 'lead-tm'))] }
    expect(selectTeamViews(input({ ...base, workspaces: [ws('w1', ['t1']), ws('w2', ['t2'])], activeWorkspaceId: 'w2' }))[0].lead.tabId).toBe('t2')
    expect(selectTeamViews(input({ ...base, workspaces: [ws('w1', ['t1']), ws('w2', ['t2'])], activeWorkspaceId: null }))[0].lead.tabId).toBe('t1')
  })

  it('within one workspace a primary pane beats a secondary one, then workspace tab order', () => {
    const t = team('t', sess('L', 'lead-tm'), [mem('A', 1, 'a-tm')])
    const [v] = selectTeamViews(input({
      rosterByHost: { h1: [t] },
      tabs: [
        tab('secondary', split(leaf('h1', 'x'), leaf('h1', 'a-tm'))),
        tab('primary-late', leaf('h1', 'a-tm')),
        tab('primary-later', leaf('h1', 'a-tm')),
      ],
      workspaces: [ws('w1', ['secondary', 'primary-late', 'primary-later'])],
    }))
    expect(v.members[0].tabId).toBe('primary-late')
  })

  it('a tab that no workspace holds is the last resort, with workspaceId null', () => {
    const t = team('t', sess('L', 'lead-tm'))
    const [v] = selectTeamViews(input({ rosterByHost: { h1: [t] }, tabs: [tab('orphan', leaf('h1', 'lead-tm'))], workspaces: [ws('w1', [])] }))
    expect([v.lead.tabId, v.lead.workspaceId]).toEqual(['orphan', null])
  })

  it('a member absent from the roster (killed or released) has no seat, though its tab is still open', () => {
    const t = team('t', sess('L', 'lead-tm'), [mem('A', 1, 'a-tm')])
    const rest = { tabs: [tab('tl', leaf('h1', 'lead-tm')), tab('ta', leaf('h1', 'a-tm'))], workspaces: [ws('w1', ['tl', 'ta'])] }
    const before = selectTeamViews(input({ rosterByHost: { h1: [t] }, ...rest }))
    expect(teamOfTab(before, 'ta')?.role).toBe('member')
    const after = selectTeamViews(input({ rosterByHost: { h1: [{ ...t, members: [] }] }, ...rest }))
    expect(after[0].members).toEqual([])
    expect(teamOfTab(after, 'ta')).toBeNull()
  })
})

describe('teamOfTab', () => {
  const lead = sess('L', 'lead-tm')
  const t1 = team('t1', lead, [mem('A', 1, 'a-tm')])
  const t2 = team('t2', sess('L2', 'lead2-tm'), [mem('B', 1, 'b-tm')])

  it('finds the team and role a tab is drawn as: lead, member, and none for a plain tab', () => {
    const views = selectTeamViews(input({
      rosterByHost: { h1: [t1, t2] },
      tabs: [tab('tl', leaf('h1', 'lead-tm')), tab('ta', leaf('h1', 'a-tm')), tab('plain', leaf('h1', 'zzz'))],
      workspaces: [ws('w1', ['tl', 'ta', 'plain'])],
    }))
    expect(teamOfTab(views, 'tl')).toMatchObject({ key: teamKeyOf('h1', 't1'), role: 'lead' })
    expect(teamOfTab(views, 'tl')?.seat.session.session_id).toBe('L')
    expect(teamOfTab(views, 'ta')).toMatchObject({ key: teamKeyOf('h1', 't1'), role: 'member' })
    expect(teamOfTab(views, 'plain')).toBeNull()
    expect(teamOfTab(views, 'no-such-tab')).toBeNull()
  })

  it('a tab showing panes of two teams belongs to the primary pane\'s team, else the first pane in layout order', () => {
    const views = (layout: PaneLayout) => selectTeamViews(input({
      rosterByHost: { h1: [t1, t2] },
      tabs: [tab('both', layout)],
      workspaces: [ws('w1', ['both'])],
    }))
    // the primary pane (the first leaf) shows team 2's member
    expect(teamOfTab(views(split(leaf('h1', 'b-tm'), leaf('h1', 'a-tm'))), 'both')?.key).toBe(teamKeyOf('h1', 't2'))
    // swap them: team 1 wins
    expect(teamOfTab(views(split(leaf('h1', 'a-tm'), leaf('h1', 'b-tm'))), 'both')?.key).toBe(teamKeyOf('h1', 't1'))
    // the primary pane shows no team: the first matching pane in layout pre-order (nested) wins
    expect(teamOfTab(views(split(leaf('h1', 'plain'), split(leaf('h1', 'b-tm')), leaf('h1', 'a-tm'))), 'both')?.key).toBe(teamKeyOf('h1', 't2'))
  })

  it('a tab that shows a seat which another tab was chosen for is not drawn as that seat', () => {
    const views = selectTeamViews(input({
      rosterByHost: { h1: [t1] },
      tabs: [tab('first', leaf('h1', 'a-tm')), tab('second', leaf('h1', 'a-tm'))],
      workspaces: [ws('w1', ['first', 'second'])],
    }))
    expect(teamOfTab(views, 'first')?.role).toBe('member')
    expect(teamOfTab(views, 'second')).toBeNull()
  })
})
