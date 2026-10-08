import { SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable'
import type { Tab } from '../../../types/tab'
import { InlineTab } from './InlineTab'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTeamDisplay } from '../../../components/team/team-display'
import { TeamMemberBeads } from '../../../components/team/TeamMemberBeads'
import { TeamGhostLeadRow } from '../../../components/team/TeamGhostLeadRow'

interface Props {
  tabIds: string[]
  tabsById: Record<string, Tab>
  activeTabId: string | null
  sourceWsId: string | null
  onSelect: (tabId: string) => void
  onClose: (tabId: string) => void
  onMiddleClick: (tabId: string) => void
  onContextMenu: (e: React.MouseEvent, tabId: string) => void
  onRename?: (tabId: string) => void
}

export function InlineTabList({
  tabIds,
  tabsById,
  activeTabId,
  sourceWsId,
  onSelect,
  onClose,
  onMiddleClick,
  onContextMenu,
  onRename,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const team = useTeamDisplay()
  // With a team provider, member rows fold into the bead row under their lead.
  const validIds = tabIds.filter((id) => !!tabsById[id] && !team?.sidebarHidden(id))
  const ghosts = team ? team.ghostLeads(sourceWsId) : []

  if (validIds.length === 0 && ghosts.length === 0) {
    return (
      <div className="pl-7 pr-3 py-1 text-[11px] text-text-muted italic">
        {t('nav.workspace_empty')}
      </div>
    )
  }

  return (
    <SortableContext items={validIds} strategy={verticalListSortingStrategy}>
      <div className="flex flex-col gap-0.5 py-0.5">
        {validIds.map((id) => {
          const row = (
            <InlineTab
              key={id}
              tab={tabsById[id]}
              isActive={activeTabId === id}
              sourceWsId={sourceWsId}
              onSelect={onSelect}
              onClose={onClose}
              onMiddleClick={onMiddleClick}
              onContextMenu={onContextMenu}
              onRename={onRename}
            />
          )
          const beads = team?.sidebarBeads(id)
          if (!team || !beads) return row
          return (
            <div key={id} data-testid="team-lead-block" className="relative flex flex-col gap-0.5">
              <span className="absolute left-[6px] top-1 h-[18px] w-[3px] rounded" style={{ background: beads.color }} />
              {row}
              <TeamMemberBeads
                teamKey={beads.teamKey}
                color={beads.color}
                members={beads.members}
                activeTabId={team.activeTabId}
                withHost={team.beadHost}
                onOpen={(sid) => team.onOpenSeat(beads.teamKey, sid)}
                onReorder={(ids) => team.onReorderMembers(beads.teamKey, ids)}
              />
            </div>
          )
        })}
        {team && ghosts.map((g) => (
          <TeamGhostLeadRow key={g.teamKey} ghost={g} team={team} />
        ))}
      </div>
    </SortableContext>
  )
}
