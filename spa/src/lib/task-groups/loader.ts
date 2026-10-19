// spa/src/lib/task-groups/loader.ts — when a host's task groups may be read (name pool v2 spec §7, NA-6a): only when its daemon declares `task_groups.v1`. The gate is the Nex store's capability entry, as for
// the personas (lib/names/persona-loader.ts); a stale entry (`fetchedAt` 0: invalidated by a reconnect, not yet refetched) keeps the old capabilities, which a downgraded daemon no longer has, so it never counts.
// Nothing here starts by itself: the sidebar's task-group view asks (NA-6b), so a device that never opens that view never reads the groups.
import { useNexHostStore, selectTaskGroupsV1 } from '../../stores/useNexHostStore'
import { useTaskGroupsStore } from '../../stores/useTaskGroupsStore'
import { useHostStore } from '../../stores/useHostStore'
import { hostEndpointKey } from '../../stores/useHostConfigStore'

export function taskGroupsSupported(hostId: string): boolean {
  const state = useNexHostStore.getState()
  return (state.byHost[hostId]?.fetchedAt ?? 0) > 0 && selectTaskGroupsV1(hostId)(state)
}

/** Reads the host's groups now when its fresh capabilities declare `task_groups.v1`; otherwise does nothing. */
export async function loadTaskGroupsIfSupported(hostId: string): Promise<void> {
  if (taskGroupsSupported(hostId)) await useTaskGroupsStore.getState().load(hostId)
}

/**
 * Forgets a host's cached groups when they can no longer be the answering daemon's: the host is removed, moved (address, port or token) under the same id, or its endpoint now answers as another daemon
 * (the identity check's `daemonIdMismatch` appears or names a different daemon). Reads nothing; returns the unsubscribe.
 */
export function startTaskGroupsLifecycle(): () => void {
  return useHostStore.subscribe((next, prev) => {
    for (const hostId of Object.keys(prev.hosts)) {
      if (!next.hosts[hostId] || hostEndpointKey(prev.hosts[hostId]) !== hostEndpointKey(next.hosts[hostId])) useTaskGroupsStore.getState().forget(hostId)
    }
    for (const [hostId, rt] of Object.entries(next.runtime)) {
      const observed = rt?.daemonIdMismatch?.observed
      if (observed !== undefined && observed !== prev.runtime[hostId]?.daemonIdMismatch?.observed) useTaskGroupsStore.getState().forget(hostId)
    }
  })
}
