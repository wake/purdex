// The watcher area's tabs (守門員專區), kept in step with the fleet's host list. Spec §3, plan §1 "Host tabs".
//
// The area is a real, syncable workspace (`WATCH_WORKSPACE_ID`) with fixed tab ids, so two devices that each create a
// tab for the same host make the SAME tab and a sync apply converges instead of duplicating. The rule that keeps it
// from ping-ponging: a tab is APPENDED for every host this device lists, but REMOVED only for a host this device saw
// leave — listed in a non-stale snapshot, then absent for ABSENT_REMOVE_MS. A tab that arrives by sync for a host this
// device never listed stays (its pane says it cannot read that host).
import type { Tab, PaneContent } from '../../../types/tab'
import { useTabStore } from '../../../stores/useTabStore'
import { useFleetStore } from '../../../lib/fleet/store'
import { readMasterWorld } from '../../../lib/profile/master-world'
import { useWorkspaceStore, getReturnWorkspaceId } from '../store'
import { WATCH_WORKSPACE_ID, fleetHostIdOfTabId, isWatchTabId, watchHostTabId } from '../../../lib/watch/ids'

/** A host that this device listed and then did not list for this long loses its tab. */
export const ABSENT_REMOVE_MS = 60_000

/** What the reconcile needs of the fleet: the host ids of the current snapshot, and whether it is stale. */
export interface WatchFleetView {
  hostIds: readonly string[]
  stale: boolean
}

function hostTab(id: string, content: PaneContent): Tab {
  // `locked`: every close path already refuses a locked tab. The pane id derives from the tab id, so two devices agree.
  return { id, pinned: false, locked: true, createdAt: Date.now(), layout: { type: 'leaf', pane: { id: `${id}.p`, content } } }
}

/** Deletes a tab from every workspace and from the tab store; `closeTab` would refuse, the tab being locked. */
function dropTab(tabId: string): void {
  const ws = useWorkspaceStore.getState()
  const owner = ws.findWorkspaceByTab(tabId)
  if (owner) ws.removeTabFromWorkspace(owner.id, tabId)
  useTabStore.setState((s) => {
    if (!(tabId in s.tabs)) return s
    const { [tabId]: _gone, ...tabs } = s.tabs
    return {
      tabs,
      tabOrder: s.tabOrder.filter((id) => id !== tabId),
      visitHistory: s.visitHistory.filter((id) => id !== tabId),
      activeTabId: s.activeTabId === tabId ? null : s.activeTabId,
    }
  })
}

export interface WatchReconciler {
  /** One pass. Writes only on a difference. Returns when the next pass is due for a pending removal, else null. */
  reconcile: (view: WatchFleetView | null, now?: number) => number | null
}

/** One reconciler = one device's memory of which hosts it has seen. Tests make two to stand for two devices. */
export function createWatchReconciler(): WatchReconciler {
  /** fleet host id → the last time a non-stale snapshot listed it. In memory only: a restart forgets, and forgets safely. */
  const lastListed = new Map<string, number>()

  const reconcile = (view: WatchFleetView | null, now: number = Date.now()): number | null => {
    const ws = useWorkspaceStore.getState()
    const watch = ws.workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)

    // 1. Area tabs found outside the area move back; with no area at all they are strays and go (an apply or a profile
    //    switch removed it, and it stays removed until the next open).
    for (const w of ws.workspaces) {
      if (w.id === WATCH_WORKSPACE_ID) continue
      for (const id of w.tabs.filter(isWatchTabId)) {
        if (!watch) dropTab(id)
        else if (id in useTabStore.getState().tabs) {
          useWorkspaceStore.getState().removeTabFromWorkspace(w.id, id)
          useWorkspaceStore.getState().addTabToWorkspace(WATCH_WORKSPACE_ID, id)
        }
      }
    }
    if (!watch) return null

    // 2. Non-watch tabs found in the area (another window's rehydrate) go to the return workspace.
    const returnTo = getReturnWorkspaceId()
    for (const id of useWorkspaceStore.getState().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!.tabs) {
      if (!isWatchTabId(id) && returnTo) useWorkspaceStore.getState().insertTab(id, returnTo)
    }

    // 3. Host tabs, only with a fleet view; without one they are left as they are.
    if (!view) return null
    if (!view.stale) for (const id of view.hostIds) lastListed.set(id, now)

    const listed = new Set(view.hostIds)
    for (const id of view.hostIds) {
      const tabId = watchHostTabId(id)
      const tabs = useTabStore.getState().tabs
      if (!(tabId in tabs)) useTabStore.getState().addTab(hostTab(tabId, { kind: 'watch-host', fleetHostId: id }))
      const inArea = useWorkspaceStore.getState().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!.tabs.includes(tabId)
      if (!inArea) useWorkspaceStore.getState().addTabToWorkspace(WATCH_WORKSPACE_ID, tabId) // `add` rewrites even a no-op
    }

    let next: number | null = null
    if (!view.stale) {
      const current = useWorkspaceStore.getState().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!
      for (const tabId of current.tabs) {
        const hostId = fleetHostIdOfTabId(tabId)
        if (hostId === null || listed.has(hostId)) continue
        const seenAt = lastListed.get(hostId)
        if (seenAt === undefined) continue // never listed on this device: it came by sync, and stays
        const due = seenAt + ABSENT_REMOVE_MS
        if (now >= due) {
          dropTab(tabId)
          lastListed.delete(hostId)
        } else next = next === null ? due : Math.min(next, due)
      }
    }
    return next
  }

  return { reconcile }
}

/** The fleet store's view for the reconcile: no snapshot → null; no current primary counts as stale. */
export function readFleetView(): WatchFleetView | null {
  const s = useFleetStore.getState()
  if (!s.snapshot) return null
  return { hostIds: s.snapshot.order, stale: s.stale || s.primaryHostId === null }
}

/**
 * Installs the reconcile for the lifetime of the app (main.tsx), beside `startStandaloneAdoption`. It runs on fleet,
 * workspace and tab changes, only while the area's workspace exists and the world is settled (a profile switch or an
 * apply in flight is not ours to touch), and a pass that finds nothing to do writes nothing. Returns how to stop it.
 */
export function startWatchArea(): () => void {
  const reconciler = createWatchReconciler()
  let timer: ReturnType<typeof setTimeout> | null = null
  let running = false

  const run = (): void => {
    if (running) return // our own writes re-enter through the subscriptions
    if (!useWorkspaceStore.getState().workspaces.some((w) => w.id === WATCH_WORKSPACE_ID)) return
    if (!readMasterWorld().settled) return
    running = true
    let next: number | null = null
    try {
      next = reconciler.reconcile(readFleetView())
    } finally {
      running = false
    }
    if (timer !== null) clearTimeout(timer)
    timer = next === null ? null : setTimeout(run, Math.max(0, next - Date.now()))
  }

  const unsubscribe = [useFleetStore.subscribe(run), useWorkspaceStore.subscribe(run), useTabStore.subscribe(run)]
  run()
  return () => {
    for (const off of unsubscribe) off()
    if (timer !== null) clearTimeout(timer)
    timer = null
  }
}
