// spa/src/lib/task-groups/bar-drag.test.ts — a drag in the group bar (TG-4c): the sidebar's decision, with the side from the bar's order.
import { describe, it, expect } from 'vitest'
import { barLockedTabs, decideBarDrop } from './bar-drag'
import { manualGroupKey, teamGroupKey, UNGROUPED } from './group-key'
import type { GroupOrderCtx, OrderedTab } from './group-order'
import type { Pane, PaneLayout } from '../../types/tab'
import type { Seat, SeatHit, TeamView } from '../team/team-views'

const layout: PaneLayout = { type: 'leaf', pane: { id: 'p', content: { kind: 'new-tab' } } as Pane }
const t = (id: string, over: Partial<OrderedTab> = {}): OrderedTab => ({ id, layout, createdAt: 1, pinned: false, ...over })
const seat = (role: 'lead' | 'member', id: string, tabId: string | null): Seat => ({ role, session: { session_id: id }, tabId } as unknown as Seat)
const TK = 'h1\u0000a'
const TKEY = teamGroupKey(TK)
const L = seat('lead', 'L', 'lt'), M1 = seat('member', 'M1', 'm1'), M2 = seat('member', 'M2', 'm2')

function ctxOf(tabs: OrderedTab[], seats: Record<string, Seat> = {}): GroupOrderCtx {
  const view = { key: TK, lead: L, members: [M1, M2] } as unknown as TeamView
  const seatByTabId = new Map(Object.entries(seats).map(([id, s]) => [id, { key: TK, role: s.role, seat: s } as SeatHit]))
  return { tabs: Object.fromEntries(tabs.map((x) => [x.id, x])), index: { seatByTabId, byKey: new Map([[TK, view]]) }, manual: { order: [], groups: {} }, rootOf: () => null }
}

describe('barLockedTabs', () => {
  it('a team\'s lead tab is locked, nothing else; a manual group or 「未分組」 locks nothing', () => {
    const c = ctxOf([t('lt'), t('m1'), t('m2'), t('plain', { group: TKEY })], { lt: L, m1: M1, m2: M2 })
    expect([...barLockedTabs(TKEY, c)]).toEqual(['lt'])
    expect(barLockedTabs(UNGROUPED, c).size).toBe(0)
    expect(barLockedTabs(manualGroupKey('aaaaaaaa'), c).size).toBe(0)
  })
})

describe('decideBarDrop', () => {
  const team = () => ctxOf([t('lt'), t('m1'), t('m1b'), t('m2'), t('plain', { group: TKEY, createdAt: 1 }), t('plain2', { group: TKEY, createdAt: 2 })], { lt: L, m1: M1, m1b: M1, m2: M2 })

  it('dragging a seat\'s tab right over the next seat moves the seat (all its tabs): the member order', () => {
    // the bar: lt, m1, m1b, m2, plain, plain2
    expect(decideBarDrop(TKEY, 'm1', 'm2', team())).toEqual({ kind: 'members', teamKey: TK, order: ['M2', 'M1'] })
    expect(decideBarDrop(TKEY, 'm1b', 'm2', team())).toEqual({ kind: 'members', teamKey: TK, order: ['M2', 'M1'] })
  })

  it('and back: a seat dragged left over the previous seat goes before it', () => {
    const c = team()
    c.index.byKey.get(TK)!.members.reverse() // member order now M2, M1 → the bar lists m2 first
    expect(decideBarDrop(TKEY, 'm1', 'm2', c)).toEqual({ kind: 'members', teamKey: TK, order: ['M1', 'M2'] })
  })

  it('the lead never moves, nothing is dropped before it', () => {
    expect(decideBarDrop(TKEY, 'lt', 'm2', team())).toEqual({ kind: 'ignore' })
    expect(decideBarDrop(TKEY, 'm2', 'lt', team())).toEqual({ kind: 'members', teamKey: TK, order: ['M2', 'M1'] }) // right after the lead: the first seat
  })

  it('plain tabs reorder by rank among themselves, and do not cross into the seats', () => {
    const d = decideBarDrop(TKEY, 'plain2', 'plain', team())
    expect(d.kind).toBe('rank')
    expect(decideBarDrop(TKEY, 'plain', 'm1', team())).toEqual({ kind: 'ignore' })
  })

  it('a manual group or 「未分組」: rank, the pinned boundary not crossed', () => {
    const c = ctxOf([t('p', { pinned: true }), t('a', { createdAt: 1 }), t('b', { createdAt: 2 })])
    expect(decideBarDrop(UNGROUPED, 'b', 'a', c).kind).toBe('rank')
    expect(decideBarDrop(UNGROUPED, 'b', 'p', c)).toEqual({ kind: 'ignore' })
  })

  it('a tab not in the group, itself: nothing', () => {
    expect(decideBarDrop(UNGROUPED, 'x', 'a', ctxOf([t('a')]))).toEqual({ kind: 'ignore' })
    expect(decideBarDrop(UNGROUPED, 'a', 'a', ctxOf([t('a')]))).toEqual({ kind: 'ignore' })
  })
})
