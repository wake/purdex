// spa/src/lib/task-groups/selection.ts — the selection follows the active tab (task groups spec §6.2). One subscriber on the tab store; on every change of the active tab, WHATEVER caused it (a click, a notification, search,
// the panel, a key, a rehydrate from another window, an apply that removed the active tab), it records, in the view in force (`effectiveView`):
//   - task-group view: `selectedGroup` becomes `groupOfTab` of the new active tab, that group's last active tab becomes it, and the view's last group follows;
//   - workspace view: the view's last workspace becomes the tab's workspace.
// A watcher-area active tab and a change to no active tab record nothing (the watcher area shows instead of the bar).
// `shouldResync` is the ONE place that says which tab-store changes make the selection re-read: the active tab. The active tab's own group changing under it is `startGroupSelection`'s second trigger (TG-5c).
import { useTabStore } from '../../stores/useTabStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useRootCacheStore } from './root-cache'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useHostStore } from '../../stores/useHostStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { isWatchTabId } from '../watch/ids'
import { effectiveView } from './effective-view'
import { currentPlacementCtx } from './current'
import { groupOfTab } from './placement'

/** How long the rosters and sessions must stay quiet before the active tab's group is re-read for a team change. */
export const TEAM_INDEX_SETTLE_MS = 250

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

/** The group of the active tab in the task-group view, or null (not that view, no tab, a watcher-area tab). Only read to tell whether it CHANGED under the same active tab. */
function activeGroupKey(): string | null {
  const { activeTabId, tabs } = useTabStore.getState()
  if (activeTabId === null || isWatchTabId(activeTabId) || effectiveView() !== 'groups') return null
  const tab = tabs[activeTabId]
  return tab === undefined ? null : groupOfTab(tab, currentPlacementCtx())
}

/**
 * Starts the subscriber (`main.tsx`); the returned function stops it. It re-reads the selection when the active tab changes AND when the active tab's own group changes under it (TG-5c, spec §6.2): a tab moved to another group from
 * the personal panel, taken out, started a group from, or whose root arrives late. Without it the tab would vanish from the bar it is in. The group moves through the group store (a member), the tab (`group`) or the root cache, so all three
 * are watched, and the group is only computed when one of them changed.
 */
export function startGroupSelection(): () => void {
  resyncSelection()
  let lastKey = activeGroupKey()
  let lastView = effectiveView()
  const regroup = () => {
    // A change of the view in force is the view toggle's business (§6.3 restores that view's last selection): only the baseline moves, nothing is recorded over the restore.
    if (effectiveView() !== lastView) {
      lastView = effectiveView()
      lastKey = activeGroupKey()
      return
    }
    const key = activeGroupKey()
    if (key === lastKey) return
    lastKey = key
    resyncSelection()
  }
  // The team index (a seat joining or leaving makes the tab a team's) follows the rosters and the sessions, which tick often: those two are looked at once the burst is over.
  let timer: ReturnType<typeof setTimeout> | null = null
  const regroupSoon = () => {
    if (timer !== null) clearTimeout(timer)
    timer = setTimeout(() => { timer = null; regroup() }, TEAM_INDEX_SETTLE_MS)
  }
  const unsubs = [
    useTabStore.subscribe((state, prev) => {
      if (shouldResync(state, prev)) {
        resyncSelection()
        lastKey = activeGroupKey()
      } else if (state.activeTabId !== null && state.tabs[state.activeTabId] !== prev.tabs[state.activeTabId]) regroup()
    }),
    useManualGroupsStore.subscribe((state, prev) => { if (state.groups !== prev.groups || state.order !== prev.order) regroup() }),
    useRootCacheStore.subscribe((state, prev) => { if (state.roots !== prev.roots) regroup() }),
    useTeamRosterStore.subscribe((state, prev) => { if (state.byHost !== prev.byHost) regroupSoon() }),
    useSessionStore.subscribe((state, prev) => { if (state.sessions !== prev.sessions) regroupSoon() }),
    // a host re-pointed, or its daemon identity verified / lost, changes which remote seats the team index trusts
    useHostStore.subscribe((state, prev) => { if (state.hosts !== prev.hosts || state.runtime !== prev.runtime) regroupSoon() }),
    useTeamUiStore.subscribe((state, prev) => { if (state.sidebarView !== prev.sidebarView) regroup() }),
    useLayoutStore.subscribe((state, prev) => { if (state.activityBarWidth !== prev.activityBarWidth || state.tabPosition !== prev.tabPosition) regroup() }),
  ]
  return () => {
    for (const off of unsubs) off()
    if (timer !== null) clearTimeout(timer)
  }
}
