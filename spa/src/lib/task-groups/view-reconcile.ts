// spa/src/lib/task-groups/view-reconcile.ts — what the task-group view remembers about a manual group dies with the group (task groups spec §6.1: "manual keys against the store"). The team half runs in `handleRosterEvent`
// (lib/team/roster-ws.ts), per host, once that host's roster is read. The manual half is this subscriber: whenever the manual groups change — a delete here, another window's, a profile apply — the memory of ids the store no
// longer holds is forgotten. It does nothing before the manual groups hydrated: an empty store that has not read its storage yet would wipe the memory of every group.
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'

/** Forgets the memory of manual groups that are gone. Safe at any time. */
export function reconcileManualKeys(): void {
  if (!useManualGroupsStore.persist.hasHydrated()) return
  useGroupViewStore.getState().forgetManual(Object.keys(useManualGroupsStore.getState().groups))
}

/** Starts the subscriber (`main.tsx`); the returned function stops it. */
export function startGroupViewReconcile(): () => void {
  reconcileManualKeys()
  const unsubs = [
    useManualGroupsStore.subscribe((state, prev) => {
      if (state.groups !== prev.groups) reconcileManualKeys()
    }),
    useManualGroupsStore.persist.onFinishHydration(reconcileManualKeys),
  ]
  return () => { for (const unsub of unsubs) unsub() }
}
