// spa/src/components/team/team-display.ts — what the tab surfaces need to draw lead/member groups (team interface plan
// TI-1a). Ported from the prototype at f61748aa with only the FINAL variant kept (spec §5): one group style, one
// sidebar style, no prototype switches. With no provider both surfaces render exactly as before (`useTeamDisplay()` is
// null); the provider (TeamDisplayProvider.tsx) turns the grouping on.
//
// This is the STRUCTURE of the teams — who is on which team, in what order, which tab stands for whom, colour, label,
// collapse, ghost workspaces. Live readings (a seat's model, effort, context) are deliberately not here: the panel
// selects them from `useTeamRosterStore` per seat, so a context-window update re-renders the panel's row and not every
// tab in the bar.
import { createContext, useContext } from 'react'

/** Eight team colors, deliberately away from the default host blue and the four light colors. */
export const TEAM_COLORS = ['#a78bfa', '#2dd4bf', '#f472b6', '#fb923c', '#e879f9', '#a3a3ff', '#5eead4', '#fda4af'] as const

export function teamColor(index: number): string {
  return TEAM_COLORS[((index % TEAM_COLORS.length) + TEAM_COLORS.length) % TEAM_COLORS.length]
}

/** One seat as the beads and the panel draw it. */
export interface TeamSeatView {
  sessionId: string
  title: string
  hostId: string
  /** tmux session code: the agent light is keyed by (hostId, code). Empty when the host's list does not carry the session. */
  sessionCode: string
  role: 'lead' | 'member'
  /** The tab showing this seat, or null when it has none (an unopened member). */
  tabId: string | null
  /** The seat's roster state (`active` | `joining` | `releasing` | `killing`) and the alias of the host a remote member lives on ('' = local). */
  state: string
  hostAlias: string
}

/** How one tab sits in a team group on the TabBar. */
export interface TeamTabMark {
  teamKey: string
  color: string
  /** The capsule text (the label, or the lead title cut to 10 wide) and the whole text for a tooltip. */
  label: string
  full: string
  truncated: boolean
  /** Tooltip of the capsule: `"<name> (<label>)"` when both exist. */
  tooltip: string
  role: 'lead' | 'member'
  /** The seat's roster state (`active` | `joining` | `releasing` | `killing`) and the alias of the host a remote member lives on ('' = local). */
  seatState: string
  hostAlias: string
  /** First / last VISIBLE tab of the group in this tab's workspace. */
  first: boolean
  last: boolean
  collapsed: boolean
  /** A member tab hidden by the collapse (the surface draws nothing for it). */
  hidden: boolean
  /** Member tabs hidden by the collapse in this workspace (shown on the capsule as `+N`). */
  hiddenCount: number
}

export interface TeamBeads {
  teamKey: string
  color: string
  collapsed: boolean
  /** The members in team order, opened or not. */
  members: TeamSeatView[]
}

/** A lead whose tab is closed while its team runs on (the sidebar shows it faded, as the way back in). */
export interface TeamGhostLead {
  teamKey: string
  color: string
  label: string
  full: string
  /** The team's shared fold state: the ghost's beads fold like a live lead's. */
  collapsed: boolean
  lead: TeamSeatView
  members: TeamSeatView[]
}

/** The team a tab belongs to as the panel draws it (structure only; readings come from the roster store). */
export interface TeamPanelTeam {
  teamKey: string
  color: string
  /** The panel header: the name, else the lead's label uncut. */
  name: string
  unnamed: boolean
  tooltip: string
  mode: 'full' | 'line'
  lead: TeamSeatView
  members: TeamSeatView[]
}

export interface TeamDisplay {
  /** Show the host icon next to each bead (the user setting, spec P7). */
  beadHost: boolean
  tabMark: (tabId: string) => TeamTabMark | null
  /**
   * The member tabs of THIS tab list that are folded into the bead row under their lead row: those in a run behind
   * their lead (team-runs), whatever the collapse. A member whose lead is in another workspace, or that stayed open
   * after the lead closed, is not among them: it is an ordinary row.
   */
  sidebarHidden: (tabIds: readonly string[]) => Set<string>
  /** The bead row under a lead tab's row; null for any other tab. */
  sidebarBeads: (tabId: string) => TeamBeads | null
  ghostLeads: (workspaceId: string | null) => TeamGhostLead[]
  /** The team of the active tab, for the floating panel; null when it belongs to none. */
  panelTeam: (activeTabId: string | null) => TeamPanelTeam | null
  /** Actions (wired in TI-1b; no-ops until then). */
  onToggleCollapse: (teamKey: string) => void
  onOpenSeat: (teamKey: string, sessionId: string) => void
  onReorderMembers: (teamKey: string, sessionIds: string[]) => void
}

export const TeamDisplayContext = createContext<TeamDisplay | null>(null)

export function useTeamDisplay(): TeamDisplay | null {
  return useContext(TeamDisplayContext)
}

/** Move `id` before or after `targetId` in `order` (members only; the lead is never in it). */
export function moveInOrder(order: string[], id: string, targetId: string, after: boolean): string[] {
  if (id === targetId) return order
  const rest = order.filter((x) => x !== id)
  const at = rest.indexOf(targetId)
  if (at < 0) return order
  rest.splice(after ? at + 1 : at, 0, id)
  return rest
}
