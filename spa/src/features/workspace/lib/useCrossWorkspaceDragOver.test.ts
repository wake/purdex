import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { DragOverEvent } from '@dnd-kit/core'
import { useCrossWorkspaceDragOver } from './useCrossWorkspaceDragOver'
import { useWorkspaceStore } from '../store'

function mockOver(overTabId: string, overWsId: string | null, isPinned = false): DragOverEvent {
  return {
    active: {
      id: 'tA',
      data: { current: { type: 'tab', tabId: 'tA', sourceWsId: 'w1', isPinned } },
      rect: { current: { initial: null, translated: null } },
    } as never,
    over: {
      id: overTabId,
      data: { current: { type: 'tab', tabId: overTabId, sourceWsId: overWsId } },
      rect: {} as never,
      disabled: false,
    } as never,
    delta: { x: 0, y: 0 },
    collisions: null,
    activatorEvent: new Event('pointerdown'),
  } as never
}

beforeEach(() => {
  useWorkspaceStore.setState({
    workspaces: [
      { id: 'w1', name: 'A', tabs: ['tA', 'tB'], activeTabId: 'tA' },
      { id: 'w2', name: 'B', tabs: ['tC'], activeTabId: 'tC' },
    ],
    activeWorkspaceId: 'w1',
  } as never)
})

describe('useCrossWorkspaceDragOver', () => {
  it('moves tA into w2 when hovered over tC', () => {
    const { result } = renderHook(() => useCrossWorkspaceDragOver())
    act(() => result.current(mockOver('tC', 'w2')))
    const wsById = new Map(useWorkspaceStore.getState().workspaces.map((w) => [w.id, w]))
    expect(wsById.get('w1')!.tabs).toEqual(['tB'])
    expect(wsById.get('w2')!.tabs).toEqual(['tA', 'tC'])
  })

  it('does not move pinned tabs', () => {
    const { result } = renderHook(() => useCrossWorkspaceDragOver())
    act(() => result.current(mockOver('tC', 'w2', true)))
    const wsById = new Map(useWorkspaceStore.getState().workspaces.map((w) => [w.id, w]))
    expect(wsById.get('w1')!.tabs).toEqual(['tA', 'tB'])
    expect(wsById.get('w2')!.tabs).toEqual(['tC'])
  })

  it('is a no-op when same workspace', () => {
    const { result } = renderHook(() => useCrossWorkspaceDragOver())
    act(() => result.current(mockOver('tB', 'w1')))
    const wsById = new Map(useWorkspaceStore.getState().workspaces.map((w) => [w.id, w]))
    expect(wsById.get('w1')!.tabs).toEqual(['tA', 'tB'])
  })

  it('debounces: repeated same-target fires move only once', () => {
    const { result } = renderHook(() => useCrossWorkspaceDragOver())
    act(() => result.current(mockOver('tC', 'w2')))
    // After first call the active's sourceWsId is mutated to 'w2' so a second
    // call with the same target should be a same-workspace no-op.
    act(() => result.current(mockOver('tC', 'w2')))
    const w2 = useWorkspaceStore.getState().workspaces.find((w) => w.id === 'w2')!
    expect(w2.tabs.filter((id) => id === 'tA')).toHaveLength(1)
  })
})
