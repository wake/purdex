// spa/src/features/workspace/components/useGroupDrag.ts — the drag of a solo session onto a group in the task-group view (name pool v2 spec §7, NA-6c). Native HTML drag and drop: the row is `draggable`, a manual group
// or 「未分組」 is a drop target. What a drop means is decided by `lib/task-groups/drag.ts`; this only holds the drag's state and wires the events. The dragged session is kept in state, not read back from the event
// (a drag-over cannot read the data), and a drag that is not ours (a file, text) is never touched.
import { useState, type DragEvent } from 'react'
import { acceptsDrag, decideDrop, type DragSource, type DropTarget } from '../../../lib/task-groups/drag'
import type { TaskGroupsEntry } from '../../../stores/useTaskGroupsStore'
import { putInGroup, takeOutOfGroup, type ActionResult } from '../../../lib/task-groups/actions'

export const GROUP_DRAG_MIME = 'application/x-purdex-group-session'

interface Input {
  /** The solo session a tab row stands for (host, lineage root, the manual group it is in), or null when the row cannot be dragged. */
  sourceOf: (tabId: string) => DragSource | null
  entryOf: (hostId: string) => TaskGroupsEntry | undefined
  t: (key: string, vars?: Record<string, string | number>) => string
  onNotice: (message: string) => void
  onResult: (r: ActionResult) => void
}

export function useGroupDrag({ sourceOf, entryOf, t, onNotice, onResult }: Input) {
  const [source, setSource] = useState<DragSource | null>(null)
  /** The target lit up now: a group id, or `ungrouped`. */
  const [over, setOver] = useState<string | null>(null)
  const end = () => { setSource(null); setOver(null) }

  const rowProps = (tabId: string) => {
    if (sourceOf(tabId) === null) return {}
    return {
      draggable: true,
      onDragStart: (e: DragEvent) => {
        const s = sourceOf(tabId)
        if (!s) { e.preventDefault(); return }
        setSource(s)
        e.dataTransfer.setData(GROUP_DRAG_MIME, tabId)
        e.dataTransfer.effectAllowed = 'move'
      },
      onDragEnd: end,
    }
  }

  const ours = (e: DragEvent) => source !== null && Array.from(e.dataTransfer.types ?? []).includes(GROUP_DRAG_MIME)

  const targetProps = (target: DropTarget) => {
    const key = target.kind === 'group' ? target.groupId : 'ungrouped'
    return {
      onDragOver: (e: DragEvent) => {
        if (!ours(e) || !source || !acceptsDrag(target, source)) return
        e.preventDefault()
        e.dataTransfer.dropEffect = 'move'
        if (over !== key) setOver(key)
      },
      onDragLeave: (e: DragEvent) => {
        if (over === key && !(e.currentTarget as Node).contains(e.relatedTarget as Node | null)) setOver(null)
      },
      onDrop: (e: DragEvent) => {
        if (!ours(e) || !source) return
        e.preventDefault()
        const d = decideDrop(target, source, entryOf, t)
        end()
        if (d.kind === 'notice') onNotice(d.message)
        else if (d.kind === 'put') void putInGroup(d.hostId, d.root, d.groupId).then(onResult)
        else if (d.kind === 'out') void takeOutOfGroup(d.hostId, d.root).then(onResult)
      },
    }
  }

  return { rowProps, targetProps, over }
}
