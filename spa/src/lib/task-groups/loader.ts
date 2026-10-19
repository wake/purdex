// spa/src/lib/task-groups/loader.ts — when a host's task groups may be read (name pool v2 spec §7, NA-6a): only when its daemon declares `task_groups.v1`. The gate is the Nex store's capability entry, as for
// the personas (lib/names/persona-loader.ts); a stale entry (`fetchedAt` 0: invalidated by a reconnect, not yet refetched) keeps the old capabilities, which a downgraded daemon no longer has, so it never counts.
// Nothing here starts by itself: the sidebar's task-group view asks (NA-6b), so a device that never opens that view never reads the groups.
import { useNexHostStore, selectTaskGroupsV1 } from '../../stores/useNexHostStore'
import { useTaskGroupsStore } from '../../stores/useTaskGroupsStore'

export function taskGroupsSupported(hostId: string): boolean {
  const state = useNexHostStore.getState()
  return (state.byHost[hostId]?.fetchedAt ?? 0) > 0 && selectTaskGroupsV1(hostId)(state)
}

/** Reads the host's groups now when its fresh capabilities declare `task_groups.v1`; otherwise does nothing. */
export async function loadTaskGroupsIfSupported(hostId: string): Promise<void> {
  if (taskGroupsSupported(hostId)) await useTaskGroupsStore.getState().load(hostId)
}
