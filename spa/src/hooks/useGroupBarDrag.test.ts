// spa/src/hooks/useGroupBarDrag.test.ts — a drop in the group bar writes the group's order (TG-4c).
import { beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useGroupBarDrag } from './useGroupBarDrag'
import { useTabStore } from '../stores/useTabStore'
import { useManualGroupsStore } from '../stores/useManualGroupsStore'
import { useGroupViewStore } from '../stores/useGroupViewStore'
import { manualGroupKey } from '../lib/task-groups/group-key'
import { createTab, type Tab } from '../types/tab'

const G = manualGroupKey('aaaaaaaa')
const mk = (over: Partial<Tab>): Tab => ({ ...createTab({ kind: 'new-tab' }), group: G, ...over })

beforeEach(() => {
  useManualGroupsStore.setState({ order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 } } })
  useGroupViewStore.setState({ selectedGroup: G, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
})

describe('useGroupBarDrag', () => {
  it('undefined while the group range is not in force or no group is selected', () => {
    expect(renderHook(() => useGroupBarDrag(null)).result.current).toBeUndefined()
    useGroupViewStore.setState({ selectedGroup: null })
    expect(renderHook(() => useGroupBarDrag(['x'])).result.current).toBeUndefined()
  })

  it('a drop writes the dragged tab\'s rank (the sidebar\'s decision, the side from the bar\'s order)', () => {
    const a = mk({ createdAt: 1 }), b = mk({ createdAt: 2 }), c = mk({ createdAt: 3 })
    useTabStore.setState({ tabs: Object.fromEntries([a, b, c].map((t) => [t.id, t])), tabOrder: [a.id, b.id, c.id], activeTabId: null })
    const { result } = renderHook(() => useGroupBarDrag([a.id, b.id, c.id]))
    expect(result.current!.locked.size).toBe(0)
    act(() => result.current!.onDrop(c.id, a.id)) // c dragged left over a
    const tabs = useTabStore.getState().tabs
    const order = [a, b, c].map((t) => tabs[t.id]).sort((x, y) => (x.groupRank === undefined ? 1 : y.groupRank === undefined ? -1 : x.groupRank < y.groupRank ? -1 : 1)).map((t) => t.id)
    expect(order[0]).toBe(c.id)
    expect(tabs[c.id].groupRank).toBeDefined()
  })

  it('canDrop says whether a drop would write anything', () => {
    const a = mk({ createdAt: 1 }), b = mk({ createdAt: 2 }), c = mk({ createdAt: 3 })
    useTabStore.setState({ tabs: Object.fromEntries([a, b, c].map((t) => [t.id, t])), tabOrder: [a.id, b.id, c.id], activeTabId: null })
    const { result } = renderHook(() => useGroupBarDrag([a.id, b.id, c.id]))
    expect(result.current!.canDrop(c.id, a.id)).toBe(true)
    expect(result.current!.canDrop(a.id, b.id)).toBe(true)
    expect(result.current!.canDrop(c.id, c.id)).toBe(false)
    expect(result.current!.canDrop(c.id, 'not-in-the-group')).toBe(false)
  })

  it('blockOf: a tab is its own block', () => {
    const a = mk({ createdAt: 1 })
    useTabStore.setState({ tabs: { [a.id]: a }, tabOrder: [a.id], activeTabId: null })
    const { result } = renderHook(() => useGroupBarDrag([a.id]))
    expect(result.current!.blockOf(a.id)).toBe(a.id)
  })

  it('a drop that changes nothing writes nothing', () => {
    const a = mk({ createdAt: 1 }), b = mk({ createdAt: 2 })
    useTabStore.setState({ tabs: Object.fromEntries([a, b].map((t) => [t.id, t])), tabOrder: [a.id, b.id], activeTabId: null })
    const before = useTabStore.getState().tabs
    const { result } = renderHook(() => useGroupBarDrag([a.id, b.id]))
    act(() => result.current!.onDrop(a.id, a.id))
    act(() => result.current!.onDrop(b.id, a.id === b.id ? a.id : b.id))
    expect(useTabStore.getState().tabs).toBe(before)
  })
})
