// spa/src/features/workspace/lib/group-drag.ts — the task-group view's rows and groups in the sidebar's drag-and-drop (task groups spec §6.1; TG-3e). The sidebar has ONE DndContext (ActivityBarWide); the group view's rows are
// sortables and its groups droppables inside it, tagged with the data types below. ActivityBarWide hands a drag that starts on a group row to the handler the mounted TaskGroupView registered here, so the view's data
// (which group a row is shown in, which tabs are sessions) stays in the view.
import type { DragEndEvent } from '@dnd-kit/core'
import type { DragRowKind } from '../../../lib/task-groups/drop-resolve'
import type { DropTarget } from '../../../lib/task-groups/drag'

export interface GroupTabDragData { type: 'group-tab'; tabId: string; groupKey: string; kind: DragRowKind; seatId?: string }
export interface GroupDropData { type: 'group-drop'; target: DropTarget }

export const isGroupTabDrag = (d: unknown): d is GroupTabDragData => (d as { type?: string } | undefined)?.type === 'group-tab'
export const isGroupDropData = (d: unknown): d is GroupDropData => (d as { type?: string } | undefined)?.type === 'group-drop'
/** Either end of a drag in the group view: the collision detection keeps these two apart from the workspace view's. */
export const isGroupViewData = (d: unknown): boolean => isGroupTabDrag(d) || isGroupDropData(d)

/** The mounted view's drop handler (one view is mounted at a time). */
export const groupDragEnd: { current: ((e: DragEndEvent) => void) | null } = { current: null }
