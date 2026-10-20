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
  canDrop: (activeId: string, overId: string) => boolean
  blockOf: (tabId: string) => string
}

export function useGroupBarDrag(groupRange: string[] | null): GroupBarDrag | undefined {
  const selected = useGroupViewStore((s) => s.selectedGroup)
  return useMemo(() => {
    if (groupRange === null || selected === null) return undefined
    const ctx = currentGroupOrderCtx()
    return {
      locked: barLockedTabs(selected, ctx),
      // the tabs of one team seat move as one block (a seat's tabs share the seat's session id)
      blockOf: (tabId) => ctx.index.seatByTabId.get(tabId)?.seat.session.session_id ?? tabId,
      onDrop: (activeId, overId) => applyReorder(decideBarDrop(selected, activeId, overId, currentGroupOrderCtx())),
      canDrop: (activeId, overId) => decideBarDrop(selected, activeId, overId, currentGroupOrderCtx()).kind !== 'ignore',
    }
  }, [groupRange, selected])
}
