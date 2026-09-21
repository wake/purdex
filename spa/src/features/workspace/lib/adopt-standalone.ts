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
// EXACTLY ONE is also NOT TWO: old storage, a snapshot restore and cross-window last-write-wins can list one tab
// in two workspaces (the tab bar renders it in both; `tabOrder` has it once). The FIRST workspace in workspace
// order keeps it — a rule with no choice in it, so two windows that each apply it get the same world — and a
// workspace that loses its active tab gets `activeTabId: null`, which is what `removeTabFromWorkspace` and
// `insertTab`'s dedup do. (A `workspace.tabs` id that is in no tab store is not this file's business.)
//
// NOT IN THE SAME TICK, AND NOT AT START: A DEBOUNCE. The tab store and the workspace store are two stores: a
// restore writes one and then the other, and another window's `addTab` + `insertTab` arrive here as two
// rehydrates, in two tasks (lib/storage/sync.ts). In between, a tab that HAS a workspace looks like one that has
// none — and adopting it then would write this window's not-yet-rehydrated workspaces over the other window's
// (zustand persists the whole store per write; that is not solved here). So a change only (re-)arms a timer, and
// the look happens `ADOPTION_SETTLE_MS` after the LAST change — a throttle would fire 10 ms after a rehydrate
// that came late in the wait. The look reads the stores as they are THEN, never a copy from when it was armed.
// Start is no exception: what storage held a moment ago may be the first half of another window's write, so the
// first look waits like any other (a pre-P3c device shows its ownerless tabs in no workspace for that long).
// Re-pointing `activeWorkspaceId` waits, too — it is a write of the same whole store.
//   ONLY MEMBERSHIP PUSHES IT BACK. A busy agent rewrites pane records in the tab store several times a second
// (useAgentStore → `setPaneRebuild`), in every window, and each of those is a rehydrate in the others. None of
// that changes who owns what, so none of it re-arms the timer: the look cannot be starved, and needs no cap.
//
// ONLY A SETTLED WORLD. While another window is half-way through a profile switch, this one holds the tabs of
// one world and the workspaces of another (lib/profile/master-world.ts): every tab looks ownerless, and
// adopting them would put a whole profile's tabs into the wrong profile's `Unsorted`, for good. Unsettled →
// nothing is written; the store change that settles the world arms the timer again (hence the third
// subscription). For a user who never made a local profile the world is always settled: the three epochs are
// 0, both ids `master`, the fence absent.
//
// NO LOOP. A write here changes the workspace store, which calls the subscriber again — which finds nothing to
// do and arms nothing. The timer exists only while there is something to do.
import { useLocalProfilesStore } from '../../../stores/useLocalProfilesStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabStore } from '../../../stores/useTabStore'
import { readMasterWorld } from '../../../lib/profile/master-world'
import { adoptStandaloneTabs } from '../../../lib/profile/sections'
import type { Workspace } from '../../../types/tab'
import { UNSORTED_WORKSPACE_ID, useWorkspaceStore } from '../store'

/** How long the tab world must have been left alone before a tab without a workspace is believed to be one. */
export const ADOPTION_SETTLE_MS = 500

/** What a look depends on, as a string: who owns which tab, which tabs exist, the pointer, and the three world tags. */
function signature(): string {
  const tab = useTabStore.getState()
  const ws = useWorkspaceStore.getState()
  const local = useLocalProfilesStore.getState()
  return JSON.stringify([
    Object.keys(tab.tabs), ws.workspaces.map((w) => [w.id, w.tabs]), ws.activeWorkspaceId,
    tab.worldId, tab.worldEpoch, ws.worldId, ws.worldEpoch, local.activeProfileId, local.worldEpoch,
  ])
}

/**
 * Anything to do? One pass over the workspaces, one over the tabs. The same questions `reconcile` asks (a tab
 * `tabOrder` does not mention counts, too), so "yes" means a write.
 */
function needsWork(): boolean {
  const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
  if (activeWorkspaceId === null && workspaces.length > 0) return true
  const owned = new Set<string>()
  for (const ws of workspaces) {
    for (const id of ws.tabs) {
      if (owned.has(id)) return true
      owned.add(id)
    }
  }
  return Object.keys(useTabStore.getState().tabs).some((id) => !owned.has(id))
}

/** Every tab id once: the first workspace (in workspace order) that lists it keeps it, at its first position. */
function dropExtraOwners(workspaces: readonly Workspace[]): { workspaces: readonly Workspace[]; dropped: number } {
  const seen = new Set<string>()
  let dropped = 0
  const next = workspaces.map((ws) => {
    const tabs: string[] = []
    for (const id of ws.tabs) {
      if (seen.has(id)) continue
      seen.add(id)
      tabs.push(id)
    }
    if (tabs.length === ws.tabs.length) return ws
    dropped += ws.tabs.length - tabs.length
    return { ...ws, tabs, activeTabId: ws.activeTabId !== null && tabs.includes(ws.activeTabId) ? ws.activeTabId : null }
  })
  return { workspaces: dropped === 0 ? workspaces : next, dropped }
}

/** One look, on what the stores hold NOW. */
function reconcile(): void {
  if (!needsWork() || !readMasterWorld().settled) return
  const { tabs, tabOrder, activeTabId } = useTabStore.getState()
  const current = useWorkspaceStore.getState()
  const deduped = dropExtraOwners(current.workspaces)
  const { workspaces, adopted } = adoptStandaloneTabs(
    { workspaces: deduped.workspaces, tabs, tabOrder },
    { unsortedName: useI18nStore.getState().t('workspace.unsorted'), newWorkspaceId: UNSORTED_WORKSPACE_ID },
  )
  // "Home" is gone: the workspace of the tab on screen, else the first one.
  const activeWorkspaceId = current.activeWorkspaceId
    ?? (workspaces.find((ws) => activeTabId !== null && ws.tabs.includes(activeTabId)) ?? workspaces[0])?.id
    ?? null
  const membership = adopted.length > 0 || deduped.dropped > 0
  // Cannot happen while `needsWork` and the two steps above agree; if they ever stop, this is what keeps a
  // write that changes nothing from arming the next timer, for ever.
  if (!membership && activeWorkspaceId === current.activeWorkspaceId) return

  useWorkspaceStore.setState(membership ? { workspaces, activeWorkspaceId } : { activeWorkspaceId })
  if (adopted.length > 0) console.info(`[workspace] ${adopted.length} tab(s) had no workspace; moved into "${UNSORTED_WORKSPACE_ID}"`)
  if (deduped.dropped > 0) console.info(`[workspace] ${deduped.dropped} tab listing(s) removed: a tab was in more than one workspace`)
}

/** Installs the invariant for the lifetime of the app (main.tsx). Returns how to stop it. */
export function startStandaloneAdoption(): () => void {
  let timer: ReturnType<typeof setTimeout> | null = null
  let last: string | null = null
  const onChange = (): void => {
    const now = signature()
    if (now === last) return
    last = now
    if (timer !== null) clearTimeout(timer)
    timer = needsWork()
      ? setTimeout(() => {
          timer = null
          reconcile()
        }, ADOPTION_SETTLE_MS)
      : null
  }

  onChange()
  const unsubscribe = [useTabStore.subscribe(onChange), useWorkspaceStore.subscribe(onChange), useLocalProfilesStore.subscribe(onChange)]
  return () => {
    for (const off of unsubscribe) off()
    if (timer !== null) clearTimeout(timer)
    timer = null
  }
}
