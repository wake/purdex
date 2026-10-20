// spa/src/lib/task-groups/selection.ts — the selection follows the active tab (task groups spec §6.2). One subscriber on the tab store; on every change of the active tab, WHATEVER caused it (a click, a notification, search,
// the panel, a key, a rehydrate from another window, an apply that removed the active tab), it records, in the view in force (`effectiveView`):
//   - task-group view: `selectedGroup` becomes `groupOfTab` of the new active tab, that group's last active tab becomes it, and the view's last group follows;
//   - workspace view: the view's last workspace becomes the tab's workspace.
// A watcher-area active tab and a change to no active tab record nothing (the watcher area shows instead of the bar).
// `shouldResync` is the ONE place that says which tab-store changes make the selection re-read: today only the active tab. (When the active tab's own group changes — a tab moved from the panel — should re-select the group, this
// is the line that grows.)
import { useTabStore } from '../../stores/useTabStore'
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { isWatchTabId } from '../watch/ids'
import { effectiveView } from './effective-view'
import { currentPlacementCtx } from './current'
import { groupOfTab } from './placement'

type TabState = ReturnType<typeof useTabStore.getState>

/** Whether a tab-store change makes the selection re-read. */
export function shouldResync(state: TabState, prev: TabState): boolean {
  return state.activeTabId !== prev.activeTabId
}

/** Records the selection for the tab that is active now. Safe to call at any time; records nothing when there is nothing to record. */
export function resyncSelection(): void {
  const { activeTabId, tabs } = useTabStore.getState()
  if (activeTabId === null || isWatchTabId(activeTabId)) return
  const tab = tabs[activeTabId]
  if (tab === undefined) return
  const view = useGroupViewStore.getState()
  if (effectiveView() === 'groups') {
    const key = groupOfTab(tab, currentPlacementCtx())
    if (key !== null) view.recordGroupActive(key, activeTabId)
    return
  }
  const workspace = useWorkspaceStore.getState().workspaces.find((w) => w.tabs.includes(activeTabId))
  if (workspace !== undefined) view.recordWorkspace(workspace.id)
}

/** Starts the subscriber (`main.tsx`); the returned function stops it. */
export function startGroupSelection(): () => void {
  resyncSelection()
  return useTabStore.subscribe((state, prev) => {
    if (shouldResync(state, prev)) resyncSelection()
  })
}
