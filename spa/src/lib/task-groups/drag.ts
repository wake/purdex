// spa/src/lib/task-groups/drag.ts — what a drag of a solo session onto the task-group view means (name pool v2 spec §7, NA-6c): onto a manual group of ITS OWN host (L9: a group holds one host's sessions) it is filed
// there, onto 「未分組」 it is taken out of its group; nothing else takes a drop. A host whose groups cannot be edited, or a session whose lineage root is not known yet, still receives the drop and says why instead of
// writing, so a drop never looks accepted and then does nothing without a word. Nothing is written without the real root. Pure: the view turns the decision into the action.
import type { TaskGroupsEntry } from '../../stores/useTaskGroupsStore'
import { hostBlockedReason } from './row-menu'

/** The dragged solo session: its host, its lineage root (null = not known yet) and the manual group it is in now. */
export interface DragSource {
  hostId: string
  root: string | null
  currentGroupId: string | null
}

export type DropTarget = { kind: 'group'; hostId: string; groupId: string } | { kind: 'ungrouped' }

export type DropDecision =
  | { kind: 'ignore' }
  | { kind: 'notice'; message: string }
  | { kind: 'put'; hostId: string; root: string; groupId: string }
  | { kind: 'out'; hostId: string; root: string }

type Translate = (key: string, vars?: Record<string, string | number>) => string

/** Whether the target takes this session at all (what the view lights up and lets the drop through for). */
export function acceptsDrag(target: DropTarget, source: DragSource): boolean {
  if (target.kind === 'ungrouped') return source.currentGroupId !== null
  return target.hostId === source.hostId && target.groupId !== source.currentGroupId
}

export function decideDrop(target: DropTarget, source: DragSource, entryOf: (hostId: string) => TaskGroupsEntry | undefined, t: Translate): DropDecision {
  if (!acceptsDrag(target, source)) return { kind: 'ignore' }
  const blocked = hostBlockedReason(entryOf(source.hostId), t) ?? (source.root === null ? t('taskgroups.root_reading') : null)
  if (blocked !== null) return { kind: 'notice', message: blocked }
  const root = source.root as string // `blocked` is null only for a known root
  return target.kind === 'group'
    ? { kind: 'put', hostId: source.hostId, root, groupId: target.groupId }
    : { kind: 'out', hostId: source.hostId, root }
}
