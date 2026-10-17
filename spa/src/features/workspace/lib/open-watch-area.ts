// Opens the watcher area (守門員專區) on a tab, and leaves it. The entry points are the host cells (W5a1) and the shield
// (W5a3); the way back is the area's back button and ⌘W (journal v5 §3.7).
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore, getReturnWorkspaceId, getWatchReturnTarget, setWatchReturnTarget } from '../store'
import { WATCH_EVENTS_TAB_ID, WATCH_GUARDIAN_TAB_ID, WATCH_WORKSPACE_ID, watchHostTabId } from '../../../lib/watch/ids'
import { readMasterWorld } from '../../../lib/profile/master-world'
import { currentWatchReconciler, readFleetView } from './watch-area'

export type WatchTarget = { tab: 'guardian' | 'events' } | { host: string }

/**
 * Ensures the area's workspace, brings its host tabs up to the fleet's host list, then selects the target the way
 * `handleSelectTab` does (active tab, active workspace, the workspace's own active tab). Entered from outside, the
 * workspace and tab on screen become the way back (`leaveWatchArea`); from inside the area that stays as it was. A target
 * with no tab — an unknown host, a guardian or events tab that does not exist yet — selects the area's first tab.
 */
export function openWatchArea(target: WatchTarget): void {
  // A world that is switching or applying is not ours to write into (the same gate as `startWatchArea`).
  if (!readMasterWorld().settled) return
  const from = useWorkspaceStore.getState().activeWorkspaceId
  if (from !== WATCH_WORKSPACE_ID) {
    setWatchReturnTarget(from === null ? null : { workspaceId: from, tabId: useTabStore.getState().activeTabId })
  }
  useWorkspaceStore.getState().ensureWatchWorkspace()
  currentWatchReconciler().reconcile(readFleetView())

  const area = useWorkspaceStore.getState().workspaces.find((w) => w.id === WATCH_WORKSPACE_ID)!
  const wanted = 'host' in target ? watchHostTabId(target.host) : target.tab === 'events' ? WATCH_EVENTS_TAB_ID : WATCH_GUARDIAN_TAB_ID
  const tabs = useTabStore.getState().tabs
  const live = area.tabs.filter((id) => id in tabs)
  const tabId = live.includes(wanted) ? wanted : (live[0] ?? null)

  useTabStore.getState().setActiveTab(tabId)
  useWorkspaceStore.getState().setActiveWorkspace(WATCH_WORKSPACE_ID)
  if (tabId !== null) useWorkspaceStore.getState().setWorkspaceActiveTab(WATCH_WORKSPACE_ID, tabId)
}

/**
 * Leaves the area for the workspace and the tab it was entered from (the back button and ⌘W). That tab gone from that
 * workspace (closed, or moved away) → the workspace's own active tab. That workspace gone, or no way back taken (a
 * restart with the area shown) → the return workspace (`getReturnWorkspaceId`) and its active tab. The area's tabs stay.
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
