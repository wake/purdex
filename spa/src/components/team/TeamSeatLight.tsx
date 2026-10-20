// spa/src/components/team/TeamSeatLight.tsx — a seat's light on its agent icon (team panel v2). It sits on the same 16 px icon as the tab rows' dot, so it is drawn from the tab rows' overlay dot (`overlay-dot.ts`, shared with TabStatusIndicator):
// the same size, ring width and place (`SEAT_LIGHT_POSITION`, which the wrappers in the team row and the personal card put on their absolute box). The same status, unread tint and "lights off" rule as the tab rows
// (TabStatusIndicator, TabIcon); a seat with no known status shows no dot, and an error is the tab rows' warning diamond rather than a dot.
import { STATUS_COLORS } from '../status-colors'
import { WarningDiamond } from '@phosphor-icons/react'
import { OVERLAY_DOT, OVERLAY_ERROR, OVERLAY_RING } from '../overlay-dot'
import { useSessionAgentIndicator } from '../../hooks/useSessionAgentIndicator'

const UNREAD_COLOR = '#ef4444'

/** The ring is the row's background: the row sets `--seat-ring` for its normal, hover and active looks. */
export function TeamSeatLight({ hostId, sessionCode }: { hostId: string; sessionCode: string }) {
  const { agentStatus, isUnread, tabIndicatorStyle } = useSessionAgentIndicator(hostId, sessionCode)
  if (agentStatus === undefined || tabIndicatorStyle === 'icon') return null // lights off: the icon-only setting
  const tinted = isUnread && agentStatus !== 'waiting' && agentStatus !== 'error' // unread never overrides ask or error
  if (agentStatus === 'error') {
    // An error is the tab rows' warning diamond, not a dot. The box this sits in is already at the dot's place, so the diamond is offset by what separates the two places.
    return (
      <WarningDiamond
        data-testid="team-panel-light-error"
        size={OVERLAY_ERROR.size}
        weight="fill"
        color={STATUS_COLORS.error}
        style={{ position: 'absolute', top: OVERLAY_ERROR.top - OVERLAY_DOT.top, right: OVERLAY_ERROR.right - OVERLAY_DOT.right, filter: 'drop-shadow(0 0 1px var(--seat-ring, var(--surface-secondary)))' }}
      />
    )
  }
  return (
    <span
      data-testid="team-panel-light-dot"
      data-status={agentStatus}
      className={`block rounded-full ${agentStatus === 'running' && !tinted ? 'animate-breathe' : ''}`}
      style={{ width: OVERLAY_DOT.size, height: OVERLAY_DOT.size, backgroundColor: tinted ? UNREAD_COLOR : STATUS_COLORS[agentStatus], boxShadow: `0 0 0 ${OVERLAY_RING}px var(--seat-ring, var(--surface-secondary))` }}
    />
  )
}
