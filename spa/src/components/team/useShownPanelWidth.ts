// spa/src/components/team/useShownPanelWidth.ts — the width the team panel actually draws (user 2026-10-20). The shared area width
// (the dragged draft, else the persisted one) is the CAP; full mode and a drilled-in workbook use it as is, one-line mode follows
// its content (lineWidthFor). The area frame and the journal cards (which hug the panel's left edge) both read this one value.
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { usePanelDraftStore } from '../../stores/usePanelDraftStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { lineWidthFor } from './panel-layout'
import type { TeamPanelTeam } from './team-display'

/** `team` null = no team list shown (use the area width); `drilled` = a seat workbook takes the list's place in the frame; `personalLine` = the personal panel's one-line form, which is a one-person team's width (task groups spec §7). */
export function useShownPanelWidth(team: TeamPanelTeam | null, drilled: boolean, personalLine = false): number {
  const persisted = useTeamUiStore((s) => s.panel.width)
  const area = usePanelDraftStore((s) => s.draft) ?? persisted
  const lightStyle = useUISettingsStore((s) => s.tabIndicatorStyle)
  const ringBox = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  if (personalLine) return lineWidthFor(1, area, lightStyle, ringBox)
  if (team === null || team.mode !== 'line' || drilled) return area
  return lineWidthFor(1 + team.members.length, area, lightStyle, ringBox)
}
