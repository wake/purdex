// spa/src/components/team/TeamTabGroup.tsx — a lead/member group on the TabBar (team interface plan TI-2, spec §4.2, §5).
// On every tab of the group a team-colour shadow plus a faint wash, drawn by a pointer-transparent overlay so the tab's own
// background and active highlight stay. No label and no collapse control (round 2); no separators inside the group (TabBar).
import { createContext, useContext, type ReactNode } from 'react'
import { useSortable } from '@dnd-kit/sortable'
import { useThemeStore } from '../../stores/useThemeStore'
import type { TeamTabMark } from './team-display'
import { groupShadow, shadowBase } from './group-shadow'

/** The sortable id of a group's frame: the unit that moves when its lead is dragged (TG-1). */
export const teamGroupDragId = (teamKey: string) => `team-group:${teamKey}`

/** What the lead tab needs to start a drag of the whole group: the frame's pointer listeners. */
export type GroupDragHandle = ReturnType<typeof useSortable>['listeners']
const GroupDragHandleContext = createContext<GroupDragHandle>(undefined)
/** The group drag listeners for the lead tab; undefined for every other tab (a member drags itself). */
export const useGroupDragHandle = () => useContext(GroupDragHandleContext)

/**
 * The group's frame is itself a sortable item (TG-1): dragging the lead tab, which wears the frame's listeners, moves the
 * frame — and with it every tab inside, hidden members included, since those sit in `normalIds` right behind the lead.
 */
export function TeamTabGroupFrame({ mark, children }: { mark: TeamTabMark; children: ReactNode }) {
  const { listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: teamGroupDragId(mark.teamKey) })
  return (
    <div
      ref={setNodeRef}
      data-testid="team-tab-group"
      data-team-key={mark.teamKey}
      className="relative flex items-center flex-shrink-0 h-full"
      style={{ transform: transform ? `translate3d(${Math.round(transform.x)}px, 0, 0)` : undefined, transition, zIndex: isDragging ? 10 : undefined }}
    >
      <GroupDragHandleContext.Provider value={listeners}>{children}</GroupDragHandleContext.Provider>
    </div>
  )
}

/**
 * The shadow and the wash of one group tab: the crisp top-right shadow (group-shadow.ts) and a
 * 6 % wash (light 8 %). An overlay, so the tab's own background and active highlight are untouched.
 */
export function TeamTabShadow({ mark }: { mark: TeamTabMark }) {
  const theme = useThemeStore((s) => s.activeThemeId)
  const base = shadowBase(mark.color, theme)
  const shadow = groupShadow(mark.color, theme)
  const wash = theme === 'light' ? 8 : 6
  return (
    <span
      data-testid="team-tab-shadow"
      data-team-color={base}
      data-shadow={shadow}
      data-wash={String(wash)}
      aria-hidden="true"
      className="absolute inset-0 pointer-events-none z-10"
      style={{
        borderRadius: 6,
        boxShadow: shadow,
        background: `color-mix(in oklab, ${base} ${wash}%, transparent)`,
      }}
    />
  )
}
