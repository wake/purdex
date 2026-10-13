// spa/src/components/team/group-drag.ts — what the group frame hands the lead tab so that dragging the lead drags the group (TG-1).
import { createContext, useContext } from 'react'
import type { useSortable } from '@dnd-kit/sortable'

/** The sortable id of a group's frame: the unit that moves when its lead is dragged. */
export const teamGroupDragId = (teamKey: string) => `team-group:${teamKey}`

/** The frame's pointer listeners, which the lead tab wears. */
export type GroupDragHandle = ReturnType<typeof useSortable>['listeners']
export const GroupDragHandleContext = createContext<GroupDragHandle>(undefined)
/** The group drag listeners for the lead tab; undefined outside a group. */
export const useGroupDragHandle = () => useContext(GroupDragHandleContext)
