// spa/src/components/team/TeamTabGroup.tsx — a lead/member group on the TabBar (team interface plan TI-2, spec §4.2, §5).
// Ported from the prototype at f61748aa with only the final variant: a team-colour label capsule with dark text (and `+N`
// when collapsed), and on every tab of the group a crisp top-right shadow plus a faint wash, drawn by a pointer-transparent
// overlay so the tab's own background and active highlight stay. No separators are drawn inside the group (TabBar).
import type { ReactNode } from 'react'
import { useThemeStore } from '../../stores/useThemeStore'
import type { TeamCapsule, TeamTabMark } from './team-display'

/** Dark text on the pastel team colours, in both themes. */
const LABEL_FG = '#14141f'

/** The capsule's tooltip: the mark's own, plus the whole lead title when the capsule shows it cut. */
function labelTitle(mark: TeamCapsule): string {
  return mark.truncated && !mark.tooltip.includes(mark.full) ? `${mark.tooltip}\n${mark.full}` : mark.tooltip
}

export function TeamGroupLabel({ mark, hidden, onToggle }: { mark: TeamCapsule; /** Member tabs the collapse hides. */ hidden: number; onToggle: (teamKey: string) => void }) {
  return (
    <button
      type="button"
      data-testid="team-group-label"
      data-team-key={mark.teamKey}
      aria-expanded={!mark.collapsed}
      title={labelTitle(mark)}
      onClick={() => onToggle(mark.teamKey)}
      className="flex items-center gap-1 h-[20px] max-w-[120px] px-2 mx-1 rounded-md text-[11px] font-semibold whitespace-nowrap cursor-pointer flex-shrink-0"
      style={{ background: mark.color, color: LABEL_FG, marginTop: 2 }}
    >
      <span className="truncate">{mark.label}</span>
      {mark.collapsed && hidden > 0 && <span data-testid="team-group-hidden" className="opacity-70">+{hidden}</span>}
    </button>
  )
}

export function TeamTabGroupFrame({ mark, children }: { mark: TeamTabMark; children: ReactNode }) {
  return (
    <div data-testid="team-tab-group" data-team-key={mark.teamKey} className="relative flex items-center flex-shrink-0 h-full">
      {children}
    </div>
  )
}

/**
 * The shadow and the wash of one group tab: `1px -1px 0` at 70 % of the team colour (the light theme darkens the colour
 * first) and a 6 % wash (light 8 %). An overlay, so the tab's own background and active highlight are untouched.
 */
export function TeamTabShadow({ mark }: { mark: TeamTabMark }) {
  const light = useThemeStore((s) => s.activeThemeId) === 'light'
  const base = light ? `color-mix(in oklab, ${mark.color}, black 25%)` : mark.color
  const wash = light ? 8 : 6
  return (
    <span
      data-testid="team-tab-shadow"
      data-team-color={base}
      data-shadow={`1px -1px 0 color-mix(in oklab, ${base} 70%, transparent)`}
      data-wash={String(wash)}
      aria-hidden="true"
      className="absolute inset-0 pointer-events-none z-10"
      style={{
        borderRadius: 6,
        boxShadow: `1px -1px 0 color-mix(in oklab, ${base} 70%, transparent)`,
        background: `color-mix(in oklab, ${base} ${wash}%, transparent)`,
      }}
    />
  )
}
