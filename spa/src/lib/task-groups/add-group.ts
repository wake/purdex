// spa/src/lib/task-groups/add-group.ts — 「新增任務編組」, the + of the bottom nav in the task-group view (task groups spec §6.1): creates 「任務編組 N」 (the first N no group uses), appends it, selects it and starts its rename.
import { useI18nStore } from '../../stores/useI18nStore'
import { createManualGroup, defaultManualGroupName } from './manual-actions'
import { manualGroupKey } from './group-key'
import { selectGroupFromSidebar } from './group-select'
import { requestRename } from './rename-request'

/** Returns the new group's id, or null when it could not be made (the store is full). */
export function addTaskGroup(): string | null {
  const r = createManualGroup({ name: defaultManualGroupName(useI18nStore.getState().t('taskgroups.default_name')) })
  if (!r.ok) return null
  selectGroupFromSidebar(manualGroupKey(r.id))
  requestRename(r.id)
  return r.id
}
