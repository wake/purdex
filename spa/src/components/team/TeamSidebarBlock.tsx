// spa/src/components/team/TeamSidebarBlock.tsx — one team in the sidebar tab list (plan TI-3, spec §4.3, P4, P9).
//
// Two parts: the lead row (the real InlineTab, or a faded ghost row when the lead tab is closed) and under it the member
// beads hanging from the lead's bot icon by the tree tick. Folded (P9): no caret; one line with a members icon and one main
// light per member, a click anywhere on it expands; unfolded, a click on the tick or the blank part of the bead area folds.
// The fold shares the team's `collapsed` state with the TabBar group (useTeamUiStore), nothing is kept here.
import type { ReactNode } from 'react'
import { UsersThree } from '@phosphor-icons/react'
import type { TeamCapsule, TeamDisplay, TeamSeatView } from './team-display'
import { TeamGroupLabel } from './TeamTabGroup'
import { TeamMemberBeads } from './TeamMemberBeads'
import { useAgentStore } from '../../stores/useAgentStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { compositeKey } from '../../lib/composite-key'

/** The four light colors (same as the tab lights). */
const LIGHT_COLOR = { running: '#4ade80', waiting: '#facc15', idle: '#6b7280', error: '#ef4444' } as const

function MemberLightDot({ member }: { member: TeamSeatView }) {
  const status = useAgentStore((s) => (member.hostId !== '' && member.sessionCode !== '' ? s.statuses[compositeKey(member.hostId, member.sessionCode)] : undefined))
  return (
    <span
      data-testid="team-fold-dot"
      data-status={status ?? 'none'}
      title={member.title}
      className="w-1.5 h-1.5 rounded-full flex-shrink-0"
      style={{ background: status ? LIGHT_COLOR[status] : 'var(--border-default)' }}
    />
  )
}

interface Props {
  team: TeamDisplay
  teamKey: string
  color: string
  collapsed: boolean
  /** The label capsule above the lead row: the tab bar's own, here without the `+N` (the beads show the members). */
  capsule: TeamCapsule
  members: TeamSeatView[]
  /** The lead row itself. */
  children: ReactNode
  ghost?: boolean
  activeTabId: string | null
}

export function TeamSidebarBlock({ team, teamKey, color, collapsed, capsule, members, children, ghost = false, activeTabId }: Props) {
  const t = useI18nStore((s) => s.t)
  const showBeads = !collapsed
  return (
    <div data-testid={ghost ? 'team-ghost-lead' : 'team-lead-block'} data-team-key={teamKey} className="flex flex-col gap-0.5">
      <div data-testid="team-sidebar-label" className={`flex items-center mx-2 pl-[18px] ${ghost ? 'opacity-50' : ''}`}>
        <TeamGroupLabel mark={capsule} hidden={0} onToggle={team.onToggleCollapse} />
      </div>
      {children}
      {showBeads && (
        <TeamMemberBeads
          teamKey={teamKey}
          color={color}
          members={members}
          activeTabId={activeTabId}
          withHost={team.beadHost}
          onOpen={(sid) => team.onOpenSeat(teamKey, sid)}
          onReorder={(ids) => team.onReorderMembers(teamKey, ids)}
          onBlankClick={() => team.onToggleCollapse(teamKey)}
        />
      )}
      {!showBeads && members.length > 0 && (
        <div data-testid="team-sidebar-plate" className="mx-2 mb-0.5 h-6 rounded-lg bg-surface-secondary">
          <button
            type="button"
            data-testid="team-sidebar-collapsed"
            title={t('team.sidebar.collapsed', { count: members.length })}
            onClick={() => team.onToggleCollapse(teamKey)}
            className="flex items-center gap-1.5 w-full h-full pl-[26px] pr-1.5 rounded-lg text-text-muted hover:bg-surface-hover hover:text-text-primary cursor-pointer"
          >
            <UsersThree size={14} />
            <span className="flex items-center gap-1">{members.map((m) => <MemberLightDot key={m.sessionId} member={m} />)}</span>
          </button>
        </div>
      )}
    </div>
  )
}
