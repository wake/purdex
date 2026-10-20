// spa/src/lib/task-groups/place-last.test.ts — a tab moved into a group from the personal panel ends up last in that group's bar (TG-5c, R18).
import { beforeEach, describe, expect, it } from 'vitest'
import { placeLastInGroup } from './place-last'
import { groupTabs, type GroupOrderCtx } from './group-order'
import { useTabStore } from '../../stores/useTabStore'
import { createTab } from '../../types/tab'
import { manualGroupKey } from './group-key'

const G = manualGroupKey('aaaaaaaa')
const mk = (code: string, agent: string) => createTab({ kind: 'tmux-session', hostId: 'h', sessionCode: code, mode: 'terminal', cachedName: '', tmuxInstance: '', rebuild: { agent: { sessionId: agent } } } as never)

/** The tabs whose agent sessions A, B, C, N have the roots RA, RB, RC, RN, all members of the group G. */
function ctx(): GroupOrderCtx {
  const roots: Record<string, string> = { A: 'RA', B: 'RB', C: 'RC', N: 'RN' }
  return {
    tabs: useTabStore.getState().tabs,
    index: { seatByTabId: new Map(), byKey: new Map() },
    manual: { order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'G', members: Object.values(roots).map((root) => ({ host: 'h', root })), createdAt: 1 } } },
    rootOf: (_h, s) => roots[s] ?? null,
  }
}
const order = () => groupTabs(G, ctx())

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})

describe('placeLastInGroup', () => {
  it('puts the tab after the group\'s last tab, ranking the unranked tabs before it (the TG-3e drop decision)', () => {
    const a = mk('a', 'A'); const b = mk('b', 'B'); const c = mk('c', 'C'); const n = mk('n', 'N')
    ;[a, b, c, n].forEach((t, i) => useTabStore.getState().addTab({ ...t, createdAt: i + 1 }))
    // an OLD rank of n that would put it first in its new group
    useTabStore.getState().setTabGroupRanks([{ tabId: n.id, rank: 'a' }])
    expect(order()[0]).toBe(n.id)
    placeLastInGroup(n.id, ctx())
    expect(order()).toEqual([a.id, b.id, c.id, n.id])
  })

  it('a pinned tab goes last among the pinned ones, not past the unpinned', () => {
    const a = mk('a', 'A'); const p = { ...mk('p', 'B'), pinned: true }; const n = { ...mk('n', 'N'), pinned: true }
    ;[a, p, n].forEach((t, i) => useTabStore.getState().addTab({ ...t, createdAt: i + 1 }))
    useTabStore.getState().setTabGroupRanks([{ tabId: n.id, rank: 'a' }, { tabId: p.id, rank: 'm' }])
    expect(order()).toEqual([n.id, p.id, a.id])
    placeLastInGroup(n.id, ctx())
    expect(order()).toEqual([p.id, n.id, a.id])
  })

  it('is a no-op for a tab alone in its group, and for a tab no group lists', () => {
    const n = mk('n', 'N')
    useTabStore.getState().addTab(n)
    const before = useTabStore.getState().tabs
    placeLastInGroup(n.id, ctx())
    placeLastInGroup('ghost', ctx())
    expect(useTabStore.getState().tabs).toBe(before)
  })
})
