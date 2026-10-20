// spa/src/lib/task-groups/reorder.test.ts — what a drag of a tab onto another tab inside one group writes (task groups spec §4.2, §6; TG-3e).
import { describe, it, expect } from 'vitest'
import { decideGroupDrop, rankZoneMove, type DropSide, type RankWrite } from './reorder'
import { compareGroupTabs } from './rank'
import { teamGroupKey, UNGROUPED, manualGroupKey } from './group-key'
import type { GroupOrderCtx, OrderedTab } from './group-order'
import type { Pane, PaneLayout } from '../../types/tab'
import type { Seat, SeatHit, TeamView } from '../team/team-views'

const layout: PaneLayout = { type: 'leaf', pane: { id: 'p', content: { kind: 'new-tab' } } as Pane }
const t = (id: string, over: Partial<OrderedTab> = {}): OrderedTab => ({ id, layout, createdAt: 1, pinned: false, ...over })

/** The zone's ids in the order the bar would show them after `writes` are applied. */
function shown(zone: OrderedTab[], writes: RankWrite[]): string[] {
  const rank = new Map(writes.map((w) => [w.tabId, w.rank]))
  return zone
    .map((x) => ({ ...x, groupRank: rank.get(x.id) ?? x.groupRank }))
    .sort(compareGroupTabs)
    .map((x) => x.id)
}

describe('rankZoneMove', () => {
  it('a drop among unranked tabs also ranks the unranked tabs before the drop point, the rest stay behind', () => {
    const zone = [t('a', { createdAt: 1 }), t('b', { createdAt: 2 }), t('c', { createdAt: 3 }), t('d', { createdAt: 4 }), t('e', { createdAt: 5 })]
    const w = rankZoneMove(zone, 'd', 'c', 'before')!
    expect(w.map((x) => x.tabId).sort()).toEqual(['a', 'b', 'd'])
    expect(shown(zone, w)).toEqual(['a', 'b', 'd', 'c', 'e'])
  })

  it('inside the ranked prefix only the dragged tab is written, between its new neighbours', () => {
    const zone = [t('a', { groupRank: 'F' }), t('b', { groupRank: 'M' }), t('c', { groupRank: 'T' })]
    const w = rankZoneMove(zone, 'c', 'b', 'before')!
    expect(w).toHaveLength(1)
    expect(w[0].tabId).toBe('c')
    expect(shown(zone, w)).toEqual(['a', 'c', 'b'])
  })

  it('a ranked tab dropped after the last ranked tab, before unranked ones, keeps the unranked behind', () => {
    const zone = [t('a', { groupRank: 'F' }), t('b', { groupRank: 'M' }), t('u', { createdAt: 1 }), t('v', { createdAt: 2 })]
    const w = rankZoneMove(zone, 'a', 'b', 'after')!
    expect(w).toHaveLength(1)
    expect(shown(zone, w)).toEqual(['b', 'a', 'u', 'v'])
  })

  it('the first tab dropped to the front gets a rank before the old first', () => {
    const zone = [t('a', { groupRank: 'F' }), t('b', { groupRank: 'M' })]
    const w = rankZoneMove(zone, 'b', 'a', 'before')!
    expect(shown(zone, w)).toEqual(['b', 'a'])
  })

  it('an order that does not change is null', () => {
    const zone = [t('a', { createdAt: 1 }), t('b', { createdAt: 2 }), t('c', { createdAt: 3 })]
    expect(rankZoneMove(zone, 'b', 'a', 'after')).toBeNull()
    expect(rankZoneMove(zone, 'b', 'c', 'before')).toBeNull()
    expect(rankZoneMove(zone, 'a', 'a', 'before')).toBeNull()
  })

  it('an id outside the zone is null', () => {
    const zone = [t('a'), t('b')]
    expect(rankZoneMove(zone, 'x', 'a', 'before')).toBeNull()
    expect(rankZoneMove(zone, 'a', 'x', 'before')).toBeNull()
  })

  it('two neighbours with equal ranks (two workspaces inserting at one spot): the whole zone is re-ranked in the new order', () => {
    const zone = [t('a', { groupRank: 'V' }), t('b', { groupRank: 'V' }), t('c', { groupRank: 'Z' })]
    const w = rankZoneMove(zone, 'c', 'b', 'before')!
    expect(w.map((x) => x.tabId).sort()).toEqual(['a', 'b', 'c'])
    expect(shown(zone, w)).toEqual(['a', 'c', 'b'])
  })

  it('a rank that cannot grow past 32 characters: the whole zone is re-ranked in the new order', () => {
    const long = 'z'.repeat(32)
    const zone = [t('b', { groupRank: 'A' }), t('a', { groupRank: long }), t('c', { createdAt: 1 })]
    const w = rankZoneMove(zone, 'b', 'a', 'after')!
    expect(w.map((x) => x.tabId).sort()).toEqual(['a', 'b', 'c'])
    expect(shown(zone, w)).toEqual(['a', 'b', 'c'])
    expect(w.every((x) => x.rank.length < 32)).toBe(true)
  })
})

describe('decideGroupDrop', () => {
  const seat = (role: 'lead' | 'member', id: string, tabId: string | null): Seat => ({ role, session: { session_id: id }, tabId } as unknown as Seat)
  const TK = 'h1\u0000a'
  const TKEY = teamGroupKey(TK)
  const L = seat('lead', 'L', 'lt')
  const M1 = seat('member', 'M1', 'm1a')
  const M2 = seat('member', 'M2', 'm2')
  const M3 = seat('member', 'M3', null) // a seat without a tab

  function ctxOf(tabs: OrderedTab[], seats: Record<string, Seat>, members: Seat[] = [M1, M2, M3]): GroupOrderCtx {
    const view = { key: TK, lead: L, members } as unknown as TeamView
    const seatByTabId = new Map(Object.entries(seats).map(([id, s]) => [id, { key: TK, role: s.role, seat: s } as SeatHit]))
    return {
      tabs: Object.fromEntries(tabs.map((x) => [x.id, x])),
      index: { seatByTabId, byKey: new Map([[TK, view]]) },
      manual: { order: [], groups: {} },
      rootOf: () => null,
    }
  }
  const drop = (groupKey: string, activeId: string, overId: string, side: DropSide, ctx: GroupOrderCtx) => decideGroupDrop({ groupKey, activeId, overId, side, ctx })

  describe('a manual group or 「未分組」', () => {
    const plainCtx = (tabs: OrderedTab[], manual: GroupOrderCtx['manual'] = { order: [], groups: {} }): GroupOrderCtx => ({
      tabs: Object.fromEntries(tabs.map((x) => [x.id, x])),
      index: { seatByTabId: new Map(), byKey: new Map() },
      manual,
      rootOf: () => null,
    })

    it('moves a tab inside the others zone by rank', () => {
      const c = plainCtx([t('x', { createdAt: 1 }), t('y', { createdAt: 2 }), t('z', { createdAt: 3 })])
      const d = drop(UNGROUPED, 'z', 'y', 'before', c)
      expect(d.kind).toBe('rank')
    })

    it('moves a pinned tab among the pinned ones', () => {
      const c = plainCtx([t('p1', { pinned: true, createdAt: 1 }), t('p2', { pinned: true, createdAt: 2 }), t('x')])
      const d = drop(UNGROUPED, 'p2', 'p1', 'before', c)
      expect(d.kind).toBe('rank')
      if (d.kind === 'rank') expect(d.writes.every((w) => w.tabId !== 'x')).toBe(true)
    })

    it('does not cross the pinned boundary, either way', () => {
      const c = plainCtx([t('p', { pinned: true }), t('x', { createdAt: 1 }), t('y', { createdAt: 2 })])
      expect(drop(UNGROUPED, 'x', 'p', 'before', c)).toEqual({ kind: 'ignore' })
      expect(drop(UNGROUPED, 'p', 'x', 'after', c)).toEqual({ kind: 'ignore' })
    })

    it('a manual group works the same, and an unchanged order is ignored', () => {
      const key = manualGroupKey('aaaaaaaa')
      const c = plainCtx([t('a', { group: key, createdAt: 1 }), t('b', { group: key, createdAt: 2 })], { order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 } } })
      expect(drop(key, 'b', 'a', 'before', c).kind).toBe('rank')
      expect(drop(key, 'a', 'b', 'before', c)).toEqual({ kind: 'ignore' })
    })

    it('a tab that is not in the group, or the same tab, is ignored', () => {
      const c = plainCtx([t('a'), t('b')])
      expect(drop(UNGROUPED, 'nope', 'a', 'before', c)).toEqual({ kind: 'ignore' })
      expect(drop(UNGROUPED, 'a', 'a', 'after', c)).toEqual({ kind: 'ignore' })
    })
  })

  describe('a team', () => {
    const tabs = (): OrderedTab[] => [
      t('lt'), t('m1a'), t('m1b'), t('m2'),
      t('pin', { pinned: true, group: TKEY }), t('pin2', { pinned: true, group: TKEY, createdAt: 2 }),
      t('plain1', { group: TKEY, createdAt: 1 }), t('plain2', { group: TKEY, createdAt: 2 }),
    ]
    const seats = { lt: L, m1a: M1, m1b: M1, m2: M2 }

    it('the lead never moves', () => {
      expect(drop(TKEY, 'lt', 'm1a', 'after', ctxOf(tabs(), seats))).toEqual({ kind: 'ignore' })
    })

    it('nothing is dropped before the lead; after the lead puts the seat first', () => {
      const c = ctxOf(tabs(), seats)
      expect(drop(TKEY, 'm2', 'lt', 'before', c)).toEqual({ kind: 'ignore' })
      expect(drop(TKEY, 'm2', 'lt', 'after', c)).toEqual({ kind: 'members', teamKey: TK, order: ['M2', 'M1', 'M3'] })
    })

    it('a seat moves as a unit and writes the whole member order, a seat without a tab included', () => {
      const c = ctxOf(tabs(), seats)
      expect(drop(TKEY, 'm1a', 'm2', 'after', c)).toEqual({ kind: 'members', teamKey: TK, order: ['M2', 'M1', 'M3'] })
      expect(drop(TKEY, 'm2', 'm1b', 'before', c)).toEqual({ kind: 'members', teamKey: TK, order: ['M2', 'M1', 'M3'] })
    })

    it('a seat with two tabs moves with both: dragging either tab is the same decision', () => {
      const c = ctxOf(tabs(), seats)
      expect(drop(TKEY, 'm1b', 'm2', 'after', c)).toEqual(drop(TKEY, 'm1a', 'm2', 'after', c))
    })

    it('an unchanged member order, or a drop on its own seat, is ignored', () => {
      const c = ctxOf(tabs(), seats)
      expect(drop(TKEY, 'm1a', 'm2', 'before', c)).toEqual({ kind: 'ignore' })
      expect(drop(TKEY, 'm1a', 'm1b', 'after', c)).toEqual({ kind: 'ignore' })
    })

    it('pinned tabs and plain tabs move by rank inside their own zone', () => {
      const c = ctxOf(tabs(), seats)
      expect(drop(TKEY, 'pin2', 'pin', 'before', c).kind).toBe('rank')
      expect(drop(TKEY, 'plain2', 'plain1', 'before', c).kind).toBe('rank')
    })

    it('nothing crosses a zone', () => {
      const c = ctxOf(tabs(), seats)
      expect(drop(TKEY, 'plain1', 'm1a', 'before', c)).toEqual({ kind: 'ignore' })
      expect(drop(TKEY, 'pin', 'plain1', 'after', c)).toEqual({ kind: 'ignore' })
      expect(drop(TKEY, 'pin', 'm2', 'before', c)).toEqual({ kind: 'ignore' })
      expect(drop(TKEY, 'm1a', 'plain1', 'after', c)).toEqual({ kind: 'ignore' })
      expect(drop(TKEY, 'm2', 'pin', 'after', c)).toEqual({ kind: 'ignore' })
    })
  })
})
