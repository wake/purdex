// spa/src/lib/task-groups/drop-resolve.ts — what dropping a row in the task-group view means (task groups spec §6.1; TG-3e). Pure: the view says which row was dragged (and the group it is shown in) and what it was dropped on
// — another row, or a group (its header or the gaps); this says whether it is a reorder inside the group or a move to another one.
//   - on a row of the same group: a reorder; the side is the direction of travel (the row ends up where the one it was dropped on was, as `arrayMove` does);
//   - on a row or the body of another manual group / 「未分組」: a move there; a team takes no drop;
//   - a team seat never leaves its team.
import { parseGroupKey, UNGROUPED } from './group-key'
import { acceptsDrag, type DropTarget } from './drag'
import type { DropSide } from './reorder'

export type DragRowKind = 'seat' | 'session' | 'plain'

export interface DraggedRow {
  tabId: string
  /** The group key the row is shown in. */
  groupKey: string
  kind: DragRowKind
}

export type DropOn = { kind: 'row'; tabId: string; groupKey: string } | { kind: 'target'; target: DropTarget }

export type Resolved =
  | { kind: 'ignore' }
  | { kind: 'reorder'; groupKey: string; overId: string; side: DropSide }
  | { kind: 'move'; target: DropTarget }

const IGNORE: Resolved = { kind: 'ignore' }

/** The drop target a group key stands for; null for a team (it takes no drop). */
export function targetOfGroupKey(groupKey: string): DropTarget | null {
  const p = parseGroupKey(groupKey)
  if (p === null || p.kind === 'team') return null
  return p.kind === 'manual' ? { kind: 'group', groupId: p.id } : { kind: 'ungrouped' }
}

/** The manual group a group key names, or null (a team, 「未分組」). */
export function manualIdOfGroupKey(groupKey: string): string | null {
  const p = parseGroupKey(groupKey)
  return p?.kind === 'manual' ? p.id : null
}

/** `order`: the dragged row's group, tab ids in bar order (to tell which way the row travels). */
export function resolveDrop(active: DraggedRow, over: DropOn, order: readonly string[]): Resolved {
  if (over.kind === 'row') {
    if (over.tabId === active.tabId) return IGNORE
    if (over.groupKey === active.groupKey) {
      const from = order.indexOf(active.tabId)
      const to = order.indexOf(over.tabId)
      if (from < 0 || to < 0) return IGNORE
      return { kind: 'reorder', groupKey: active.groupKey, overId: over.tabId, side: from < to ? 'after' : 'before' }
    }
  }
  if (active.kind === 'seat') return IGNORE
  const target = over.kind === 'row' ? targetOfGroupKey(over.groupKey) : over.target
  if (target === null) return IGNORE
  const currentGroupId = manualIdOfGroupKey(active.groupKey)
  // A plain tab is placed by its own `group`, so it can also leave a team (to a manual group or 「未分組」); only a drop where it already is means nothing.
  if (active.kind === 'plain') {
    if (target.kind === 'ungrouped') return active.groupKey === UNGROUPED ? IGNORE : { kind: 'move', target }
    return target.groupId === currentGroupId ? IGNORE : { kind: 'move', target }
  }
  return acceptsDrag(target, { currentGroupId }) ? { kind: 'move', target } : IGNORE
}
