// spa/src/components/team/useTitleBarTeam.ts — the team of the active tab, for the title bar's strip and button.
import { useTeamDisplay, type TeamPanelTeam } from './team-display'
import { useTabStore } from '../../stores/useTabStore'

/** The team of the active tab (in any state of the area), or null: no provider, or the tab belongs to no team. */
export function useTitleBarTeam(): TeamPanelTeam | null {
  const display = useTeamDisplay()
  const activeTabId = useTabStore((s) => s.activeTabId)
  return display ? display.panelTeam(activeTabId) : null
}
