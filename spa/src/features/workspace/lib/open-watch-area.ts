// Opens the watcher area (守門員專區) on a tab, and leaves it. The entry points are the host cells (W5a1) and the shield
// (W5a3); the way back is the area's back button and ⌘W (journal v5 §3.7).
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore, getReturnWorkspaceId, getWatchReturnTarget } from '../store'
import { WATCH_EVENTS_TAB_ID, WATCH_GUARDIAN_TAB_ID, WATCH_JOBS_TAB_ID, WATCH_WORKSPACE_ID, watchHostTabId } from '../../../lib/watch/ids'
import { readMasterWorld } from '../../../lib/profile/master-world'
import { writeHostTab, type HostSubTab } from '../../../lib/watch/view-memory'
import { currentWatchReconciler, readPoolView } from './watch-area'

/** `poolHost`: a pool host by its wwd `host_id` (its tab id), with the page of the host to show. `alias` is not read (kept so callers that have it need no change). */
export type WatchTarget = { tab: 'guardian' | 'events' | 'jobs' } | { poolHost: { hostId: string; alias?: string }; sub?: HostSubTab }

/**
 * Ensures the area's workspace, brings its host tabs up to the pool's host list, then selects the target the way
 * `handleSelectTab` does (active tab, active workspace, the workspace's own active tab). Entering from outside takes
 * the way back (`leaveWatchArea`) through the store's own entry rule (`watchReturnTarget`, store.ts), as a synced entry
 * does; from inside the area it stays as it was. A target with no tab — an unknown host, a guardian or events tab that
 * does not exist yet — selects the area's first tab.
 */
export function openWatchArea(target: WatchTarget): void {
  // A world that is switching or applying is not ours to write into (the same gate as `startWatchArea`).
  if (!readMasterWorld().settled) return
  useWorkspaceStore.getState().ensureWatchWorkspace()
  currentWatchReconciler().reconcile(readPoolView())

  const area = useWorkspaceStore.getState().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!
  if ('poolHost' in target && target.sub) writeHostTab(target.poolHost.hostId, target.sub)
  const wanted = 'poolHost' in target
    ? watchHostTabId(target.poolHost.hostId)
    : 'tab' in target && target.tab === 'events' ? WATCH_EVENTS_TAB_ID : 'tab' in target && target.tab === 'jobs' ? WATCH_JOBS_TAB_ID : WATCH_GUARDIAN_TAB_ID
  const tabs = useTabStore.getState().tabs
  const live = area.tabs.filter((id) => id in tabs)
  const tabId = wanted !== null && live.includes(wanted) ? wanted : (live[0] ?? null)

  useTabStore.getState().setActiveTab(tabId)
  useWorkspaceStore.getState().setActiveWorkspace(WATCH_WORKSPACE_ID)
  if (tabId !== null) useWorkspaceStore.getState().setWorkspaceActiveTab(WATCH_WORKSPACE_ID, tabId)
}

/**
 * Leaves the area for the workspace and the tab it was entered from (the back button and ⌘W). That tab gone from that
 * workspace (closed, or moved away) → the workspace's own active tab. That workspace not among the workspaces now
 * (removed, or another world, whose ids are its own), or no way back taken (a restart with the area shown) → the return
 * workspace (`getReturnWorkspaceId`) and its active tab. The area's tabs stay.
 */
export function leaveWatchArea(): void {
  const target = getWatchReturnTarget()
  const remembered = target === null
    ? undefined
    : useWorkspaceStore.getState().workspaces.find((w) => w.id === target.workspaceId && w.id !== WATCH_WORKSPACE_ID)
  const wsId = remembered?.id ?? getReturnWorkspaceId() ?? useWorkspaceStore.getState().ensureUnsortedWorkspace().id
  const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!
  const tabs = useTabStore.getState().tabs
  const shown = (id: string | null | undefined): id is string => !!id && id in tabs && ws.tabs.includes(id)
  const back = remembered ? target?.tabId : undefined
  const tabId = shown(back) ? back : shown(ws.activeTabId) ? ws.activeTabId : (ws.tabs.find((id) => id in tabs) ?? null)

  useTabStore.getState().setActiveTab(tabId)
  useWorkspaceStore.getState().setActiveWorkspace(wsId)
  if (tabId !== null) useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, tabId)
}
