import type { DragEndEvent } from '@dnd-kit/core'
import { arrayMove } from '@dnd-kit/sortable'

export type WorkspaceDragData = { type: 'workspace'; wsId: string }
export type TabDragData = {
  type: 'tab'
  tabId: string
  /** `null` = rendered outside any workspace list (InlineTab's default). No list does since P3c-2 — every tab
   *  belongs to a workspace (Profile Sync spec §4.3) — so a `null` on either side of a drop is a noop. */
  sourceWsId: string | null
  isPinned?: boolean
}
export type WorkspaceHeaderDropData = { type: 'workspace-header'; wsId: string }
export type DragData =
  | WorkspaceDragData
  | TabDragData
  | WorkspaceHeaderDropData

export type DragEndAction =
  | { kind: 'noop' }
  | { kind: 'reorder-workspaces'; order: string[] }
  | { kind: 'reorder-workspace-tabs'; wsId: string; order: string[] }
  | {
      kind: 'move-tab-to-workspace'
      tabId: string
      targetWsId: string
      afterTabId: string | null
    }

export interface DragEndContext {
  wsIds: string[]
  workspaces: Array<{ id: string; tabs: string[] }>
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

  if (activeData.type !== 'tab') return NOOP
  if (!overData) return NOOP

  // #404 — pinned tab may only reorder within its own workspace.
  // Same-ws tab-slot drop falls through to the reorder branch below;
  // every other drop target is rejected.
  if (activeData.isPinned) {
    const isSameWsTabDrop =
      overData.type === 'tab' && overData.sourceWsId === activeData.sourceWsId
    if (!isSameWsTabDrop) return NOOP
  }

  // Same-zone tab reorder
  if (overData.type === 'tab' && overData.sourceWsId === activeData.sourceWsId) {
    const sourceWsId = activeData.sourceWsId
    if (sourceWsId === null) return NOOP
    const ws = ctx.workspaces.find((w) => w.id === sourceWsId)
    if (!ws) return NOOP
    const oldIdx = ws.tabs.indexOf(activeData.tabId)
    const newIdx = ws.tabs.indexOf(overData.tabId)
    if (oldIdx === -1 || newIdx === -1) return NOOP
    return {
      kind: 'reorder-workspace-tabs',
      wsId: sourceWsId,
      order: arrayMove(ws.tabs, oldIdx, newIdx),
    }
  }

  // Cross-zone: tab dropped on a tab of another workspace.
  if (overData.type === 'tab' && overData.sourceWsId !== activeData.sourceWsId) {
    // A tab never leaves its workspace for "no workspace" (Profile Sync spec §4.3).
    if (overData.sourceWsId === null) return NOOP
    return {
      kind: 'move-tab-to-workspace',
      tabId: activeData.tabId,
      targetWsId: overData.sourceWsId,
      afterTabId: overData.tabId,
    }
  }

  // Workspace header drop target → prepend to that workspace.
  // Dropping a tab onto its own workspace header is a no-op — the tab already
  // lives there, and falling through would call insertTab unnecessarily and
  // flicker activeTabId without moving anything.
  if (overData.type === 'workspace-header') {
    if (overData.wsId === activeData.sourceWsId) return NOOP
    return {
      kind: 'move-tab-to-workspace',
      tabId: activeData.tabId,
      targetWsId: overData.wsId,
      afterTabId: null,
    }
  }

  return NOOP
}

export interface DragEndDispatch {
  onReorderWorkspaces?: (order: string[]) => void
  onReorderWorkspaceTabs?: (wsId: string, order: string[]) => void
  onMoveTabToWorkspace?: (tabId: string, targetWsId: string, afterTabId: string | null) => void
}

export function dispatchDragEndAction(action: DragEndAction, d: DragEndDispatch): void {
  switch (action.kind) {
    case 'reorder-workspaces':
      d.onReorderWorkspaces?.(action.order)
      return
    case 'reorder-workspace-tabs':
      d.onReorderWorkspaceTabs?.(action.wsId, action.order)
      return
    case 'move-tab-to-workspace':
      d.onMoveTabToWorkspace?.(action.tabId, action.targetWsId, action.afterTabId)
      return
    case 'noop':
      return
    default: {
      const _exhaustive: never = action
      void _exhaustive
      return
    }
  }
}
