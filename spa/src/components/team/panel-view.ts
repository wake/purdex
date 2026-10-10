// spa/src/components/team/panel-view.ts — what the panel area shows for the active tab (team spec §4.4 Round 3, first match wins):
//   1. the tab belongs to a team                       → the team's drilled-in workbook if it has one, else the team view;
//   2. its own `cc` conversation has a workbook        → that workbook (shared four-state value);
//   3. otherwise                                       → nothing.
// Pure: it reads memories and clears none, so switching away and back shows the same thing. There is no per-tab toggle.
import type { TeamPanelTeam } from './team-display'

export interface OwnWorkbookTarget { hostId: string; sessionId: string }

export type PanelView =
  | { kind: 'team'; team: TeamPanelTeam }
  /** The tab's own conversation's workbook (a tab of no team). */
  | ({ kind: 'workbook'; from: 'own' } & OwnWorkbookTarget)
  /** The team's drilled-in seat workbook; the team rides along (its frame, mode and seats). */
  | { kind: 'workbook'; from: 'team'; team: TeamPanelTeam }

export interface PanelViewInput {
  /** `TeamDisplay.panelTeam(activeTabId)`. */
  panelTeam: TeamPanelTeam | null
  teamDrill: Readonly<Record<string, { hostId: string; sessionId: string }>>
  /** The active tab's own conversation, only when it HAS a workbook on a `workbook.v1` host (`useOwnWorkbook`). */
  own: OwnWorkbookTarget | null
}

export function panelView({ panelTeam, teamDrill, own }: PanelViewInput): PanelView | null {
  if (panelTeam !== null) return teamDrill[panelTeam.teamKey] ? { kind: 'workbook', from: 'team', team: panelTeam } : { kind: 'team', team: panelTeam }
  return own === null ? null : { kind: 'workbook', from: 'own', hostId: own.hostId, sessionId: own.sessionId }
}
