// spa/src/components/team/TeamGhostLeadRow.tsx — a lead whose tab is closed while the team runs on (spec §4.6, plan TI-3).
//
// Closing the lead tab closes the group's tabs; the sessions keep running. The sidebar keeps a faded lead row (and its beads)
// in the workspace it was closed from as the way back: clicking it reopens the lead tab and the group comes back.
import type { TeamDisplay, TeamGhostLead } from './team-display'
import { TeamSidebarBlock } from './TeamSidebarBlock'
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'
import { useI18nStore } from '../../stores/useI18nStore'

export function TeamGhostLeadRow({ ghost, team, activeTabId }: { ghost: TeamGhostLead; team: TeamDisplay; activeTabId: string | null }) {
  const t = useI18nStore((s) => s.t)
  const { lead, color, teamKey, members, collapsed, capsule } = ghost
  const open = () => team.onOpenSeat(teamKey, lead.sessionId)
  return (
    <TeamSidebarBlock team={team} teamKey={teamKey} color={color} collapsed={collapsed} capsule={capsule} members={members} activeTabId={activeTabId} ghost>
      <div
        role="button"
        tabIndex={0}
        title={t('team.sidebar.ghost', { title: lead.title })}
        onClick={open}
        onKeyDown={(e) => { if (e.key === 'Enter') open() }}
        className="group relative flex items-center gap-1.5 mx-2 pl-[18px] pr-1.5 py-1 rounded-md text-xs cursor-pointer text-text-muted border border-dashed border-border-default hover:bg-surface-hover"
      >
        <span className="opacity-50 flex items-center gap-1.5">
          <TeamSeatIcon hostId={lead.hostId} sessionCode={lead.sessionCode} />
          <TeamSeatHostBadge hostId={lead.hostId} sessionCode={lead.sessionCode} />
        </span>
        <span className="flex-1 truncate italic">{lead.title}</span>
        <span className="text-[10px] flex-shrink-0">{t('team.sidebar.ghost_badge')}</span>
      </div>
    </TeamSidebarBlock>
  )
}
