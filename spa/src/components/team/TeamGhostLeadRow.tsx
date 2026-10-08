// spa/src/components/team/TeamGhostLeadRow.tsx — a lead whose tab is closed while the team runs on.
//
// Closing the lead tab closes the group's tabs; the sessions keep running. The sidebar keeps a faded lead
// row (and its beads) as the way back: clicking it reopens the lead tab, and the group comes back.
import type { TeamDisplay, TeamGhostLead } from './team-display'
import { TeamSidebarBlock } from './TeamSidebarBlock'
import { TeamSeatHostBadge, TeamSeatIcon } from './TeamSeatIcon'

export function TeamGhostLeadRow({ ghost, team }: { ghost: TeamGhostLead; team: TeamDisplay }) {
  const { lead, color, teamKey, members, label, unnamed } = ghost
  return (
    <TeamSidebarBlock team={team} teamKey={teamKey} color={color} label={label} unnamed={unnamed} collapsed={false} members={members} ghost>
      <div
        role="button"
        tabIndex={0}
        title={`${lead.title}（分頁已關，點一下重開）`}
        onClick={() => team.onOpenSeat(teamKey, lead.sessionId)}
        onKeyDown={(e) => { if (e.key === 'Enter') team.onOpenSeat(teamKey, lead.sessionId) }}
        className="group relative flex items-center gap-1.5 mx-2 pl-[18px] pr-1.5 py-1 rounded-md text-xs cursor-pointer text-text-muted border border-dashed border-border-default hover:bg-surface-hover"
      >
        <span className="opacity-50 flex items-center gap-1.5">
          <TeamSeatIcon hostId={lead.hostId} sessionCode={lead.sessionCode} subagents={false} />
          <TeamSeatHostBadge hostId={lead.hostId} sessionCode={lead.sessionCode} />
        </span>
        <span className="flex-1 truncate italic">{lead.title}</span>
        <span className="text-[10px] flex-shrink-0">未開</span>
      </div>
    </TeamSidebarBlock>
  )
}
