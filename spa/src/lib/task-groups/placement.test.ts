// spa/src/lib/task-groups/placement.test.ts — which group a tab is in (task groups spec §5): a team seat first, then the agent's manual membership, then the tab's own `group`, then 「未分組」.
import { describe, it, expect } from 'vitest'
import { groupOfTab, manualOwners, type PlacedTab, type PlacementCtx } from './placement'
import { manualGroupKey, teamGroupKey, UNGROUPED } from './group-key'
import type { Pane, PaneContent, PaneLayout } from '../../types/tab'
import type { SeatHit, TeamView } from '../team/team-views'

const sess = (hostId: string, agent?: string, terminated = false): PaneContent => ({
  kind: 'tmux-session', hostId, sessionCode: 'c', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i',
  ...(terminated ? { terminated: 'session-closed' as never } : {}),
  ...(agent ? { rebuild: { sessionName: 'n', tmuxInstance: 'i', agent: { type: 'cc', sessionId: agent } } } : {}),
})
const leaf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } as Pane })
const split = (a: PaneLayout, b: PaneLayout): PaneLayout => ({ type: 'split', direction: 'h', children: [a, b], sizes: [50, 50] } as PaneLayout)
const tab = (id: string, layout: PaneLayout, group?: string): PlacedTab => ({ id, layout, ...(group ? { group } : {}) })
const TK = 'h1\u0000a'
const hit = { key: TK, role: 'lead', seat: {} } as unknown as SeatHit
const ctx = (over: Partial<PlacementCtx> & { seats?: Record<string, SeatHit> } = {}): PlacementCtx => ({
  index: { seatByTabId: new Map(Object.entries(over.seats ?? {})), byKey: new Map([[TK, {} as TeamView]]) },
  manual: over.manual ?? { order: [], groups: {} },
  rootOf: over.rootOf ?? (() => null),
})
const g = (name: string, members: Array<[string, string]>) => ({ name, members: members.map(([host, root]) => ({ host, root })), createdAt: 1 })

describe('groupOfTab', () => {
  it('a watcher-area tab is in no group', () => {
    expect(groupOfTab(tab('watch-guardian', leaf('p', sess('h1'))), ctx({ seats: { 'watch-guardian': hit } }))).toBeNull()
  })

  it('a tab showing a team seat is the team’s, even over a manual membership and its own group', () => {
    const manual = { order: ['aaaaaaaa'], groups: { aaaaaaaa: g('G', [['h1', 'R']]) } }
    const t = tab('t1', leaf('p', sess('h1', 'S')), manualGroupKey('aaaaaaaa'))
    expect(groupOfTab(t, ctx({ seats: { t1: hit }, manual, rootOf: () => 'R' }))).toBe(teamGroupKey(TK))
  })

  it('a split tab whose primary pane is solo and second pane a seat is the team’s (the seat map decides)', () => {
    const layout = split(leaf('p1', sess('h1', 'S')), leaf('p2', sess('h1', 'M')))
    expect(groupOfTab(tab('t1', layout), ctx({ seats: { t1: hit } }))).toBe(teamGroupKey(TK))
  })

  it('an agent whose (host, root) is a manual member is in that group, ended or live, via the first pane that recorded an agent', () => {
    const manual = { order: ['aaaaaaaa'], groups: { aaaaaaaa: g('G', [['h1', 'R']]) } }
    const layout = split(leaf('p1', sess('h1')), leaf('p2', sess('h1', 'S', true)))
    expect(groupOfTab(tab('t1', layout), ctx({ manual, rootOf: (h, s) => (h === 'h1' && s === 'S' ? 'R' : null) }))).toBe(manualGroupKey('aaaaaaaa'))
  })

  it('a member two groups list belongs to the first in order; the same root on another host is not it', () => {
    const manual = { order: ['bbbbbbbb', 'aaaaaaaa'], groups: { aaaaaaaa: g('A', [['h1', 'R']]), bbbbbbbb: g('B', [['h1', 'R']]) } }
    expect(manualOwners(manual).get(JSON.stringify(['h1', 'R']))).toBe('bbbbbbbb')
    expect(groupOfTab(tab('t1', leaf('p', sess('h2', 'S'))), ctx({ manual, rootOf: () => 'R' }))).toBe(UNGROUPED)
  })

  it('tab.group names an existing group → that group; a missing one → 未分組', () => {
    const manual = { order: ['aaaaaaaa'], groups: { aaaaaaaa: g('G', []) } }
    expect(groupOfTab(tab('t1', leaf('p', sess('h1')), manualGroupKey('aaaaaaaa')), ctx({ manual }))).toBe(manualGroupKey('aaaaaaaa'))
    expect(groupOfTab(tab('t2', leaf('p', sess('h1')), manualGroupKey('cccccccc')), ctx({ manual }))).toBe(UNGROUPED)
    expect(groupOfTab(tab('t3', leaf('p', sess('h1')), teamGroupKey(TK)), ctx())).toBe(teamGroupKey(TK))
    expect(groupOfTab(tab('t4', leaf('p', sess('h1')), teamGroupKey('h1\u0000zz')), ctx())).toBe(UNGROUPED)
  })

  it('an agent with an unknown root falls through to tab.group, then 未分組', () => {
    const manual = { order: ['aaaaaaaa'], groups: { aaaaaaaa: g('G', [['h1', 'R']]) } }
    expect(groupOfTab(tab('t1', leaf('p', sess('h1', 'S')), manualGroupKey('aaaaaaaa')), ctx({ manual }))).toBe(manualGroupKey('aaaaaaaa'))
    expect(groupOfTab(tab('t2', leaf('p', sess('h1', 'S'))), ctx({ manual }))).toBe(UNGROUPED)
  })
})
