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

/**
 * How a group is drawn on the TabBar (the user compares these). Every style starts the group with the
 * team-name label; they differ in how the group's tabs are tied to it.
 */
export type TeamGroupStyle = 'label' | 'dot' | 'endcap' | 'gap' | 'sepcolor' | 'rule' | 'combo' | 'tint' | 'frame' | 'topbar' | 'plate'
  | 'corner-tr' | 'corner-br' | 'corner-tr-icon' | 'corner-br-icon'
  | 'badge-icon' | 'badge-disc' | 'edge-arc' | 'edge-short'

/** The low-key cues a group style turns on (the older four styles use none of them). */
export type TeamGroupCue = 'dot' | 'endcap' | 'gap' | 'sepcolor' | 'rule'

const GROUP_CUES: Partial<Record<TeamGroupStyle, TeamGroupCue[]>> = {
  dot: ['dot'], endcap: ['endcap'], gap: ['gap'], sepcolor: ['sepcolor'], rule: ['rule'], combo: ['dot', 'gap', 'endcap'],
}

/** The folded-corner styles: a team-colored diagonal corner on each tab (optionally with an inverted member icon). */
export function groupCorner(style: TeamGroupStyle): { pos: 'tr' | 'br'; icon: boolean } | null {
  switch (style) {
    case 'corner-tr': return { pos: 'tr', icon: false }
    case 'corner-br': return { pos: 'br', icon: false }
    case 'corner-tr-icon': return { pos: 'tr', icon: true }
    case 'corner-br-icon': return { pos: 'br', icon: true }
    default: return null
  }
}

/** The corner-badge styles: a member icon straddling a tab's top-right corner (bare team-colored icon, or a team-colored disc with a knocked-out icon). */
export function groupBadge(style: TeamGroupStyle): 'icon' | 'disc' | null {
  return style === 'badge-icon' ? 'icon' : style === 'badge-disc' ? 'disc' : null
}

/** Corner size (small / default / large); the icon variants are drawn bigger to hold the icon. */
export type TeamCornerSize = 'sm' | 'md' | 'lg'

/** The glyph in a corner badge ('user' is the old v5b one, kept as a comparison). */
export type TeamBadgeIcon = 'bookmark' | 'users' | 'hexagon' | 'diamond' | 'dot' | 'letter' | 'user'

/** How much of the bookmark's top is cut off (a short hanging ribbon), and where it hangs along the tab's top edge. */
export type TeamBookmarkCut = 'third' | 'half'
export type TeamBookmarkPos = 'before-x' | 'above-left' | 'above-right'

/** Right-edge line thickness in px. */
export type TeamEdgeWidth = 1.5 | 2

/** The right-edge styles: a team-colored line on the tab's right edge, either following the corner radius (arc) or only the middle (short). */
export function groupEdge(style: TeamGroupStyle): 'arc' | 'short' | null {
  return style === 'edge-arc' ? 'arc' : style === 'edge-short' ? 'short' : null
}

/** Where the hook's top starts: exactly at the lead highlight's lower edge, or fused into the highlight. */
export type TeamHookTop = 'below' | 'blend'

export function groupHasCue(style: TeamGroupStyle, cue: TeamGroupCue): boolean {
  return GROUP_CUES[style]?.includes(cue) ?? false
}

/** How the sidebar folds the member beads: a caret/sign in front of the lead row, or the Users icon + dots row. */
export type TeamCollapseStyle = 'sign' | 'users'

/** The drawing of the hook that hangs the bead rows under the lead (every one runs down to the last row). */
export type TeamHookStyle = 'glyph' | 'thin' | 'bold' | 'rail'

/** How a bead whose member has a tab open is told apart from one without (no fading either way). */
export type TeamOpenMark = 'none' | 'tick'

/** How the sidebar shows that the beads hang under the lead (no left border line). */
export type TeamSidebarStyle = 'hook' | 'plusminus' | 'chevron'

/** How one tab sits in a group on the TabBar. */
export interface TeamTabMark {
  teamKey: string
  color: string
  /** What the group label says: the team name, or the fallback when the team has none. */
  label: string
  /** The team has no name of its own (the label is a fallback). */
  unnamed: boolean
  role: 'lead' | 'member'
  style: TeamGroupStyle
  /** First / last visible tab of the group. */
  first: boolean
  last: boolean
  collapsed: boolean
  /** Member tabs hidden by the collapse (shown on the label). */
  hiddenCount: number
}

/** A lead whose tab is closed while its team runs on (sidebar shows it faded, as the way back in). */
export interface TeamGhostLead {
  teamKey: string
  color: string
  label: string
  unnamed: boolean
  lead: TeamSeatView
  members: TeamSeatView[]
}

export interface TeamDisplay {
  activeTabId: string | null
  /** Show the host badge next to each bead (a user setting). */
  beadHost: boolean
  groupStyle: TeamGroupStyle
  sidebarStyle: TeamSidebarStyle
  collapseStyle: TeamCollapseStyle
  hookStyle: TeamHookStyle
  hookTop: TeamHookTop
  cornerSize: TeamCornerSize
  badgeIcon: TeamBadgeIcon
  edgeWidth: TeamEdgeWidth
  bookmarkCut: TeamBookmarkCut
  bookmarkPos: TeamBookmarkPos
  openMark: TeamOpenMark
  tabMark: (tabId: string) => TeamTabMark | null
  /** Member tabs folded into the bead row (their lead row is in the same list). */
  sidebarHidden: (tabId: string) => boolean
  /** Beads under a lead row, in team order. */
  sidebarBeads: (tabId: string) => { teamKey: string; color: string; label: string; unnamed: boolean; collapsed: boolean; members: TeamSeatView[] } | null
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
