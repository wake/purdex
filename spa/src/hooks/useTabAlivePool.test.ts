import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTabAlivePool } from './useTabAlivePool'
import { useUISettingsStore } from '../stores/useUISettingsStore'

function resetSettings(overrides?: { keepAliveCount?: number; keepAlivePinned?: boolean }) {
  useUISettingsStore.setState({
    keepAliveCount: overrides?.keepAliveCount ?? 0,
    keepAlivePinned: overrides?.keepAlivePinned ?? false,
    terminalSettingsVersion: 0,
    terminalRevealDelay: 300,
    terminalRenderer: 'webgl',
  })
}

interface MockTab { id: string; pinned: boolean; light?: boolean }

describe('useTabAlivePool', () => {
  beforeEach(() => resetSettings())

  it('light tabs stay alive at keepAliveCount=0 (not just the active tab)', () => {
    // Editor/preview/settings tabs are light → always kept alive so they never
    // remount on a tab switch, independent of the terminal-oriented keepAliveCount.
    const tabs: MockTab[] = [
      { id: 'ed1', pinned: false, light: true },
      { id: 'ed2', pinned: false, light: true },
    ]
    const { result } = renderHook(() => useTabAlivePool('ed1', tabs))
    expect(result.current.aliveIds).toContain('ed1')
    expect(result.current.aliveIds).toContain('ed2')
  })

  it('heavy tabs remain bounded by keepAliveCount=0 (only the active heavy tab)', () => {
    const tabs: MockTab[] = [
      { id: 't1', pinned: false }, // heavy (light omitted)
      { id: 't2', pinned: false },
    ]
    const { result } = renderHook(() => useTabAlivePool('t1', tabs))
    expect(result.current.aliveIds).toEqual(['t1'])
  })

  it('mixed pool at keepAliveCount=0: all light alive + only the active heavy', () => {
    const tabs: MockTab[] = [
      { id: 'term', pinned: false }, // heavy
      { id: 'ed1', pinned: false, light: true },
      { id: 'ed2', pinned: false, light: true },
    ]
    const { result, rerender } = renderHook(
      ({ activeId }) => useTabAlivePool(activeId, tabs),
      { initialProps: { activeId: 'term' as string } },
    )
    // term active heavy → alive; both editors light → alive.
    expect(result.current.aliveIds).toEqual(expect.arrayContaining(['term', 'ed1', 'ed2']))

    // Switch to a light tab: the heavy terminal is no longer active → evicted
    // (keepAliveCount=0), but both editors stay alive.
    rerender({ activeId: 'ed1' })
    expect(result.current.aliveIds).toContain('ed1')
    expect(result.current.aliveIds).toContain('ed2')
    expect(result.current.aliveIds).not.toContain('term')
  })

  it('keepAliveCount=0: pool only contains activeTabId', () => {
    const tabs: MockTab[] = [
      { id: 'a', pinned: false },
      { id: 'b', pinned: false },
    ]
    const { result } = renderHook(() => useTabAlivePool('a', tabs))
    expect(result.current.aliveIds).toEqual(['a'])
  })

  it('keepAliveCount=0: no activeTab returns empty pool', () => {
    const { result } = renderHook(() => useTabAlivePool(null, []))
    expect(result.current.aliveIds).toEqual([])
  })

  it('keepAliveCount=2: pool keeps last 2+1 visited tabs', () => {
    resetSettings({ keepAliveCount: 2 })
    const tabs: MockTab[] = [
      { id: 'a', pinned: false },
      { id: 'b', pinned: false },
      { id: 'c', pinned: false },
      { id: 'd', pinned: false },
    ]

    const { result, rerender } = renderHook(
      ({ activeId }) => useTabAlivePool(activeId, tabs),
      { initialProps: { activeId: 'a' as string } },
    )
    rerender({ activeId: 'b' })
    rerender({ activeId: 'c' })
    rerender({ activeId: 'd' })
    // Pool: d (active) + c, b (2 kept) = 3 total. a evicted.
    expect(result.current.aliveIds).toContain('d')
    expect(result.current.aliveIds).toContain('c')
    expect(result.current.aliveIds).toContain('b')
    expect(result.current.aliveIds).not.toContain('a')
  })

  it('keepAlivePinned=true: pinned tabs do not count toward pool limit', () => {
    resetSettings({ keepAliveCount: 1, keepAlivePinned: true })
    const tabs: MockTab[] = [
      { id: 'pinned1', pinned: true },
      { id: 'normal1', pinned: false },
      { id: 'normal2', pinned: false },
    ]

    const { result, rerender } = renderHook(
      ({ activeId }) => useTabAlivePool(activeId, tabs),
      { initialProps: { activeId: 'pinned1' as string } },
    )
    rerender({ activeId: 'normal1' })
    rerender({ activeId: 'normal2' })
    // normal2 (active) + normal1 (1 kept) + pinned1 (exempt, doesn't count)
    expect(result.current.aliveIds).toContain('normal2')
    expect(result.current.aliveIds).toContain('pinned1')
    expect(result.current.aliveIds).toContain('normal1')
  })

  it('terminalSettingsVersion bump resets pool to only active tab', () => {
    resetSettings({ keepAliveCount: 3 })
    const tabs: MockTab[] = [
      { id: 'a', pinned: false },
      { id: 'b', pinned: false },
    ]

    const { result, rerender } = renderHook(
      ({ activeId }) => useTabAlivePool(activeId, tabs),
      { initialProps: { activeId: 'a' as string } },
    )
    rerender({ activeId: 'b' })
    expect(result.current.aliveIds).toContain('a')

    // Bump version
    act(() => useUISettingsStore.getState().bumpTerminalSettingsVersion())
    rerender({ activeId: 'b' })
    expect(result.current.poolVersion).toBe(1)
    expect(result.current.aliveIds).toEqual(['b'])
  })

  it('closed tab is removed from pool without backfill', () => {
    resetSettings({ keepAliveCount: 2 })
    const tabs: MockTab[] = [
      { id: 'a', pinned: false },
      { id: 'b', pinned: false },
      { id: 'c', pinned: false },
      { id: 'd', pinned: false },
    ]

    const { result, rerender } = renderHook(
      ({ activeId, currentTabs }) => useTabAlivePool(activeId, currentTabs),
      { initialProps: { activeId: 'a' as string, currentTabs: tabs } },
    )
    rerender({ activeId: 'b', currentTabs: tabs })
    rerender({ activeId: 'c', currentTabs: tabs })
    // Pool: [c, b, a]. d never visited.
    expect(result.current.aliveIds).toEqual(['c', 'b', 'a'])

    // Close tab b (remove from tabs)
    const tabsWithoutB = tabs.filter((t) => t.id !== 'b')
    rerender({ activeId: 'c', currentTabs: tabsWithoutB })
    // b removed from pool. Next in history is a, which naturally stays.
    expect(result.current.aliveIds).toContain('c')
    expect(result.current.aliveIds).not.toContain('b')
    expect(result.current.aliveIds).toContain('a')
  })

  it('keepAliveCount change clears history', () => {
    resetSettings({ keepAliveCount: 2 })
    const tabs: MockTab[] = [
      { id: 'a', pinned: false },
      { id: 'b', pinned: false },
    ]

    const { result, rerender } = renderHook(
      ({ activeId }) => useTabAlivePool(activeId, tabs),
      { initialProps: { activeId: 'a' as string } },
    )
    rerender({ activeId: 'b' })
    expect(result.current.aliveIds).toContain('a')

    // Change keepAliveCount
    act(() => useUISettingsStore.getState().setKeepAliveCount(1))
    rerender({ activeId: 'b' })
    // History cleared, only active tab
    expect(result.current.aliveIds).toEqual(['b'])
  })
})
