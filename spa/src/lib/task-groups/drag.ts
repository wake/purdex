// spa/src/lib/task-groups/drag.ts — what a drag of a solo session onto the task-group view means (task groups spec §6.1, TG-2b; name pool v2 spec §7, NA-6c): onto a manual group it is filed there — a group gathers
// sessions of ANY host — onto 「未分組」 it is taken out of its group; nothing else takes a drop. The root a write needs is not asked here: the caller gets it with `resolveRoot` (session-actions.ts) and says
// `no-root` only when the session's host cannot answer. Pure: the view turns the decision into the action.

/** The dragged solo session: the manual group it is in now (null = none). */
export interface DragSource {
  currentGroupId: string | null
}

export type DropTarget = { kind: 'group'; groupId: string } | { kind: 'ungrouped' }

export type DropDecision =
  | { kind: 'ignore' }
  | { kind: 'put'; groupId: string }
  | { kind: 'out' }

/** Whether the target takes this session at all (what the view lights up and lets the drop through for). */
export function acceptsDrag(target: DropTarget, source: DragSource): boolean {
  if (target.kind === 'ungrouped') return source.currentGroupId !== null
  return target.groupId !== source.currentGroupId
}

export function decideDrop(target: DropTarget, source: DragSource): DropDecision {
  if (!acceptsDrag(target, source)) return { kind: 'ignore' }
  return target.kind === 'group' ? { kind: 'put', groupId: target.groupId } : { kind: 'out' }
}
