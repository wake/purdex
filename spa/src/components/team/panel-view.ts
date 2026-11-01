// spa/src/components/team/panel-view.ts — what the panel area shows for the active tab (team spec §4.4 Round 3; task groups spec §7, first match wins):
//   1. the tab belongs to a team                       → the team view;
//   2. the tab has an agent session (live or ended)    → the personal panel;
//   3. otherwise                                       → nothing.
// Pure: it reads memories and clears none, so switching away and back shows the same thing. There is no per-tab toggle.
import type { TeamPanelTeam } from './team-display'
import type { PersonalTarget } from '../../lib/task-groups/personal'

export type PanelView =
  | { kind: 'team'; team: TeamPanelTeam }
  /** A tab of no team that shows an agent session: the personal panel (the one shared mode value). */
  | { kind: 'personal'; target: PersonalTarget }

export interface PanelViewInput {
  /** `TeamDisplay.panelTeam(activeTabId)`. */
  panelTeam: TeamPanelTeam | null
  /** The active tab's agent session (`usePersonalTarget`); null for a tab with none. */
  personal: PersonalTarget | null
}

export function panelView({ panelTeam, personal }: PanelViewInput): PanelView | null {
  if (panelTeam !== null) return { kind: 'team', team: panelTeam }
  return personal === null ? null : { kind: 'personal', target: personal }
}
