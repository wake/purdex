// spa/src/features/workspace/components/useGroupDrag.ts — the drag of a solo session onto a group in the task-group view (name pool v2 spec §7, NA-6c). Native HTML drag and drop: the row is `draggable`, a manual group
// or 「未分組」 is a drop target. What a drop means is decided by `lib/task-groups/drag.ts`; this only holds the drag's state and wires the events.
//   - Only the dragged TAB is kept (a drag-over cannot read the data). What it stands for — host, lineage root, the group it is in — is asked again at every drag-over and at the drop, so a root read while the drag
//     was under way, or a tab that changed, is what gets written, never the picture from when the drag began.
//   - A drag that starts on a control inside the row (the close button, an input) is not a group drag; one that is not ours (a file, text) is never touched.
import { useRef, useState, type DragEvent, type MouseEvent } from 'react'
import { acceptsDrag, decideDrop, type DragSource, type DropTarget } from '../../../lib/task-groups/drag'
import type { TaskGroupsEntry } from '../../../stores/useTaskGroupsStore'
import { putInGroup, takeOutOfGroup, type ActionResult } from '../../../lib/task-groups/actions'

export const GROUP_DRAG_MIME = 'application/x-purdex-group-session'

/** Controls inside a row: a press that starts on one is theirs (close, rename), not the start of a drag. */
const CONTROL = 'button, input, textarea, select, a'

interface Input {
  /** The solo session a tab row stands for NOW (host, lineage root, the manual group it is in), or null when the row cannot be dragged. */
  sourceOf: (tabId: string) => DragSource | null
  entryOf: (hostId: string) => TaskGroupsEntry | undefined
  t: (key: string, vars?: Record<string, string | number>) => string
  onNotice: (message: string) => void
  onResult: (r: ActionResult) => void
}

export function useGroupDrag({ sourceOf, entryOf, t, onNotice, onResult }: Input) {
  const [dragTab, setDragTab] = useState<string | null>(null)
  /** The target lit up now: a group id, or `ungrouped`. */
  const [over, setOver] = useState<string | null>(null)
  const startedOnControl = useRef(false)
  const end = () => { setDragTab(null); setOver(null) }

  const rowProps = (tabId: string) => {
    if (sourceOf(tabId) === null) return {}
    return {
      draggable: true,
      onMouseDownCapture: (e: MouseEvent) => { // capture: the close button stops its own mousedown from bubbling
        const hit = (e.target as Element).closest(CONTROL)
        startedOnControl.current = hit !== null && e.currentTarget.contains(hit)
      },
      onDragStart: (e: DragEvent) => {
        if (startedOnControl.current || sourceOf(tabId) === null) { e.preventDefault(); return }
        setDragTab(tabId)
        e.dataTransfer.setData(GROUP_DRAG_MIME, tabId)
        e.dataTransfer.effectAllowed = 'move'
      },
      onDragEnd: end,
    }
  }

  const ours = (e: DragEvent) => dragTab !== null && Array.from(e.dataTransfer.types ?? []).includes(GROUP_DRAG_MIME)

  const targetProps = (target: DropTarget) => {
    const key = target.kind === 'group' ? target.groupId : 'ungrouped'
    return {
      onDragOver: (e: DragEvent) => {
        const source = dragTab !== null ? sourceOf(dragTab) : null
        if (!ours(e) || !source || !acceptsDrag(target, source)) return
        e.preventDefault()
        e.dataTransfer.dropEffect = 'move'
        if (over !== key) setOver(key)
      },
      onDragLeave: (e: DragEvent) => {
        if (over === key && !(e.currentTarget as Node).contains(e.relatedTarget as Node | null)) setOver(null)
      },
      onDrop: (e: DragEvent) => {
        if (!ours(e)) return
        e.preventDefault()
        const source = dragTab !== null ? sourceOf(dragTab) : null
        end()
        if (!source) return
        const d = decideDrop(target, source, entryOf, t)
        if (d.kind === 'notice') onNotice(d.message)
        else if (d.kind === 'put') void putInGroup(d.hostId, d.root, d.groupId).then(onResult)
        else if (d.kind === 'out') void takeOutOfGroup(d.hostId, d.root).then(onResult)
      },
    }
  }

  return { rowProps, targetProps, over }
}
