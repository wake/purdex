// spa/src/lib/task-groups/current.ts — the placement and ordering inputs read from the CURRENT stores, for code that is not a React render: the selection subscriber, the fresh-tab write, the range. A render reads its own memoized
// inputs (TaskGroupView); an action or a subscriber runs once per event and may afford one pass over the tabs. Reads only.
import { useTabStore } from '../../stores/useTabStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { currentTeamState } from '../team/team-state'
import { rootOf } from './root-cache'
import type { GroupOrderCtx } from './group-order'
import type { PlacementCtx } from './placement'

export function currentPlacementCtx(): PlacementCtx {
  const { order, groups } = useManualGroupsStore.getState()
  return { index: currentTeamState().index, manual: { order, groups }, rootOf }
}

export function currentGroupOrderCtx(): GroupOrderCtx {
  return { ...currentPlacementCtx(), tabs: useTabStore.getState().tabs }
}
