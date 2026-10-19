// spa/src/lib/task-groups/view-model.test.ts — what the sidebar's task-group view lists (name pool v2 spec §7, L9, L10; NA-6b): the team groups first (a team with its members), then the manual groups of each host in
// creation order, then 「未分組」 last. A session in a live team shows under its team only; a manual group holds the solo sessions of its own host whose lineage root it lists; a root not known yet is 「未分組」 for the
// display. Pure: the sidebar feeds it from the stores.
import { describe, it, expect } from 'vitest'
import { buildGroupView, type SessionTab } from './view-model'
import type { TaskGroup } from './api'
import type { TeamView, Seat } from '../team/team-views'

const tab = (tabId: string, hostId: string, agentSessionId: string | null, over: Partial<SessionTab> = {}): SessionTab => ({ tabId, hostId, sessionCode: `code-${tabId}`, agentSessionId, workspaceId: 'w1', ...over })
const group = (id: string, roots: string[], created_at = 1, over: Partial<TaskGroup> = {}): TaskGroup => ({ id, name: `G-${id}`, roots, created_at, updated_at: created_at, ...over })
const seat = (role: 'lead' | 'member', id: string, tabId: string | null, over: Partial<Seat> = {}): Seat => ({
  role, session: { session_id: id, ref: `_${id}`, address: `mlab/${id}`, live: true }, state: 'active', origin: role === 'lead' ? null : 'spawned', joinedAt: 1, label: id,
  hostId: 'h1', hostAlias: '', remote: false, tabId, workspaceId: 'w1', paneIndex: 0, groupTabId: tabId, groupPaneIndex: 0, ...over,
})
const team = (teamId: string, lead: Seat, members: Seat[] = [], over: Partial<TeamView> = {}): TeamView => ({ key: `h1\u0000${teamId}`, hostId: 'h1', teamId, createdAt: 1, name: `Team ${teamId}`, label: '', colorIndex: 0, lead, members, ...over })
const noRoot = () => null

describe('buildGroupView', () => {
  it('lists team groups, then manual groups, then 未分組, in that order', () => {
    const v = buildGroupView({
      tabs: [tab('t1', 'h1', 'L'), tab('t2', 'h1', 'M'), tab('t3', 'h1', 'S3'), tab('t4', 'h1', 'S4')],
      teams: [team('a', seat('lead', 'L', 't1'), [seat('member', 'M', 't2')])],
      manual: { h1: { groups: [group('g1', ['S3'])], unsafe: null } },
      rootOf: (_h, s) => s,
      hostOrder: ['h1'],
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
      manual: { h1: { groups: [group('g1', ['L', 'M'])], unsafe: null } },
      rootOf: (_h, s) => s,
      hostOrder: ['h1'],
    })
    const manual = v.find((s) => s.kind === 'manual')
    expect(manual && manual.kind === 'manual' && manual.rows).toEqual([]) // the group stays, with nothing live in it
    expect(v.find((s) => s.kind === 'ungrouped')).toBeUndefined()
  })

  it('the lineage root decides: a relayed session (its own id is not in the group) belongs where its root does', () => {
    const roots: Record<string, string> = { S3b: 'S3' } // S3b is the session a relay made out of S3
    const v = buildGroupView({
      tabs: [tab('t3', 'h1', 'S3b')], teams: [], manual: { h1: { groups: [group('g1', ['S3'])], unsafe: null } },
      rootOf: (_h, s) => roots[s] ?? null, hostOrder: ['h1'],
    })
    expect(v[0].kind === 'manual' && v[0].rows.map((r) => r.tabId)).toEqual(['t3'])
  })

  it('a root not known yet is 未分組 for the display; so is a session the pane has no agent session id for', () => {
    const v = buildGroupView({
      tabs: [tab('t3', 'h1', 'S3b'), tab('t4', 'h1', null)], teams: [], manual: { h1: { groups: [group('g1', ['S3'])], unsafe: null } },
      rootOf: noRoot, hostOrder: ['h1'],
    })
    const u = v.find((s) => s.kind === 'ungrouped')
    expect(u && u.kind === 'ungrouped' && u.rows.map((r) => r.tabId)).toEqual(['t3', 't4'])
  })

  it('a manual group holds only its own host\'s sessions (L9)', () => {
    const v = buildGroupView({
      tabs: [tab('t1', 'h1', 'S1'), tab('t2', 'h2', 'S1')], teams: [], manual: { h1: { groups: [group('g1', ['S1'])], unsafe: null } },
      rootOf: (_h, s) => s, hostOrder: ['h1', 'h2'],
    })
    expect(v.find((s) => s.kind === 'manual')).toMatchObject({ hostId: 'h1', rows: [{ tabId: 't1' }] })
    expect(v.find((s) => s.kind === 'ungrouped')).toMatchObject({ rows: [{ tabId: 't2' }] })
  })

  it('manual groups come in host order, then creation order', () => {
    const v = buildGroupView({
      tabs: [], teams: [], hostOrder: ['h2', 'h1'], rootOf: noRoot,
      manual: { h1: { groups: [group('b', [], 5), group('a', [], 2)], unsafe: null }, h2: { groups: [group('c', [], 9)], unsafe: null } },
    })
    expect(v.filter((s) => s.kind === 'manual').map((s) => s.kind === 'manual' && s.group.id)).toEqual(['c', 'a', 'b'])
  })

  it('a group with nothing live in it is listed empty (the view collapses it); 未分組 is left out when it has nothing', () => {
    const v = buildGroupView({ tabs: [], teams: [], manual: { h1: { groups: [group('g1', ['gone'])], unsafe: null } }, rootOf: noRoot, hostOrder: ['h1'] })
    expect(v).toHaveLength(1)
    expect(v[0].kind === 'manual' && v[0].rows).toEqual([])
  })

  it('a host that cannot be written to (unsafe) says so on its block, even with no groups to show', () => {
    const unsafe = { count: 2, reasons: ['root "x" is twice in the group'], invalid: false }
    const v = buildGroupView({ tabs: [], teams: [], manual: { h1: { groups: [], unsafe } }, rootOf: noRoot, hostOrder: ['h1'] })
    expect(v).toEqual([{ kind: 'unsafe', hostId: 'h1', count: 2, reasons: ['root "x" is twice in the group'], invalid: false }])
  })

  it('a team seat with no open tab is still listed, not openable; a tab not showing a tmux session is not here at all', () => {
    const v = buildGroupView({
      tabs: [tab('t1', 'h1', 'L')], teams: [team('a', seat('lead', 'L', 't1'), [seat('member', 'M', null)])],
      manual: {}, rootOf: noRoot, hostOrder: ['h1'],
    })
    const t = v[0]
    expect(t.kind === 'team' && t.members.map((r) => [r.seatId, r.tabId])).toEqual([['M', null]])
  })
})
