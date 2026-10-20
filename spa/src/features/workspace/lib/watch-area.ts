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
import { WATCH_EVENTS_TAB_ID, WATCH_GUARDIAN_TAB_ID, WATCH_JOBS_TAB_ID, WATCH_WORKSPACE_ID, hostIdOfTabId, isWatchTabId, watchHostTabId } from '../../../lib/watch/ids'
import { useWatchStatusStore, deriveGuardian } from '../../../lib/watch/status-store'
import { anyHostDeclaresWatch } from '../../../lib/watch/events-store'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { jobsDaemons } from '../../../lib/pool/jobs-driver-live'
import { useSessionStore } from '../../../stores/useSessionStore'
import { tmuxSessionContent } from '../../../lib/open-session-tab'

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
  // If it was the active tab, a neighbour in the same row takes over (as closing a tab does), not nothing.
  let neighbour: string | null = null
  if (owner) {
    const i = owner.tabs.indexOf(tabId)
    neighbour = owner.tabs[i + 1] ?? owner.tabs[i - 1] ?? null
    ws.removeTabFromWorkspace(owner.id, tabId)
  }
  const wasActive = useTabStore.getState().activeTabId === tabId
  useTabStore.setState((s) => {
    if (!(tabId in s.tabs)) return s
    const { [tabId]: _gone, ...tabs } = s.tabs
    return {
      tabs,
      tabOrder: s.tabOrder.filter((id) => id !== tabId),
      visitHistory: s.visitHistory.filter((id) => id !== tabId),
      activeTabId: s.activeTabId === tabId ? (neighbour !== null && neighbour in s.tabs ? neighbour : null) : s.activeTabId,
    }
  })
  if (owner && wasActive && neighbour !== null) useWorkspaceStore.getState().setWorkspaceActiveTab(owner.id, neighbour)
}

const GUARDIAN_PANEL: PaneContent = { kind: 'watch-guardian' }
const EVENTS_PANE: PaneContent = { kind: 'watch-events' }
const JOBS_PANE: PaneContent = { kind: 'watch-jobs' }

interface GuardianWish {
  wanted: PaneContent
  /** True when turning a session into the panel is backed by evidence that does not depend on this device's reach. */
  downgradeOk: boolean
}

/**
 * What the guardian pane should hold now (plan §1, T-a3.4), or null = write nothing: the derivation is not settled (no
 * answer yet, a connected host that has not answered since it connected, the holder's session list not loaded).
 * Session → panel (`downgradeOk`) only when the evidence is the same on every device: `none` from an `ok` host, or the
 * holder reachable, its list loaded, and no such session. Not for an unreachable holder, a failed read or a forgotten host.
 */
function guardianWish(): GuardianWish | null {
  const byHost = useWatchStatusStore.getState().byHost
  const host = useHostStore.getState()
  const connected = Object.keys(host.hosts).filter((id) => host.runtime[id]?.status === 'connected')
  if (connected.length === 0 || connected.some((id) => !(id in byHost))) return null
  const g = deriveGuardian(byHost)
  if (g.state === 'unknown') return null
  if ((g.state === 'awake' || g.state === 'resting') && g.reachable && g.appHostId) {
    const all = useSessionStore.getState().sessions
    if (!Object.hasOwn(all, g.appHostId)) return null // `sessions[host]` exists only once a fetch landed: loaded
    const found = all[g.appHostId].find((x) => x.name === g.tmuxSession)
    if (found) return { wanted: tmuxSessionContent(g.appHostId, found), downgradeOk: false }
    return { wanted: GUARDIAN_PANEL, downgradeOk: true }
  }
  return { wanted: GUARDIAN_PANEL, downgradeOk: g.state === 'none' }
}

const contentKey = (c: PaneContent): string =>
  c.kind === 'tmux-session' ? `${c.hostId}\0${c.sessionCode}\0${c.tmuxInstance}` : 'panel'

export interface WatchReconciler {
  /** One pass. Writes only on a difference. Returns when the next pass is due for a pending removal, else null. */
  reconcile: (view: WatchFleetView | null, now?: number) => number | null
}

/** One reconciler = one device's memory of which hosts it has seen. Tests make two to stand for two devices. */
export function createWatchReconciler(): WatchReconciler {
  /** fleet host id → the last time a non-stale snapshot listed it. In memory only: a restart forgets, and forgets safely. */
  const lastListed = new Map<string, number>()
  /** The derivation behind the guardian pane's content, as this device last wrote it. Edge-triggered: see `reconcileGuardian`. */
  let guardianKey: string | undefined

  /**
   * The guardian tab: made first when it is missing, and its pane content set when THIS device's derivation changes — not on
   * every run. A content that arrived by sync from another device stays until this device's own derivation changes; both
   * devices read the same daemons, so they converge without ping-pong.
   */
  const reconcileGuardian = (): void => {
    const tabs = useTabStore.getState().tabs
    const area = () => useWorkspaceStore.getState().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!
    const made = !(WATCH_GUARDIAN_TAB_ID in tabs)
    if (made) useTabStore.getState().addTab(hostTab(WATCH_GUARDIAN_TAB_ID, GUARDIAN_PANEL))
    if (!area().tabs.includes(WATCH_GUARDIAN_TAB_ID)) {
      useWorkspaceStore.getState().addTabToWorkspace(WATCH_WORKSPACE_ID, WATCH_GUARDIAN_TAB_ID)
      // First of the row when it is created; after that the reader's order stands.
      const rest = area().tabs.filter((id) => id !== WATCH_GUARDIAN_TAB_ID)
      useWorkspaceStore.getState().reorderWorkspaceTabs(WATCH_WORKSPACE_ID, [WATCH_GUARDIAN_TAB_ID, ...rest])
    }

    const wish = guardianWish()
    if (wish === null) return
    const key = contentKey(wish.wanted)
    if (key === guardianKey) return
    const tab = useTabStore.getState().tabs[WATCH_GUARDIAN_TAB_ID]
    if (!tab || tab.layout.type !== 'leaf') return // not remembered: the next run retries
    const cur = tab.layout.pane.content
    if (JSON.stringify(cur) === JSON.stringify(wish.wanted)) { guardianKey = key; return }
    if (wish.wanted.kind === 'watch-guardian' && !wish.downgradeOk) return // this device cannot tell; leave the session
    useTabStore.getState().setPaneContent(WATCH_GUARDIAN_TAB_ID, tab.layout.pane.id, wish.wanted)
    guardianKey = key
  }

  /**
   * The events tab (spec §5): made, right after the guardian tab, once any connected host carries the watch module. Only
   * created, never removed — a host that goes away for a moment must not take a tab the reader may have moved or synced.
   */
  const reconcileEvents = (): void => {
    const exists = WATCH_EVENTS_TAB_ID in useTabStore.getState().tabs
    if (!exists && !anyHostDeclaresWatch(useWatchStatusStore.getState().byHost)) return
    if (!exists) useTabStore.getState().addTab(hostTab(WATCH_EVENTS_TAB_ID, EVENTS_PANE))
    const area = () => useWorkspaceStore.getState().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!
    if (!area().tabs.includes(WATCH_EVENTS_TAB_ID)) {
      useWorkspaceStore.getState().addTabToWorkspace(WATCH_WORKSPACE_ID, WATCH_EVENTS_TAB_ID)
      // Right after the guardian tab when it is created; after that the reader's order stands.
      const rest = area().tabs.filter((id) => id !== WATCH_EVENTS_TAB_ID && id !== WATCH_GUARDIAN_TAB_ID)
      const head = area().tabs.includes(WATCH_GUARDIAN_TAB_ID) ? [WATCH_GUARDIAN_TAB_ID] : []
      useWorkspaceStore.getState().reorderWorkspaceTabs(WATCH_WORKSPACE_ID, [...head, WATCH_EVENTS_TAB_ID, ...rest])
    }
  }

  /**
   * The jobs tab (WWD-7b1): made, right after the events tab (else the guardian tab), once a connected daemon announces `jobs.v1`.
   * Only created, never removed — a daemon that drops for a moment must not take a tab the reader may have moved or synced.
   */
  const reconcileJobs = (): void => {
    const exists = WATCH_JOBS_TAB_ID in useTabStore.getState().tabs
    if (!exists && jobsDaemons().length === 0) return
    if (!exists) useTabStore.getState().addTab(hostTab(WATCH_JOBS_TAB_ID, JOBS_PANE))
    const area = () => useWorkspaceStore.getState().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!
    if (!area().tabs.includes(WATCH_JOBS_TAB_ID)) {
      useWorkspaceStore.getState().addTabToWorkspace(WATCH_WORKSPACE_ID, WATCH_JOBS_TAB_ID)
      // After the guardian and events tabs when it is created; after that the reader's order stands.
      const rest = area().tabs.filter((id) => id !== WATCH_JOBS_TAB_ID && id !== WATCH_GUARDIAN_TAB_ID && id !== WATCH_EVENTS_TAB_ID)
      const head = [WATCH_GUARDIAN_TAB_ID, WATCH_EVENTS_TAB_ID].filter((id) => area().tabs.includes(id))
      useWorkspaceStore.getState().reorderWorkspaceTabs(WATCH_WORKSPACE_ID, [...head, WATCH_JOBS_TAB_ID, ...rest])
    }
  }

  const reconcile = (view: WatchFleetView | null, now: number = Date.now()): number | null => {
    const ws = useWorkspaceStore.getState()
    const watch = ws.workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)

    // No workspace at all is a store that has not loaded, not a removed area: nothing is swept then.
    if (!watch && ws.workspaces.length === 0) return null

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
    if (!watch) {
      // Tabs of the removed area that no workspace lists (adoption has not run yet) go as well.
      for (const id of Object.keys(useTabStore.getState().tabs)) if (isWatchTabId(id)) dropTab(id)
      return null
    }

    // 2. Non-watch tabs found in the area (another window's rehydrate) go to the return workspace.
    const returnTo = getReturnWorkspaceId()
    for (const id of useWorkspaceStore.getState().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!.tabs) {
      if (!isWatchTabId(id) && returnTo) useWorkspaceStore.getState().insertTab(id, returnTo)
    }

    // 2b. The guardian, events and jobs tabs, with or without a fleet view.
    reconcileGuardian()
    reconcileEvents()
    reconcileJobs()

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
        const hostId = hostIdOfTabId(tabId)
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

/**
 * Selecting any other session tab leaves the area (spec §3): while `watch` is the active workspace and the active tab
 * belongs to another one — a session opened, a terminal link, ⌘T (whose tab `insertTab` sent to the return workspace) —
 * that workspace becomes the active one. An area tab, or a tab no workspace owns yet, changes nothing.
 */
export function leaveAreaForForeignActiveTab(): void {
  const ws = useWorkspaceStore.getState()
  if (ws.activeWorkspaceId !== WATCH_WORKSPACE_ID) return
  const tabId = useTabStore.getState().activeTabId
  if (tabId === null || isWatchTabId(tabId)) return
  const owner = ws.findWorkspaceByTab(tabId)
  if (owner && owner.id !== WATCH_WORKSPACE_ID) ws.setActiveWorkspace(owner.id)
}

/** The reconciler `startWatchArea` runs (and `openWatchArea` borrows, so both share one device's memory of what it saw). */
let current: WatchReconciler = createWatchReconciler()
export const currentWatchReconciler = (): WatchReconciler => current

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
  current = reconciler
  let timer: ReturnType<typeof setTimeout> | null = null
  let running = false

  const run = (): void => {
    if (running) return // our own writes re-enter through the subscriptions
    if (!readMasterWorld().settled) return
    // No area (a sync apply or a profile switch removed it): it stays removed, and the tabs it left behind are swept.
    const hasArea = useWorkspaceStore.getState().workspaces.some((w) => w.id === WATCH_WORKSPACE_ID)
    if (hasArea) leaveAreaForForeignActiveTab()
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

  const unsubscribe = [
    useFleetStore.subscribe(run), useWorkspaceStore.subscribe(run), useTabStore.subscribe(run),
    useWatchStatusStore.subscribe(run), useSessionStore.subscribe(run), // the guardian pane follows where the watcher is
    useNexHostStore.subscribe(run), useHostStore.subscribe(run), // the jobs tab appears once a connected daemon announces `jobs.v1`
  ]
  run()
  return () => {
    for (const off of unsubscribe) off()
    if (timer !== null) clearTimeout(timer)
    timer = null
    if (current === reconciler) current = createWatchReconciler()
  }
}
