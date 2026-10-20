// spa/src/lib/task-groups/group-order.test.ts — the tabs of one group in bar order (task groups spec §4.2, §5).
import { describe, it, expect } from 'vitest'
import { allGroupTabs, groupTabs, type GroupOrderCtx, type OrderedTab } from './group-order'
import { manualGroupKey, teamGroupKey, UNGROUPED } from './group-key'
import type { Pane, PaneLayout } from '../../types/tab'
import type { Seat, SeatHit, TeamView } from '../team/team-views'

const layout: PaneLayout = { type: 'leaf', pane: { id: 'p', content: { kind: 'new-tab' } } as Pane }
const t = (id: string, over: Partial<OrderedTab> = {}): OrderedTab => ({ id, layout, createdAt: 1, pinned: false, ...over })
const seat = (role: 'lead' | 'member', id: string, tabId: string | null): Seat => ({ role, session: { session_id: id }, tabId } as unknown as Seat)
const TK = 'h1\u0000a'
const TKEY = teamGroupKey(TK)

function ctxOf(tabs: OrderedTab[], opts: { lead?: Seat; members?: Seat[]; seats?: Record<string, Seat>; manual?: GroupOrderCtx['manual'] } = {}): GroupOrderCtx {
  const lead = opts.lead ?? seat('lead', 'L', null)
  const view = { key: TK, lead, members: opts.members ?? [] } as unknown as TeamView
  const seatByTabId = new Map(Object.entries(opts.seats ?? {}).map(([id, s]) => [id, { key: TK, role: s.role, seat: s } as SeatHit]))
  return {
    tabs: Object.fromEntries(tabs.map((x) => [x.id, x])),
    index: { seatByTabId, byKey: new Map([[TK, view]]) },
    manual: opts.manual ?? { order: [], groups: {} },
    rootOf: () => null,
  }
}

describe('groupTabs', () => {
  it('ungrouped: pinned first, then rank, then createdAt, then id', () => {
    const c = ctxOf([t('b', { createdAt: 2 }), t('a', { createdAt: 2 }), t('r', { groupRank: 'V' }), t('p', { pinned: true, createdAt: 9 }), t('o', { createdAt: 1 })])
    expect(groupTabs(UNGROUPED, c)).toEqual(['p', 'r', 'o', 'a', 'b'])
  })

  it('a team: the lead first even when pinned, then other pinned tabs, then each member in member order, then plain tabs', () => {
    const L = seat('lead', 'L', 'lt'), M1 = seat('member', 'M1', 'm1'), M2 = seat('member', 'M2', 'm2')
    const c = ctxOf(
      [t('plain', { group: TKEY }), t('m2'), t('m1'), t('lt', { pinned: true }), t('pm', { pinned: true, groupRank: 'V' })],
      { lead: L, members: [M1, M2], seats: { lt: L, m1: M1, m2: M2, pm: M1 }, },
    )
    expect(groupTabs(TKEY, c)).toEqual(['lt', 'pm', 'm1', 'm2', 'plain'])
  })

  it('a seat with two tabs keeps them together, the chosen one first', () => {
    const M1 = seat('member', 'M1', 'b'), M2 = seat('member', 'M2', 'c')
    const c = ctxOf([t('a', { groupRank: 'A' }), t('b', { groupRank: 'Z' }), t('c')], { members: [M1, M2], seats: { a: M1, b: M1, c: M2 } })
    expect(groupTabs(TKEY, c)).toEqual(['b', 'a', 'c'])
  })

  it('tabs with the same rank in two workspaces order by tab id whichever is read first', () => {
    const one = ctxOf([t('x2', { groupRank: 'V' }), t('x1', { groupRank: 'V' })])
    const two = ctxOf([t('x1', { groupRank: 'V' }), t('x2', { groupRank: 'V' })])
    expect(groupTabs(UNGROUPED, one)).toEqual(['x1', 'x2'])
    expect(groupTabs(UNGROUPED, two)).toEqual(['x1', 'x2'])
  })

  it('a manual group lists the tabs tab.group puts in it; a watcher tab is listed nowhere', () => {
    const key = manualGroupKey('aaaaaaaa')
    const c = ctxOf([t('a', { group: key }), t('watch-jobs'), t('z')], { manual: { order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 } } } })
    const all = allGroupTabs(c)
    expect(all.get(key)).toEqual(['a'])
    expect(all.get(UNGROUPED)).toEqual(['z'])
    expect([...all.values()].flat()).not.toContain('watch-jobs')
  })
})
