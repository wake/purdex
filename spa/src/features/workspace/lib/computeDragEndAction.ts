import type { DragEndEvent } from '@dnd-kit/core'
import { arrayMove } from '@dnd-kit/sortable'

export type WorkspaceDragData = { type: 'workspace'; wsId: string }
export type TabDragData = {
  type: 'tab'
  tabId: string
  sourceWsId: string | null
  isPinned?: boolean
}
export type DragData = WorkspaceDragData | TabDragData

export type DragEndAction =
  | { kind: 'noop' }
  | { kind: 'reorder-workspaces'; order: string[] }
  | { kind: 'reorder-standalone-tabs'; order: string[] }
  | { kind: 'reorder-workspace-tabs'; wsId: string; order: string[] }

export interface DragEndContext {
  wsIds: string[]
  workspaces: Array<{ id: string; tabs: string[] }>
  standaloneTabIds: string[]
}

const NOOP: DragEndAction = { kind: 'noop' }

export function computeDragEndAction(
  event: DragEndEvent,
  ctx: DragEndContext,
): DragEndAction {
  const { active, over } = event
  if (!over || active.id === over.id) return NOOP

  const activeData = active.data.current as DragData | undefined
  const overData = over.data.current as DragData | undefined
  if (!activeData) return NOOP

  if (activeData.type === 'workspace') {
    const oldIndex = ctx.wsIds.indexOf(String(active.id))
    const newIndex = ctx.wsIds.indexOf(String(over.id))
    if (oldIndex === -1 || newIndex === -1) return NOOP
    return { kind: 'reorder-workspaces', order: arrayMove(ctx.wsIds, oldIndex, newIndex) }
  }

  if (activeData.type === 'tab') {
    // Phase 3 will handle cross-zone drops; for now require tab → tab in same zone.
    if (!overData || overData.type !== 'tab') return NOOP
    if (activeData.sourceWsId !== overData.sourceWsId) return NOOP

    if (activeData.sourceWsId === null) {
      const oldIdx = ctx.standaloneTabIds.indexOf(activeData.tabId)
      const newIdx = ctx.standaloneTabIds.indexOf(overData.tabId)
      if (oldIdx === -1 || newIdx === -1) return NOOP
      return {
        kind: 'reorder-standalone-tabs',
        order: arrayMove(ctx.standaloneTabIds, oldIdx, newIdx),
      }
    }

    const wsId = activeData.sourceWsId
    const ws = ctx.workspaces.find((w) => w.id === wsId)
    if (!ws) return NOOP
    const oldIdx = ws.tabs.indexOf(activeData.tabId)
    const newIdx = ws.tabs.indexOf(overData.tabId)
    if (oldIdx === -1 || newIdx === -1) return NOOP
    return {
      kind: 'reorder-workspace-tabs',
      wsId,
      order: arrayMove(ws.tabs, oldIdx, newIdx),
    }
  }

  return NOOP
}

export interface DragEndDispatch {
  onReorderWorkspaces?: (order: string[]) => void
  onReorderStandaloneTabs?: (order: string[]) => void
  onReorderWorkspaceTabs?: (wsId: string, order: string[]) => void
}

export function dispatchDragEndAction(action: DragEndAction, d: DragEndDispatch): void {
  switch (action.kind) {
    case 'reorder-workspaces':
      d.onReorderWorkspaces?.(action.order)
      return
    case 'reorder-standalone-tabs':
      d.onReorderStandaloneTabs?.(action.order)
      return
    case 'reorder-workspace-tabs':
      d.onReorderWorkspaceTabs?.(action.wsId, action.order)
      return
    case 'noop':
      return
  }
}
