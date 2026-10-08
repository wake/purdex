// spa/src/components/team/TeamSidebarBlock.tsx — one team in the sidebar tab list.
//
// Three lines: the team-name label, the lead row (the real InlineTab, or a faded row when the lead tab
// is closed), and the member beads. No border line ties them together; TeamSidebarStyle picks the sign:
//   hook      — the bead row hangs under the lead with ⎿ (Claude Code's tree mark); no toggle here
//   plusminus — ⊟／⊞ in front of the lead row expands / collapses the beads
//   chevron   — ▾／▸ in front of the lead row, and the bead row hangs with ⎿
// The toggle shares the team's collapsed state with the TabBar group.
import type { ReactNode } from 'react'
import { CaretDown, CaretRight } from '@phosphor-icons/react'
import type { TeamDisplay, TeamSeatView } from './team-display'
import { TeamMemberBeads } from './TeamMemberBeads'

const LABEL_FG = '#14141f'

interface Props {
  team: TeamDisplay
  teamKey: string
  color: string
  label: string
  unnamed: boolean
  collapsed: boolean
  members: TeamSeatView[]
  /** The lead row itself. */
  children: ReactNode
  ghost?: boolean
}

export function TeamSidebarBlock({ team, teamKey, color, label, unnamed, collapsed, members, children, ghost = false }: Props) {
  const style = team.sidebarStyle
  const hasToggle = style !== 'hook' && members.length > 0
  const showBeads = !(hasToggle && collapsed)
  const hook = style === 'hook' || style === 'chevron'
  return (
    <div data-testid={ghost ? 'team-ghost-lead' : 'team-lead-block'} data-sidebar-style={style} className="flex flex-col gap-0.5">
      <div className="flex items-center mx-2 pl-[18px] h-[18px]">
        <span
          data-testid="team-sidebar-label"
          title={unnamed ? `${label}（team 沒有名字，暫用 lead 的標題）` : label}
          className={`px-1.5 rounded text-[10px] font-semibold leading-[16px] truncate max-w-full ${unnamed ? 'italic opacity-80' : ''}`}
          style={{ background: color, color: LABEL_FG }}
        >
          {label}
        </span>
      </div>
      <div className="relative">
        {children}
        {hasToggle && (
          <button
            type="button"
            data-testid="team-sidebar-toggle"
            title={collapsed ? '展開 member' : '收合 member'}
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => { e.stopPropagation(); team.onToggleCollapse(teamKey) }}
            className="absolute left-[2px] top-1/2 -translate-y-1/2 w-3.5 h-4 flex items-center justify-center rounded-[3px] text-text-muted hover:text-text-primary hover:bg-surface-hover cursor-pointer text-[13px] leading-none"
          >
            {style === 'plusminus' ? (collapsed ? '⊞' : '⊟') : collapsed ? <CaretRight size={10} weight="bold" /> : <CaretDown size={10} weight="bold" />}
          </button>
        )}
      </div>
      {showBeads && (
        <TeamMemberBeads
          teamKey={teamKey}
          color={color}
          members={members}
          activeTabId={team.activeTabId}
          withHost={team.beadHost}
          onOpen={(sid) => team.onOpenSeat(teamKey, sid)}
          onReorder={(ids) => team.onReorderMembers(teamKey, ids)}
          prefix={hook ? <span className="text-text-muted text-[13px] leading-none -mt-1.5 mr-0.5 select-none" aria-hidden="true">⎿</span> : undefined}
        />
      )}
      {!showBeads && (
        <div className="ml-[42px] text-[10px] text-text-muted -mt-0.5 mb-0.5" data-testid="team-sidebar-collapsed">{members.length} 個 member 收起</div>
      )}
    </div>
  )
}
