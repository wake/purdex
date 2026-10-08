// spa/src/components/team/team-display.ts — what the tab surfaces need to draw lead/member groups.
//
// The sidebar tab list and the top TabBar read this context. With no provider (today's App) both
// render exactly as before; a provider (the team prototype now, the team UI PR later) turns on:
// member rows folded into a bead row under the lead row, the lead tab as a group head, collapse.
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
  /** tmux session code: the agent light is keyed by (hostId, code). */
  sessionCode: string
  role: 'lead' | 'member'
  /** The tab showing this seat, or null when it has none (an unopened member). */
  tabId: string | null
}

/** How one tab sits in a group on the TabBar. */
export interface TeamTabMark {
  teamKey: string
  color: string
  role: 'lead' | 'member'
  /** First / last visible tab of the group (the group underline is rounded at its ends). */
  first: boolean
  last: boolean
  collapsed: boolean
  /** Member tabs hidden by the collapse (only meaningful on the lead). */
  hiddenCount: number
}

/** A lead whose tab is closed while its team runs on (sidebar shows it faded, as the way back in). */
export interface TeamGhostLead {
  teamKey: string
  color: string
  lead: TeamSeatView
  members: TeamSeatView[]
}

export interface TeamDisplay {
  activeTabId: string | null
  /** Show the host badge next to each bead. */
  beadHost: boolean
  tabMark: (tabId: string) => TeamTabMark | null
  /** Member tabs folded into the bead row (their lead row is in the same list). */
  sidebarHidden: (tabId: string) => boolean
  /** Beads under a lead row, in team order. */
  sidebarBeads: (tabId: string) => { teamKey: string; color: string; members: TeamSeatView[] } | null
  ghostLeads: (workspaceId: string | null) => TeamGhostLead[]
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
