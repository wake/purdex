import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import type { DragEndEvent, DragOverEvent } from '@dnd-kit/core'
import { computeDragEndAction } from './computeDragEndAction'
import { useCrossWorkspaceDragOver } from './useCrossWorkspaceDragOver'
import { useWorkspaceStore } from '../store'

const ctx = {
  wsIds: ['w1', 'watch'],
  workspaces: [
    { id: 'w1', tabs: ['t1a', 't1b'] },
    { id: 'watch', tabs: ['watch-guardian', 'watch-host-a'] },
  ],
}
function end(active: { id: string; data: unknown }, over: { id: string; data: unknown }): DragEndEvent {
  return {
    active: { id: active.id, data: { current: active.data }, rect: { current: { initial: null, translated: null } } },
    over: { id: over.id, data: { current: over.data }, rect: {} },
    delta: { x: 0, y: 0 }, collisions: null, activatorEvent: new Event('pointerdown'),
  } as unknown as DragEndEvent
}
const tab = (tabId: string, sourceWsId: string) => ({ id: tabId, data: { type: 'tab', tabId, sourceWsId } })

describe('drag guards for the watcher area', () => {
  it('an area tab reorders inside the area', () => {
    const a = computeDragEndAction(end(tab('watch-host-a', 'watch'), tab('watch-guardian', 'watch')), ctx)
    expect(a).toEqual({ kind: 'reorder-workspace-tabs', wsId: 'watch', order: ['watch-host-a', 'watch-guardian'] })
  })

  it('an area tab dropped on a sidebar tab or workspace header is a noop', () => {
    expect(computeDragEndAction(end(tab('watch-host-a', 'watch'), tab('t1a', 'w1')), ctx)).toEqual({ kind: 'noop' })
    expect(computeDragEndAction(end(tab('watch-host-a', 'watch'), { id: 'h', data: { type: 'workspace-header', wsId: 'w1' } }), ctx)).toEqual({ kind: 'noop' })
  })

  it('a sidebar tab dropped into the area is a noop', () => {
    expect(computeDragEndAction(end(tab('t1a', 'w1'), tab('watch-guardian', 'watch')), ctx)).toEqual({ kind: 'noop' })
    expect(computeDragEndAction(end(tab('t1a', 'w1'), { id: 'h', data: { type: 'workspace-header', wsId: 'watch' } }), ctx)).toEqual({ kind: 'noop' })
  })

  it('an ordinary cross-workspace move still works', () => {
    const c = { ...ctx, wsIds: ['w1', 'w2'], workspaces: [...ctx.workspaces, { id: 'w2', tabs: ['t2a'] }] }
    expect(computeDragEndAction(end(tab('t1a', 'w1'), tab('t2a', 'w2')), c).kind).toBe('move-tab-to-workspace')
  })
})

describe('useCrossWorkspaceDragOver', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({
      workspaces: [
        { id: 'w1', name: 'A', tabs: ['tA'], activeTabId: 'tA' },
        { id: 'watch', name: 'W', tabs: ['watch-guardian'], activeTabId: 'watch-guardian' },
      ],
      activeWorkspaceId: 'w1',
    } as never)
  })
  const over = (activeId: string, fromWs: string, overId: string, toWs: string): DragOverEvent => ({
    active: { id: activeId, data: { current: { type: 'tab', tabId: activeId, sourceWsId: fromWs } }, rect: { current: { initial: null, translated: null } } },
    over: { id: overId, data: { current: { type: 'tab', tabId: overId, sourceWsId: toWs } }, rect: {}, disabled: false },
    delta: { x: 0, y: 0 }, collisions: null, activatorEvent: new Event('pointerdown'),
  } as never)

  it('does not move a sidebar tab into the area, nor an area tab out', () => {
    const { result } = renderHook(() => useCrossWorkspaceDragOver())
    act(() => result.current(over('tA', 'w1', 'watch-guardian', 'watch')))
    act(() => result.current(over('watch-guardian', 'watch', 'tA', 'w1')))
    const byId = new Map(useWorkspaceStore.getState().workspaces.map((w) => [w.id, w.tabs]))
    expect(byId.get('w1')).toEqual(['tA'])
    expect(byId.get('watch')).toEqual(['watch-guardian'])
  })
})
