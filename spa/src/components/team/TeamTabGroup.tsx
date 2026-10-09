// spa/src/components/team/TeamTabGroup.tsx — a lead/member group on the TabBar (team interface plan TI-2, spec §4.2, §5).
// On every tab of the group a team-colour shadow plus a faint wash, drawn by a pointer-transparent overlay so the tab's own
// background and active highlight stay. No label and no collapse control (round 2); no separators inside the group (TabBar).
import type { ReactNode } from 'react'
import { useThemeStore } from '../../stores/useThemeStore'
import type { TeamTabMark } from './team-display'
import { groupShadow, shadowBase } from './group-shadow'

export function TeamTabGroupFrame({ mark, children }: { mark: TeamTabMark; children: ReactNode }) {
  return (
    <div data-testid="team-tab-group" data-team-key={mark.teamKey} className="relative flex items-center flex-shrink-0 h-full">
      {children}
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
