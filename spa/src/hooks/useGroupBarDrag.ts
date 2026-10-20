// spa/src/hooks/useGroupBarDrag.ts — how the top bar's tabs are dragged in the task-group view (task groups spec §6.2, TG-4c): the locked tabs (a team's lead) and the drop, which writes through the sidebar's decision
// (lib/task-groups/bar-drag.ts). Undefined while the group range is not in force. The ctx is read when a drag ends (and once per range for the locks), so it is never older than the drop.
import { useMemo } from 'react'
import { useGroupViewStore } from '../stores/useGroupViewStore'
import { currentGroupOrderCtx } from '../lib/task-groups/current'
import { barLockedTabs, decideBarDrop } from '../lib/task-groups/bar-drag'
import { applyReorder } from '../lib/task-groups/apply-reorder'

export interface GroupBarDrag {
  locked: ReadonlySet<string>
  onDrop: (activeId: string, overId: string) => void
}

export function useGroupBarDrag(groupRange: string[] | null): GroupBarDrag | undefined {
  const selected = useGroupViewStore((s) => s.selectedGroup)
  return useMemo(() => {
    if (groupRange === null || selected === null) return undefined
    return {
      locked: barLockedTabs(selected, currentGroupOrderCtx()),
      onDrop: (activeId, overId) => applyReorder(decideBarDrop(selected, activeId, overId, currentGroupOrderCtx())),
    }
  }, [groupRange, selected])
}
