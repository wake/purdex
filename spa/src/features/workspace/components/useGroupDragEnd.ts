// spa/src/features/workspace/components/useGroupDragEnd.ts — registers the task-group view's drop handler with the sidebar's DndContext (lib/group-drag.ts; TG-3e). Re-registered every render so it reads the latest data.
import { useEffect } from 'react'
import type { DragEndEvent } from '@dnd-kit/core'
import { applyGroupDrop, type DropDeps } from '../../../lib/task-groups/apply-drop'
import type { DropOn } from '../../../lib/task-groups/drop-resolve'
import { groupDragEnd, isGroupDropData, isGroupTabDrag } from '../lib/group-drag'

export function useGroupDragEnd(deps: DropDeps) {
  useEffect(() => {
    groupDragEnd.current = (e: DragEndEvent) => {
      const active = e.active.data.current
      if (!e.over || !isGroupTabDrag(active)) return
      const o = e.over.data.current
      let over: DropOn
      if (isGroupTabDrag(o)) over = { kind: 'row', tabId: o.tabId, groupKey: o.groupKey }
      else if (isGroupDropData(o)) over = { kind: 'target', target: o.target }
      else return
      applyGroupDrop({ tabId: active.tabId, groupKey: active.groupKey, kind: active.kind }, over, deps)
    }
    return () => { groupDragEnd.current = null }
  })
}
