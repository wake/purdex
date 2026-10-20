// spa/src/lib/task-groups/range.test.ts — the bar's range in the task-group view is the selected group's tabs in bar order (task groups spec §6.2, TG-4a).
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { selectedGroupRange, useSelectedGroupRange } from './range'
import { allGroupTabs } from './group-order'
import { currentGroupOrderCtx } from './current'
import { manualGroupKey, UNGROUPED } from './group-key'
import { useTabStore } from '../../stores/useTabStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { createTab, type Tab } from '../../types/tab'

const IDS = ['aaaaaaaa', 'bbbbbbbb', 'cccccccc']
const KEYS = IDS.map(manualGroupKey)

function setup(tabs: Tab[], selected: string | null, view: 'groups' | 'workspace' = 'groups') {
  useTabStore.setState({ tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), tabOrder: tabs.map((t) => t.id), activeTabId: null })
  useManualGroupsStore.setState({ order: IDS, groups: Object.fromEntries(IDS.map((id) => [id, { name: id, members: [], createdAt: 1 }])) })
  useGroupViewStore.setState({ selectedGroup: selected, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
  useTeamUiStore.setState({ sidebarView: view })
  useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'top' })
}

const tabIn = (group: string | undefined, over: Partial<Tab> = {}): Tab => ({ ...createTab({ kind: 'new-tab' }), ...(group !== undefined ? { group } : {}), ...over })

beforeEach(() => setup([], null))
afterEach(() => useTeamUiStore.setState({ sidebarView: 'workspace' }))

describe('selectedGroupRange', () => {
  it('is the selected group\'s tabs in bar order: pinned first, then rank, then creation', () => {
    const p = tabIn(KEYS[0], { pinned: true, createdAt: 9 })
    const r = tabIn(KEYS[0], { groupRank: 'M', createdAt: 5 })
    const o1 = tabIn(KEYS[0], { createdAt: 1 })
    const o2 = tabIn(KEYS[0], { createdAt: 2 })
    const other = tabIn(KEYS[1])
    setup([o2, other, r, o1, p], KEYS[0])
    expect(selectedGroupRange()).toEqual([p.id, r.id, o1.id, o2.id])
  })

  it('null whenever the workspace rules apply: the workspace view, a narrow sidebar, tabs on the left, no group selected', () => {
    setup([tabIn(KEYS[0])], KEYS[0])
    expect(selectedGroupRange()).not.toBeNull()
    useTeamUiStore.setState({ sidebarView: 'workspace' })
    expect(selectedGroupRange()).toBeNull()
    useTeamUiStore.setState({ sidebarView: 'groups' })
    useLayoutStore.setState({ activityBarWidth: 'narrow' })
    expect(selectedGroupRange()).toBeNull()
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' })
    expect(selectedGroupRange()).toBeNull()
    useLayoutStore.setState({ tabPosition: 'top' })
    useGroupViewStore.setState({ selectedGroup: null })
    expect(selectedGroupRange()).toBeNull()
  })

  it('an empty selected group is an empty range', () => {
    setup([tabIn(KEYS[1])], KEYS[0])
    expect(selectedGroupRange()).toEqual([])
  })

  it('PROPERTY: for random tabs and groups the range of every group is exactly that group\'s tabs in the order groupTabs lists them, and the groups partition the tabs', () => {
    let seed = 12345
    const rand = () => { seed = (seed * 1664525 + 1013904223) >>> 0; return seed / 2 ** 32 }
    const RANKS = ['F', 'M', 'T', 'a']
    for (let round = 0; round < 60; round += 1) {
      const n = 1 + Math.floor(rand() * 12)
      const tabs = Array.from({ length: n }, () => {
        const g = Math.floor(rand() * 4) // 3 = none
        return tabIn(g < 3 ? KEYS[g] : undefined, {
          pinned: rand() < 0.2,
          createdAt: 1 + Math.floor(rand() * 5),
          ...(rand() < 0.4 ? { groupRank: RANKS[Math.floor(rand() * RANKS.length)] } : {}),
        })
      })
      setup(tabs, KEYS[0])
      const all = allGroupTabs(currentGroupOrderCtx())
      const seen: string[] = []
      for (const key of [...KEYS, UNGROUPED]) {
        useGroupViewStore.setState({ selectedGroup: key })
        const range = selectedGroupRange()!
        expect(range).toEqual(all.get(key) ?? [])
        seen.push(...range)
      }
      expect([...seen].sort()).toEqual(tabs.map((t) => t.id).sort())
    }
  })
})

describe('useSelectedGroupRange', () => {
  it('follows the tabs, the selection and the view', () => {
    const a = tabIn(KEYS[0], { createdAt: 1 })
    setup([a], KEYS[0])
    const { result } = renderHook(() => useSelectedGroupRange())
    expect(result.current).toEqual([a.id])
    const b = tabIn(KEYS[0], { createdAt: 2 })
    act(() => { useTabStore.setState({ tabs: { [a.id]: a, [b.id]: b }, tabOrder: [a.id, b.id] }) })
    expect(result.current).toEqual([a.id, b.id])
    act(() => { useGroupViewStore.setState({ selectedGroup: KEYS[1] }) })
    expect(result.current).toEqual([])
    act(() => { useTeamUiStore.setState({ sidebarView: 'workspace' }) })
    expect(result.current).toBeNull()
  })
})
