// spa/src/features/workspace/lib/adopt-standalone.ts — EVERY TAB BELONGS TO EXACTLY ONE WORKSPACE, kept as a
// standing invariant rather than a boot step (Profile Sync spec §4.3; P3 plan, P3c-1).
//
// `insertTab` (../store.ts) gives every tab it is asked about a workspace. This file is for the tabs nobody
// asked it about: device-state's restore and merge write `tabOrder` wholesale (lib/device-state, until P4b), a
// pre-P3c device has them persisted, and there may be a producer nobody has found. Whenever the tab world
// changes and a tab is in no workspace, it is moved into `Unsorted` (`UNSORTED_WORKSPACE_ID`), created only
// then. It also re-points an `activeWorkspaceId` of `null` — the old "Home" view, which no longer exists — at
// a workspace. It is app-level, not Profile Sync's: it runs for a user with no master, too.
//
// NOT IN THE SAME TICK. The tab store and the workspace store are two stores: a restore writes one and then
// the other, and another window's `addTab` + `insertTab` arrive here as two rehydrates, in two tasks
// (lib/storage/sync.ts). In between, a tab that HAS a workspace looks like one that has none — and adopting it
// then would write this window's not-yet-rehydrated workspaces over the other window's, moving its new tab
// into `Unsorted` for every window. So a change only arms a timer; the look that counts happens
// `ADOPTION_SETTLE_MS` later, on whatever the stores hold by then. (Start is the exception: memory was read
// from storage a moment ago, and the first render should already see the invariant.)
//
// ONLY A SETTLED WORLD. While another window is half-way through a profile switch, this one holds the tabs of
// one world and the workspaces of another (lib/profile/master-world.ts): every tab looks ownerless, and
// adopting them would put a whole profile's tabs into the wrong profile's `Unsorted`, for good. Unsettled →
// nothing is written; the store change that settles the world arms the timer again (hence the third
// subscription). For a user who never made a local profile the world is always settled: the three epochs are
// 0, both ids `master`, the fence absent.
//
// NO LOOP. An adoption writes the workspace store, which calls the subscriber again — which finds nothing to
// do and arms nothing. The timer exists only while there is something to do.
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabStore } from '../../../stores/useTabStore'
import { readMasterWorld } from '../../../lib/profile/master-world'
import { adoptStandaloneTabs } from '../../../lib/profile/sections'
import { UNSORTED_WORKSPACE_ID, useWorkspaceStore } from '../store'

/** How long the tab world must have been left alone before a tab without a workspace is believed to be one. */
export const ADOPTION_SETTLE_MS = 500

/**
 * Anything to do? Cheap enough for every store change: one pass over the workspaces, one over the tabs. The same
 * question `adoptStandaloneTabs` asks (a tab `tabOrder` does not mention counts, too), so "yes" means a write.
 */
function needsWork(): boolean {
  const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
  if (activeWorkspaceId === null && workspaces.length > 0) return true
  const owned = new Set(workspaces.flatMap((ws) => ws.tabs))
  return Object.keys(useTabStore.getState().tabs).some((id) => !owned.has(id))
}

function reconcile(): void {
  if (!needsWork() || !readMasterWorld().settled) return
  const { tabs, tabOrder, activeTabId } = useTabStore.getState()
  const current = useWorkspaceStore.getState()
  const { workspaces, adopted } = adoptStandaloneTabs(
    { workspaces: current.workspaces, tabs, tabOrder },
    { unsortedName: useI18nStore.getState().t('workspace.unsorted'), newWorkspaceId: UNSORTED_WORKSPACE_ID },
  )
  // "Home" is gone: the workspace of the tab on screen, else the first one.
  const activeWorkspaceId = current.activeWorkspaceId
    ?? (workspaces.find((ws) => activeTabId !== null && ws.tabs.includes(activeTabId)) ?? workspaces[0])?.id
    ?? null
  // Cannot happen while `needsWork` and `adoptStandaloneTabs` agree; if they ever stop, this is what keeps a
  // write that changes nothing from arming the next timer, for ever.
  if (adopted.length === 0 && activeWorkspaceId === current.activeWorkspaceId) return

  useWorkspaceStore.setState(adopted.length === 0 ? { activeWorkspaceId } : { workspaces, activeWorkspaceId })
  if (adopted.length > 0) console.info(`[workspace] ${adopted.length} tab(s) had no workspace; moved into "${UNSORTED_WORKSPACE_ID}"`)
}

/** Installs the invariant for the lifetime of the app (main.tsx, before the first render). Returns how to stop it. */
export function startStandaloneAdoption(): () => void {
  let timer: ReturnType<typeof setTimeout> | null = null
  const onChange = (): void => {
    if (timer !== null || !needsWork()) return
    timer = setTimeout(() => {
      timer = null
      reconcile()
    }, ADOPTION_SETTLE_MS)
  }

  reconcile()
  const unsubscribe = [useTabStore.subscribe(onChange), useWorkspaceStore.subscribe(onChange), useLocalProfilesStore.subscribe(onChange)]
  return () => {
    for (const off of unsubscribe) off()
    if (timer !== null) clearTimeout(timer)
    timer = null
  }
}
