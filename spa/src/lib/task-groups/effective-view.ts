// spa/src/lib/task-groups/effective-view.ts — which sidebar view is really in force (task groups spec §3, TG-3a-2). The task-group view exists only in the WIDE sidebar and only while the tabs are not in the sidebar
// (`tabPosition` is not `left`): in a narrow sidebar, or with the tabs on the left, the App behaves as in the workspace view (range, selection, the automatic write, the roots read) whatever the stored view says. The
// tab-store actions, the selection subscriber, the range, the shortcuts, the roots lifecycle and the App all read THIS one function, so they never disagree. A plain reader outside React (`effectiveView`) and a hook.
import { useLayoutStore, type ActivityBarWidth, type TabPosition } from '../../stores/useLayoutStore'
import { useTeamUiStore, type SidebarView } from '../../stores/useTeamUiStore'

export type EffectiveView = 'workspace' | 'groups'

/** The rule, pure. */
export function effectiveViewOf(stored: SidebarView, width: ActivityBarWidth, tabPosition: TabPosition): EffectiveView {
  return stored === 'groups' && width === 'wide' && tabPosition !== 'left' ? 'groups' : 'workspace'
}

/** The view in force now, from the stores. */
export function effectiveView(): EffectiveView {
  const { activityBarWidth, tabPosition } = useLayoutStore.getState()
  return effectiveViewOf(useTeamUiStore.getState().sidebarView, activityBarWidth, tabPosition)
}

/** The same, for a render. */
export function useEffectiveView(): EffectiveView {
  const stored = useTeamUiStore((s) => s.sidebarView)
  const width = useLayoutStore((s) => s.activityBarWidth)
  const tabPosition = useLayoutStore((s) => s.tabPosition)
  return effectiveViewOf(stored, width, tabPosition)
}
