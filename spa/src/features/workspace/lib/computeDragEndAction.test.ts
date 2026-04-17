import { describe, it, expect, vi } from 'vitest'
import type { DragEndEvent } from '@dnd-kit/core'
import {
  computeDragEndAction,
  dispatchDragEndAction,
  type DragEndAction,
  type DragEndContext,
} from './computeDragEndAction'

function mkEvent(
  active: { id: string; data?: unknown } | null,
  over: { id: string; data?: unknown } | null,
): DragEndEvent {
  return {
    active: active
      ? { id: active.id, data: { current: active.data }, rect: { current: { initial: null, translated: null } } }
      : null,
    over: over
      ? { id: over.id, data: { current: over.data }, rect: {} as unknown }
      : null,
    delta: { x: 0, y: 0 },
    collisions: null,
    activatorEvent: new Event('pointerdown'),
  } as unknown as DragEndEvent
}

const ctx = (overrides?: Partial<DragEndContext>): DragEndContext => ({
  wsIds: ['w1', 'w2', 'w3'],
  workspaces: [
    { id: 'w1', tabs: ['t1a', 't1b'] },
    { id: 'w2', tabs: ['t2a'] },
  ],
  standaloneTabIds: ['sA', 'sB', 'sC'],
  ...overrides,
})

describe('computeDragEndAction', () => {
  describe('early returns → noop', () => {
    it('no over target', () => {
      const action = computeDragEndAction(
        mkEvent({ id: 'w1', data: { type: 'workspace', wsId: 'w1' } }, null),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })

    it('active === over', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 'w1', data: { type: 'workspace', wsId: 'w1' } },
          { id: 'w1', data: { type: 'workspace', wsId: 'w1' } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })

    it('activeData undefined', () => {
      const action = computeDragEndAction(
        mkEvent({ id: 'w1' }, { id: 'w2' }),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })

    it('tab dropped on workspace-row sortable (not header) → noop', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 't1a', data: { type: 'tab', tabId: 't1a', sourceWsId: 'w1' } },
          { id: 'w2', data: { type: 'workspace', wsId: 'w2' } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })
  })

  describe('workspace reorder', () => {
    it('moves workspace from 0 to 2', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 'w1', data: { type: 'workspace', wsId: 'w1' } },
          { id: 'w3', data: { type: 'workspace', wsId: 'w3' } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'reorder-workspaces', order: ['w2', 'w3', 'w1'] })
    })

    it('returns noop when workspace id not in wsIds', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 'wX', data: { type: 'workspace', wsId: 'wX' } },
          { id: 'w1', data: { type: 'workspace', wsId: 'w1' } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })
  })

  describe('standalone tab reorder (sourceWsId = null)', () => {
    it('moves sA to end', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 'sA', data: { type: 'tab', tabId: 'sA', sourceWsId: null } },
          { id: 'sC', data: { type: 'tab', tabId: 'sC', sourceWsId: null } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'reorder-standalone-tabs', order: ['sB', 'sC', 'sA'] })
    })

    it('returns noop when tab id not in standaloneTabIds', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 'sX', data: { type: 'tab', tabId: 'sX', sourceWsId: null } },
          { id: 'sA', data: { type: 'tab', tabId: 'sA', sourceWsId: null } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })
  })

  describe('workspace tab reorder (sourceWsId = wsId)', () => {
    it('reorders tabs within w1', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 't1a', data: { type: 'tab', tabId: 't1a', sourceWsId: 'w1' } },
          { id: 't1b', data: { type: 'tab', tabId: 't1b', sourceWsId: 'w1' } },
        ),
        ctx(),
      )
      expect(action).toEqual({
        kind: 'reorder-workspace-tabs',
        wsId: 'w1',
        order: ['t1b', 't1a'],
      })
    })

    it('returns noop when workspace not found', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 't1a', data: { type: 'tab', tabId: 't1a', sourceWsId: 'wGhost' } },
          { id: 't1b', data: { type: 'tab', tabId: 't1b', sourceWsId: 'wGhost' } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })

    it('returns noop when tab id not in ws.tabs', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 'tGhost', data: { type: 'tab', tabId: 'tGhost', sourceWsId: 'w1' } },
          { id: 't1b', data: { type: 'tab', tabId: 't1b', sourceWsId: 'w1' } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })
  })

  describe('cross-ws (Phase 3 PR D)', () => {
    it('tab → another ws tab-slot → move-tab-to-workspace afterTabId=targetTab', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 't1a', data: { type: 'tab', tabId: 't1a', sourceWsId: 'w1' } },
          { id: 't2a', data: { type: 'tab', tabId: 't2a', sourceWsId: 'w2' } },
        ),
        ctx(),
      )
      expect(action).toEqual({
        kind: 'move-tab-to-workspace',
        tabId: 't1a',
        targetWsId: 'w2',
        afterTabId: 't2a',
      })
    })

    it('tab → own workspace-header drop target → noop (no phantom move)', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 't1a', data: { type: 'tab', tabId: 't1a', sourceWsId: 'w1' } },
          { id: 'ws-header-w1', data: { type: 'workspace-header', wsId: 'w1' } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })

    it('tab → workspace-header drop target → move-tab-to-workspace afterTabId=null (prepend)', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 't1a', data: { type: 'tab', tabId: 't1a', sourceWsId: 'w1' } },
          { id: 'ws-header-w2', data: { type: 'workspace-header', wsId: 'w2' } },
        ),
        ctx(),
      )
      expect(action).toEqual({
        kind: 'move-tab-to-workspace',
        tabId: 't1a',
        targetWsId: 'w2',
        afterTabId: null,
      })
    })

    it('tab → home-header drop target → move-tab-to-standalone', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 't1a', data: { type: 'tab', tabId: 't1a', sourceWsId: 'w1' } },
          { id: 'home-header', data: { type: 'home-header' } },
        ),
        ctx(),
      )
      expect(action).toEqual({
        kind: 'move-tab-to-standalone',
        tabId: 't1a',
        sourceWsId: 'w1',
      })
    })

    it('standalone tab → workspace-header → move-tab-to-workspace afterTabId=null', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 'sA', data: { type: 'tab', tabId: 'sA', sourceWsId: null } },
          { id: 'ws-header-w2', data: { type: 'workspace-header', wsId: 'w2' } },
        ),
        ctx(),
      )
      expect(action).toEqual({
        kind: 'move-tab-to-workspace',
        tabId: 'sA',
        targetWsId: 'w2',
        afterTabId: null,
      })
    })

    it('standalone tab → other ws tab-slot → move-tab-to-workspace afterTabId=targetTab', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 'sA', data: { type: 'tab', tabId: 'sA', sourceWsId: null } },
          { id: 't2a', data: { type: 'tab', tabId: 't2a', sourceWsId: 'w2' } },
        ),
        ctx(),
      )
      expect(action).toEqual({
        kind: 'move-tab-to-workspace',
        tabId: 'sA',
        targetWsId: 'w2',
        afterTabId: 't2a',
      })
    })

    it('standalone tab → home-header → noop (already standalone)', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 'sA', data: { type: 'tab', tabId: 'sA', sourceWsId: null } },
          { id: 'home-header', data: { type: 'home-header' } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })

    it('pinned tab → other ws tab-slot → noop (#404)', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 't1a', data: { type: 'tab', tabId: 't1a', sourceWsId: 'w1', isPinned: true } },
          { id: 't2a', data: { type: 'tab', tabId: 't2a', sourceWsId: 'w2' } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })

    it('pinned tab → workspace-header → noop (#404)', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 't1a', data: { type: 'tab', tabId: 't1a', sourceWsId: 'w1', isPinned: true } },
          { id: 'ws-header-w2', data: { type: 'workspace-header', wsId: 'w2' } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })

    it('pinned tab → home-header → noop (#404)', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 't1a', data: { type: 'tab', tabId: 't1a', sourceWsId: 'w1', isPinned: true } },
          { id: 'home-header', data: { type: 'home-header' } },
        ),
        ctx(),
      )
      expect(action).toEqual({ kind: 'noop' })
    })

    it('pinned tab same-ws reorder still works (#404)', () => {
      const action = computeDragEndAction(
        mkEvent(
          { id: 't1a', data: { type: 'tab', tabId: 't1a', sourceWsId: 'w1', isPinned: true } },
          { id: 't1b', data: { type: 'tab', tabId: 't1b', sourceWsId: 'w1' } },
        ),
        ctx(),
      )
      expect(action).toEqual({
        kind: 'reorder-workspace-tabs',
        wsId: 'w1',
        order: ['t1b', 't1a'],
      })
    })
  })
})

describe('dispatchDragEndAction', () => {
  it('dispatches reorder-workspaces to onReorderWorkspaces', () => {
    const d = {
      onReorderWorkspaces: vi.fn(),
      onReorderStandaloneTabs: vi.fn(),
      onReorderWorkspaceTabs: vi.fn(),
    }
    dispatchDragEndAction({ kind: 'reorder-workspaces', order: ['w2', 'w1'] }, d)
    expect(d.onReorderWorkspaces).toHaveBeenCalledWith(['w2', 'w1'])
    expect(d.onReorderStandaloneTabs).not.toHaveBeenCalled()
    expect(d.onReorderWorkspaceTabs).not.toHaveBeenCalled()
  })

  it('dispatches reorder-standalone-tabs to onReorderStandaloneTabs', () => {
    const d = { onReorderStandaloneTabs: vi.fn() }
    dispatchDragEndAction({ kind: 'reorder-standalone-tabs', order: ['sB', 'sA'] }, d)
    expect(d.onReorderStandaloneTabs).toHaveBeenCalledWith(['sB', 'sA'])
  })

  it('dispatches reorder-workspace-tabs to onReorderWorkspaceTabs with wsId', () => {
    const d = { onReorderWorkspaceTabs: vi.fn() }
    dispatchDragEndAction(
      { kind: 'reorder-workspace-tabs', wsId: 'w1', order: ['t1b', 't1a'] },
      d,
    )
    expect(d.onReorderWorkspaceTabs).toHaveBeenCalledWith('w1', ['t1b', 't1a'])
  })

  it('noop action fires nothing', () => {
    const d = {
      onReorderWorkspaces: vi.fn(),
      onReorderStandaloneTabs: vi.fn(),
      onReorderWorkspaceTabs: vi.fn(),
    }
    dispatchDragEndAction({ kind: 'noop' }, d)
    expect(d.onReorderWorkspaces).not.toHaveBeenCalled()
    expect(d.onReorderStandaloneTabs).not.toHaveBeenCalled()
    expect(d.onReorderWorkspaceTabs).not.toHaveBeenCalled()
  })

  it('tolerates missing callbacks (optional chaining)', () => {
    const action: DragEndAction = { kind: 'reorder-workspaces', order: ['a'] }
    expect(() => dispatchDragEndAction(action, {})).not.toThrow()
  })

  it('dispatches move-tab-to-workspace with tabId, targetWsId, afterTabId', () => {
    const d = { onMoveTabToWorkspace: vi.fn() }
    dispatchDragEndAction(
      { kind: 'move-tab-to-workspace', tabId: 't1', targetWsId: 'w2', afterTabId: null },
      d,
    )
    expect(d.onMoveTabToWorkspace).toHaveBeenCalledWith('t1', 'w2', null)
  })

  it('dispatches move-tab-to-workspace with non-null afterTabId', () => {
    const d = { onMoveTabToWorkspace: vi.fn() }
    dispatchDragEndAction(
      { kind: 'move-tab-to-workspace', tabId: 't1', targetWsId: 'w2', afterTabId: 't2' },
      d,
    )
    expect(d.onMoveTabToWorkspace).toHaveBeenCalledWith('t1', 'w2', 't2')
  })

  it('dispatches move-tab-to-standalone with tabId and sourceWsId', () => {
    const d = { onMoveTabToStandalone: vi.fn() }
    dispatchDragEndAction(
      { kind: 'move-tab-to-standalone', tabId: 't1', sourceWsId: 'w1' },
      d,
    )
    expect(d.onMoveTabToStandalone).toHaveBeenCalledWith('t1', 'w1')
  })
})
