// spa/src/lib/task-groups/current.ts — the placement and ordering inputs read from the CURRENT stores, for code that is not a React render: the selection subscriber, the fresh-tab write, the range. A render reads its own memoized
// inputs (TaskGroupView); an action or a subscriber runs once per event and may afford one pass over the tabs. Reads only.
import { useTabStore } from '../../stores/useTabStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useSessionStore } from '../../stores/useSessionStore'
import type { Tab } from '../../types/tab'
import { currentTeamState } from '../team/team-state'
import { buildTeamIndex } from '../team/team-index'
import { rootOf } from './root-cache'
import type { GroupOrderCtx } from './group-order'
import type { PlacementCtx } from './placement'

/** `pending`: a tab about to be inserted, which the team index must already know — a seat tab is recognised by its panes, and the store does not hold the tab yet. */
export function currentPlacementCtx(pending?: Tab): PlacementCtx {
  const { order, groups } = useManualGroupsStore.getState()
  const state = currentTeamState()
  const index = pending === undefined
    ? state.index
    : buildTeamIndex(state.views, { ...useTabStore.getState().tabs, [pending.id]: pending }, useSessionStore.getState().sessions)
  return { index, manual: { order, groups }, rootOf }
}

export function currentGroupOrderCtx(): GroupOrderCtx {
  return { ...currentPlacementCtx(), tabs: useTabStore.getState().tabs }
}
