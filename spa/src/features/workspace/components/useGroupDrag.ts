// spa/src/features/workspace/components/useGroupDrag.ts — the drag of a solo session onto a group in the task-group view (task groups spec §6.1, TG-2b; name pool v2 spec §7, NA-6c). Native HTML drag and drop: the row is
// `draggable`, a manual group or 「未分組」 is a drop target. What a drop means is decided by `lib/task-groups/drag.ts`, and the write is `lib/task-groups/session-actions.ts`; this only holds the drag's state and wires the events.
//   - Only the dragged TAB is kept (a drag-over cannot read the data). What it stands for — host, agent session, the group it is in — is asked again at every drag-over and at the drop, so a tab that changed while the drag
//     was under way is what gets written, never the picture from when the drag began.
//   - A drag that starts on a control inside the row (the close button, an input) is not a group drag; one that is not ours (a file, text) is never touched.
import { useRef, useState, type DragEvent, type MouseEvent } from 'react'
import { acceptsDrag, decideDrop, type DropTarget } from '../../../lib/task-groups/drag'
import { fileSession, takeSessionOut, type SessionRef } from '../../../lib/task-groups/session-actions'
import type { ManualActionResult } from '../../../lib/task-groups/manual-actions'

export const GROUP_DRAG_MIME = 'application/x-purdex-group-session'

/** Controls inside a row: a press that starts on one is theirs (close, rename), not the start of a drag. */
const CONTROL = 'button, input, textarea, select, a'

interface Input {
  /** The solo session a tab row stands for NOW and the manual group it is in, or null when the row cannot be dragged. */
  sourceOf: (tabId: string) => { session: SessionRef; currentGroupId: string | null } | null
  onResult: (r: ManualActionResult) => void
}

export function useGroupDrag({ sourceOf, onResult }: Input) {
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
        const d = decideDrop(target, source)
        if (d.kind === 'put') void fileSession(source.session, d.groupId).then(onResult)
        else if (d.kind === 'out') void takeSessionOut(source.session).then(onResult)
      },
    }
  }

  return { rowProps, targetProps, over }
}
