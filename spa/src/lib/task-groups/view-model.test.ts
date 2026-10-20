// spa/src/lib/task-groups/view-model.test.ts — what the sidebar's task-group view lists (task groups spec §6.1, TG-2b; name pool v2 spec §7, L10): the team groups first (a team with its members), then the manual groups
// of `useManualGroupsStore` in its `order`, then 「未分組」 last. A session in a live team shows under its team only; a manual group holds the solo sessions — of ANY host — whose (host, lineage root) it lists; a root not
// known yet is 「未分組」 for the display. Pure: the sidebar feeds it from the stores.
import { describe, it, expect } from 'vitest'
import { buildGroupView, type SessionTab } from './view-model'
import type { ManualGroup, ManualGroups } from '../../stores/useManualGroupsStore'
import type { TeamView, Seat } from '../team/team-views'

const tab = (tabId: string, hostId: string, agentSessionId: string | null, over: Partial<SessionTab> = {}): SessionTab => ({ tabId, hostId, sessionCode: `code-${tabId}`, agentSessionId, workspaceId: 'w1', ...over })
const group = (name: string, members: Array<[string, string]>, over: Partial<ManualGroup> = {}): ManualGroup => ({ name, members: members.map(([host, root]) => ({ host, root })), createdAt: 1, ...over })
const manual = (groups: Record<string, ManualGroup>, order: string[] = Object.keys(groups)): ManualGroups => ({ order, groups })
const seat = (role: 'lead' | 'member', id: string, tabId: string | null, over: Partial<Seat> = {}): Seat => ({
  role, session: { session_id: id, ref: `_${id}`, address: `mlab/${id}`, live: true }, state: 'active', origin: role === 'lead' ? null : 'spawned', joinedAt: 1, label: id,
  hostId: 'h1', hostAlias: '', remote: false, tabId, workspaceId: 'w1', paneIndex: 0, groupTabId: tabId, groupPaneIndex: 0, ...over,
})
const team = (teamId: string, lead: Seat, members: Seat[] = [], over: Partial<TeamView> = {}): TeamView => ({ key: `h1\u0000${teamId}`, hostId: 'h1', teamId, createdAt: 1, name: `Team ${teamId}`, label: '', colorIndex: 0, lead, members, ...over })
const noRoot = () => null
const none = manual({})

describe('buildGroupView', () => {
  it('lists team groups, then manual groups, then 未分組, in that order', () => {
    const v = buildGroupView({
      tabs: [tab('t1', 'h1', 'L'), tab('t2', 'h1', 'M'), tab('t3', 'h1', 'S3'), tab('t4', 'h1', 'S4')],
      teams: [team('a', seat('lead', 'L', 't1'), [seat('member', 'M', 't2')])],
      manual: manual({ g1: group('G1', [['h1', 'S3']]) }),
      rootOf: (_h, s) => s,
    })
    expect(v.map((s) => s.kind)).toEqual(['team', 'manual', 'ungrouped'])
    const [t, m, u] = v
    expect(t.kind === 'team' && [t.lead.tabId, ...t.members.map((r) => r.tabId)]).toEqual(['t1', 't2'])
    expect(m.kind === 'manual' && m.rows.map((r) => r.tabId)).toEqual(['t3'])
    expect(u.kind === 'ungrouped' && u.rows.map((r) => r.tabId)).toEqual(['t4'])
  })

  it('a session in a live team is shown under its team only, whatever a manual group says (L10)', () => {
    const v = buildGroupView({
      tabs: [tab('t1', 'h1', 'L'), tab('t2', 'h1', 'M')],
      teams: [team('a', seat('lead', 'L', 't1'), [seat('member', 'M', 't2')])],
      manual: manual({ g1: group('G1', [['h1', 'L'], ['h1', 'M']]) }),
      rootOf: (_h, s) => s,
    })
    const m = v.find((s) => s.kind === 'manual')
    expect(m && m.kind === 'manual' && m.rows).toEqual([]) // the group stays, with nothing live in it
    expect(v.find((s) => s.kind === 'ungrouped')).toBeUndefined()
  })

  it('the lineage root decides: a relayed session (its own id is not in the group) belongs where its root does', () => {
    const roots: Record<string, string> = { S3b: 'S3' } // S3b is the session a relay made out of S3
    const v = buildGroupView({ tabs: [tab('t3', 'h1', 'S3b')], teams: [], manual: manual({ g1: group('G1', [['h1', 'S3']]) }), rootOf: (_h, s) => roots[s] ?? null })
    expect(v[0].kind === 'manual' && v[0].rows.map((r) => r.tabId)).toEqual(['t3'])
  })

  it('a root not known yet is 未分組 for the display; so is a session the pane has no agent session id for', () => {
    const v = buildGroupView({ tabs: [tab('t3', 'h1', 'S3b'), tab('t4', 'h1', null)], teams: [], manual: manual({ g1: group('G1', [['h1', 'S3']]) }), rootOf: noRoot })
    const u = v.find((s) => s.kind === 'ungrouped')
    expect(u && u.kind === 'ungrouped' && u.rows.map((r) => r.tabId)).toEqual(['t3', 't4'])
  })

  it('a manual group gathers sessions of ANY host: a member is a (host, root), and the same root on another host is another session', () => {
    const v = buildGroupView({
      tabs: [tab('t1', 'h1', 'S1'), tab('t2', 'h2', 'S2'), tab('t3', 'h3', 'S1')], teams: [],
      manual: manual({ g1: group('G1', [['h1', 'S1'], ['h2', 'S2']]) }), rootOf: (_h, s) => s,
    })
    expect(v.find((s) => s.kind === 'manual')).toMatchObject({ groupId: 'g1', rows: [{ tabId: 't1' }, { tabId: 't2' }] })
    expect(v.find((s) => s.kind === 'ungrouped')).toMatchObject({ rows: [{ tabId: 't3' }] })
  })

  it('manual groups come in the store\'s order, not by name or time; an id in `order` the store lacks is skipped', () => {
    const v = buildGroupView({
      tabs: [], teams: [], rootOf: noRoot,
      manual: manual({ a: group('A', [], { createdAt: 2 }), b: group('B', [], { createdAt: 5 }), c: group('C', [], { createdAt: 9 }) }, ['c', 'ghost', 'a', 'b']),
    })
    expect(v.filter((s) => s.kind === 'manual').map((s) => s.kind === 'manual' && s.groupId)).toEqual(['c', 'a', 'b'])
  })

  it('a session two groups name (two devices filed it differently) is shown once, in the first of them', () => {
    const v = buildGroupView({
      tabs: [tab('t1', 'h1', 'S1')], teams: [],
      manual: manual({ a: group('A', [['h1', 'S1']]), b: group('B', [['h1', 'S1']]) }, ['b', 'a']), rootOf: (_h, s) => s,
    })
    const sections = v.filter((s) => s.kind === 'manual')
    expect(sections.map((s) => s.kind === 'manual' && [s.groupId, s.rows.length])).toEqual([['b', 1], ['a', 0]])
  })

  it('a group with nothing live in it is listed empty (the view collapses it); 未分組 is left out when it has nothing', () => {
    const v = buildGroupView({ tabs: [], teams: [], manual: manual({ g1: group('G1', [['h1', 'gone']]) }), rootOf: noRoot })
    expect(v).toHaveLength(1)
    expect(v[0].kind === 'manual' && v[0].rows).toEqual([])
  })

  it('there is no per-host block and no unsafe row: with no groups only the sessions are listed', () => {
    const v = buildGroupView({ tabs: [tab('t1', 'h1', 'S1')], teams: [], manual: none, rootOf: noRoot })
    expect(v.map((s) => s.kind)).toEqual(['ungrouped'])
  })

  it('a team seat with no open tab is still listed, not openable; a tab not showing a tmux session is not here at all', () => {
    const v = buildGroupView({ tabs: [tab('t1', 'h1', 'L')], teams: [team('a', seat('lead', 'L', 't1'), [seat('member', 'M', null)])], manual: none, rootOf: noRoot })
    const t = v[0]
    expect(t.kind === 'team' && t.members.map((r) => [r.seatId, r.tabId])).toEqual([['M', null]])
  })

  describe('a session two tabs show (team first)', () => {
    const named = (tabId: string, agentSessionId: string | null, sessionName: string) => tab(tabId, 'h1', agentSessionId, { sessionName })
    const named_seat = (role: 'lead' | 'member', id: string, tabId: string | null) => seat(role, id, tabId, { session: { session_id: id, ref: `_${id}`, address: `mlab/${id}`, live: true, tmux_session: `tm-${id}` } as Seat['session'] })

    it('the extra tab showing a seat session is in no group and not under ungrouped', () => {
      const v = buildGroupView({
        tabs: [named('t1', 'M', 'tm-M'), named('t2', 'M', 'tm-M')],
        teams: [team('a', named_seat('lead', 'L', null), [named_seat('member', 'M', 't1')])],
        manual: none, rootOf: (_h, s) => s,
      })
      expect(v.map((s) => s.kind)).toEqual(['team'])
    })

    it('a manual group listing the session root does not pick up the extra tab', () => {
      const v = buildGroupView({
        tabs: [named('t1', 'M', 'tm-M'), named('t2', 'M', 'tm-M')],
        teams: [team('a', named_seat('lead', 'L', null), [named_seat('member', 'M', 't1')])],
        manual: manual({ g1: group('G1', [['h1', 'M']]) }), rootOf: (_h, s) => s,
      })
      const m = v.find((s) => s.kind === 'manual')
      expect(m && m.kind === 'manual' && m.rows).toEqual([])
      expect(v.find((s) => s.kind === 'ungrouped')).toBeUndefined()
    })

    it('whichever of the two tabs the seat was drawn from, ungrouped is the same and holds neither', () => {
      const run = (chosen: 't1' | 't2') => buildGroupView({
        tabs: [named('t1', 'M', 'tm-M'), named('t0', 'S', 'tm-S'), named('t2', 'M', 'tm-M')],
        teams: [team('a', named_seat('lead', 'L', null), [named_seat('member', 'M', chosen)])],
        manual: none, rootOf: noRoot,
      }).filter((x) => x.kind === 'ungrouped').flatMap((x) => (x.kind === 'ungrouped' ? x.rows.map((r) => r.tabId) : []))
      expect(run('t1')).toEqual(['t0'])
      expect(run('t2')).toEqual(['t0'])
    })

    it('a session in no team keeps both tabs', () => {
      const v = buildGroupView({
        tabs: [named('t1', 'S', 'tm-S'), named('t2', 'S', 'tm-S')],
        teams: [team('a', named_seat('lead', 'L', null), [named_seat('member', 'M', null)])],
        manual: none, rootOf: noRoot,
      })
      const u = v.find((s) => s.kind === 'ungrouped')
      expect(u && u.kind === 'ungrouped' && u.rows.map((r) => r.tabId)).toEqual(['t1', 't2'])
    })
  })
})
