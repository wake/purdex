// spa/src/components/team/panel-view.ts — what the panel area shows for the active tab (team spec §4.4, first match wins):
//   1. the tab's workbook toggle is on          → that tab's workbook;
//   2. the tab belongs to a team                → the team's drilled-in workbook if it has one, else the team view;
//   3. otherwise                                → nothing.
// Pure: it reads three memories and clears none, so switching away and back shows the same thing.
import type { TeamPanelTeam } from './team-display'

export type PanelView =
  | { kind: 'team'; team: TeamPanelTeam }
  | { kind: 'workbook'; from: 'tab' | 'team' }

export interface PanelViewInput {
  workbookTabs: Readonly<Record<string, true>>
  /** `TeamDisplay.panelTeam(activeTabId)`. */
  panelTeam: TeamPanelTeam | null
  teamDrill: Readonly<Record<string, { hostId: string; sessionId: string }>>
}

export function panelView(activeTabId: string | null, { workbookTabs, panelTeam, teamDrill }: PanelViewInput): PanelView | null {
  if (activeTabId !== null && workbookTabs[activeTabId] === true) return { kind: 'workbook', from: 'tab' }
  if (panelTeam === null) return null
  return teamDrill[panelTeam.teamKey] ? { kind: 'workbook', from: 'team' } : { kind: 'team', team: panelTeam }
}
