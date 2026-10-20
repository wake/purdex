// spa/src/lib/task-groups/view-model.test.ts — what the sidebar's task-group view lists (task groups spec §6.1, TG-2b, TG-3a-2b; name pool v2 spec §7, L10): the team groups first (a team with its members, then its other
// tabs), then the manual groups of `useManualGroupsStore` in its `order`, then 「未分組」 last. A group lists all its tabs (placement.ts decides which, group-order.ts in what order); a root not known yet is 「未分組」
// for the display. Pure: the sidebar feeds it from the stores.
import { describe, it, expect } from 'vitest'
import { buildGroupView, type GroupViewInput } from './view-model'
import { manualGroupKey, teamGroupKey } from './group-key'
import type { OrderedTab } from './group-order'
import type { ManualGroup, ManualGroups } from '../../stores/useManualGroupsStore'
import type { Pane, PaneContent, PaneLayout } from '../../types/tab'
import type { Seat, SeatHit, TeamView } from '../team/team-views'

const content = (agent: string | null, ended = false): PaneContent => agent === null
  ? { kind: 'dashboard' }
  : { kind: 'tmux-session', hostId: 'h1', sessionCode: 'c', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i',
      rebuild: { sessionName: 'n', tmuxInstance: 'i', agent: { type: 'cc', sessionId: agent, updatedAt: 1 }, ...(ended ? { agentExited: { at: 2, reason: 'session-end' as const } } : {}) } }
const tab = (id: string, agent: string | null, over: Partial<OrderedTab> & { host?: string; ended?: boolean } = {}): OrderedTab => {
  const { host, ended, ...rest } = over
  const c = content(agent, ended)
  if (host && c.kind === 'tmux-session') c.hostId = host
  const layout: PaneLayout = { type: 'leaf', pane: { id: `p-${id}`, content: c } as Pane }
  return { id, layout, createdAt: 1, pinned: false, ...rest }
}
const group = (name: string, members: Array<[string, string]>, over: Partial<ManualGroup> = {}): ManualGroup => ({ name, members: members.map(([host, root]) => ({ host, root })), createdAt: 1, ...over })
const manual = (groups: Record<string, ManualGroup>, order: string[] = Object.keys(groups)): ManualGroups => ({ order, groups })
const seat = (role: 'lead' | 'member', id: string, tabId: string | null, over: Partial<Seat> = {}): Seat => ({
  role, session: { session_id: id, ref: `_${id}`, address: `mlab/${id}`, live: true }, state: 'active', origin: role === 'lead' ? null : 'spawned', joinedAt: 1, label: id,
  hostId: 'h1', hostAlias: '', remote: false, tabId, workspaceId: 'w1', paneIndex: 0, groupTabId: tabId, groupPaneIndex: 0, ...over,
})
const team = (teamId: string, lead: Seat, members: Seat[] = []): TeamView => ({ key: `h1\u0000${teamId}`, hostId: 'h1', teamId, createdAt: 1, name: `Team ${teamId}`, label: '', colorIndex: 0, lead, members })
const noRoot = () => null
const none = manual({})

/** `seats` also names tabs that show a seat without being the seat's chosen tab: tab id → seat session id. */
function input(tabs: OrderedTab[], teams: TeamView[], over: { manual?: ManualGroups; rootOf?: GroupViewInput['rootOf']; seats?: Record<string, string> } = {}): GroupViewInput {
  const seatByTabId = new Map<string, SeatHit>()
  const byKey = new Map(teams.map((v) => [v.key, v]))
  for (const v of teams) {
    for (const s of [v.lead, ...v.members]) {
      if (s.tabId) seatByTabId.set(s.tabId, { key: v.key, role: s.role, seat: s })
      for (const [tabId, sessionId] of Object.entries(over.seats ?? {})) if (sessionId === s.session.session_id) seatByTabId.set(tabId, { key: v.key, role: s.role, seat: s })
    }
  }
  return { tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), teams, index: { seatByTabId, byKey }, manual: over.manual ?? none, rootOf: over.rootOf ?? noRoot }
}

describe('buildGroupView', () => {
  it('lists team groups, then manual groups, then 未分組, in that order', () => {
    const v = buildGroupView(input(
      [tab('t1', 'L'), tab('t2', 'M'), tab('t3', 'S3'), tab('t4', 'S4')],
      [team('a', seat('lead', 'L', 't1'), [seat('member', 'M', 't2')])],
      { manual: manual({ g1: group('G1', [['h1', 'S3']]) }), rootOf: (_h, s) => s },
    ))
    expect(v.map((s) => s.kind)).toEqual(['team', 'manual', 'ungrouped'])
    const [t, m, u] = v
    expect(t.kind === 'team' && [t.lead.tabId, ...t.members.map((r) => r.tabId)]).toEqual(['t1', 't2'])
    expect(m.kind === 'manual' && m.rows.map((r) => r.tabId)).toEqual(['t3'])
    expect(u.kind === 'ungrouped' && u.rows.map((r) => r.tabId)).toEqual(['t4'])
  })

  it('a session in a live team is shown under its team only, whatever a manual group says (L10)', () => {
    const v = buildGroupView(input(
      [tab('t1', 'L'), tab('t2', 'M')], [team('a', seat('lead', 'L', 't1'), [seat('member', 'M', 't2')])],
      { manual: manual({ g1: group('G1', [['h1', 'L'], ['h1', 'M']]) }), rootOf: (_h, s) => s },
    ))
    const m = v.find((s) => s.kind === 'manual')
    expect(m && m.kind === 'manual' && m.rows).toEqual([]) // the group stays, with nothing live in it
    expect(v.find((s) => s.kind === 'ungrouped')).toBeUndefined()
  })

  it('the lineage root decides: a relayed session (its own id is not in the group) belongs where its root does', () => {
    const roots: Record<string, string> = { S3b: 'S3' } // S3b is the session a relay made out of S3
    const v = buildGroupView(input([tab('t3', 'S3b')], [], { manual: manual({ g1: group('G1', [['h1', 'S3']]) }), rootOf: (_h, s) => roots[s] ?? null }))
    expect(v[0].kind === 'manual' && v[0].rows.map((r) => r.tabId)).toEqual(['t3'])
  })

  it('a root not known yet is 未分組 for the display; so is a session the pane has no agent session id for', () => {
    const v = buildGroupView(input([tab('t3', 'S3b'), tab('t4', null)], [], { manual: manual({ g1: group('G1', [['h1', 'S3']]) }) }))
    const u = v.find((s) => s.kind === 'ungrouped')
    expect(u && u.kind === 'ungrouped' && u.rows.map((r) => r.tabId)).toEqual(['t3', 't4'])
  })

  it('a manual group gathers sessions of ANY host: a member is a (host, root), and the same root on another host is another session', () => {
    const v = buildGroupView(input(
      [tab('t1', 'S1', { groupRank: 'a' }), tab('t2', 'S2', { host: 'h2', groupRank: 'b' }), tab('t3', 'S1', { host: 'h3' })], [],
      { manual: manual({ g1: group('G1', [['h1', 'S1'], ['h2', 'S2']]) }), rootOf: (_h, s) => s },
    ))
    expect(v.find((s) => s.kind === 'manual')).toMatchObject({ groupId: 'g1', rows: [{ tabId: 't1' }, { tabId: 't2' }] })
    expect(v.find((s) => s.kind === 'ungrouped')).toMatchObject({ rows: [{ tabId: 't3' }] })
  })

  it('manual groups come in the store\'s order, not by name or time; an id in `order` the store lacks is skipped', () => {
    const v = buildGroupView(input([], [], {
      manual: manual({ a: group('A', [], { createdAt: 2 }), b: group('B', [], { createdAt: 5 }), c: group('C', [], { createdAt: 9 }) }, ['c', 'ghost', 'a', 'b']),
    }))
    expect(v.filter((s) => s.kind === 'manual').map((s) => s.kind === 'manual' && s.groupId)).toEqual(['c', 'a', 'b'])
  })

  it('a session two groups name (two devices filed it differently) is shown once, in the first of them', () => {
    const v = buildGroupView(input([tab('t1', 'S1')], [], { manual: manual({ a: group('A', [['h1', 'S1']]), b: group('B', [['h1', 'S1']]) }, ['b', 'a']), rootOf: (_h, s) => s }))
    const sections = v.filter((s) => s.kind === 'manual')
    expect(sections.map((s) => s.kind === 'manual' && [s.groupId, s.rows.length])).toEqual([['b', 1], ['a', 0]])
  })

  it('a group with nothing in it is listed empty (the view collapses it); 未分組 is left out when it has nothing', () => {
    const v = buildGroupView(input([], [], { manual: manual({ g1: group('G1', [['h1', 'gone']]) }) }))
    expect(v).toHaveLength(1)
    expect(v[0].kind === 'manual' && v[0].rows).toEqual([])
  })

  it('with no groups only the tabs are listed, in 未分組', () => {
    expect(buildGroupView(input([tab('t1', 'S1')], [])).map((s) => s.kind)).toEqual(['ungrouped'])
  })

  it('a team seat with no open tab is still listed, not openable', () => {
    const v = buildGroupView(input([tab('t1', 'L')], [team('a', seat('lead', 'L', 't1'), [seat('member', 'M', null)])]))
    const t = v[0]
    expect(t.kind === 'team' && t.members.map((r) => [r.seatId, r.tabId])).toEqual([['M', null]])
  })

  describe('every tab is listed (TG-3a-2b)', () => {
    it('a tab that is no session is listed, under 未分組 or in the group tab.group names', () => {
      const key = manualGroupKey('aaaaaaaa')
      const v = buildGroupView(input([tab('d1', null), tab('d2', null, { group: key })], [], { manual: manual({ aaaaaaaa: group('G', []) }) }))
      expect(v.find((s) => s.kind === 'manual')).toMatchObject({ rows: [{ tabId: 'd2' }] })
      expect(v.find((s) => s.kind === 'ungrouped')).toMatchObject({ rows: [{ tabId: 'd1' }] })
    })

    it('a tab whose agent has ended stays in its group', () => {
      const v = buildGroupView(input([tab('t1', 'S1', { ended: true })], [], { manual: manual({ g1: group('G1', [['h1', 'S1']]) }), rootOf: (_h, s) => s }))
      expect(v.find((s) => s.kind === 'manual')).toMatchObject({ rows: [{ tabId: 't1' }] })
    })

    it('a second tab of a seat, and a plain tab filed under the team, are the team\'s extra rows — the seat tab first, then the plain one (group order)', () => {
      const t = team('a', seat('lead', 'L', 't1'), [seat('member', 'M', 't2')])
      const v = buildGroupView(input(
        [tab('t1', 'L'), tab('t2', 'M'), tab('t2b', 'M', { groupRank: 'b' }), tab('plain', null, { group: teamGroupKey(t.key), groupRank: 'a' })], [t],
        { seats: { t2b: 'M' } },
      ))
      const sec = v[0]
      expect(sec.kind === 'team' && sec.extra.map((r) => r.tabId)).toEqual(['t2b', 'plain'])
      expect(v.map((s) => s.kind)).toEqual(['team'])
    })

    it('a team with no extra tab has an empty extra', () => {
      const v = buildGroupView(input([tab('t1', 'L')], [team('a', seat('lead', 'L', 't1'))]))
      expect(v[0].kind === 'team' && v[0].extra).toEqual([])
    })
  })
})
