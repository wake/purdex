// Opens the watcher area (守門員專區) on a tab. The entry points are the host cells (W5a1) and the shield (W5a3).
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../store'
import { WATCH_EVENTS_TAB_ID, WATCH_GUARDIAN_TAB_ID, WATCH_WORKSPACE_ID, watchHostTabId } from '../../../lib/watch/ids'
import { readMasterWorld } from '../../../lib/profile/master-world'
import { currentWatchReconciler, readFleetView } from './watch-area'

export type WatchTarget = { tab: 'guardian' | 'events' } | { host: string }

/**
 * Ensures the area's workspace, brings its host tabs up to the fleet's host list, then selects the target the way
 * `handleSelectTab` does (active tab, active workspace, the workspace's own active tab). The workspace the user came
 * from is remembered by the store itself (the last non-watch active workspace), so entering from elsewhere needs
 * nothing more here. A target with no tab — an unknown host, a guardian or events tab that does not exist yet — selects the
 * area's first tab.
 */
export function openWatchArea(target: WatchTarget): void {
  // A world that is switching or applying is not ours to write into (the same gate as `startWatchArea`).
  if (!readMasterWorld().settled) return
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
