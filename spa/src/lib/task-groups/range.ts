// spa/src/lib/task-groups/range.ts — the range of the bar in the task-group view (task groups spec §6.2, TG-4a): the tabs of the SELECTED group, in the order the bar shows them (`groupTabs`). It is null whenever the
// workspace rules apply — the workspace view in force (`effectiveView`: stored view, wide sidebar, tabs not on the left), no group selected yet — so a caller passes it on to `getVisibleTabIds` and the workspace rule
// takes over. (The watcher area is `getVisibleTabIds`' own business: it ignores the range there.) The shortcuts read it outside React (`selectedGroupRange`), the App in a render (`useSelectedGroupRange`).
import { useMemo } from 'react'
import { useTabStore } from '../../stores/useTabStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useTeamViews } from '../../hooks/useTeamViews'
import { buildTeamIndex } from '../team/team-index'
import { currentGroupOrderCtx } from './current'
import { effectiveView, useEffectiveView } from './effective-view'
import { groupTabs } from './group-order'
import { rootOf, useRootCacheStore } from './root-cache'

/** The selected group's tabs now, or null when the workspace rules apply. For code outside a render. */
export function selectedGroupRange(): string[] | null {
  if (effectiveView() !== 'groups') return null
  const selected = useGroupViewStore.getState().selectedGroup
  return selected === null ? null : groupTabs(selected, currentGroupOrderCtx())
}

/** The same, for a render: recomputed when the tabs, the groups, the teams or the roots read change. */
export function useSelectedGroupRange(): string[] | null {
  const inForce = useEffectiveView() === 'groups'
  const selected = useGroupViewStore((s) => s.selectedGroup)
  const tabs = useTabStore((s) => s.tabs)
  const order = useManualGroupsStore((s) => s.order)
  const groups = useManualGroupsStore((s) => s.groups)
  const sessions = useSessionStore((s) => s.sessions)
  const memberOrder = useTeamUiStore((s) => s.memberOrder)
  const teams = useTeamViews(memberOrder)
  const roots = useRootCacheStore((s) => s.roots)
  return useMemo(() => {
    if (!inForce || selected === null) return null
    // `roots` is read through `rootOf`; it is a dependency so a root read after the first paint re-places the tabs
    void roots
    return groupTabs(selected, { tabs, index: buildTeamIndex(teams, tabs, sessions), manual: { order, groups }, rootOf })
  }, [inForce, selected, tabs, order, groups, sessions, teams, roots])
}
