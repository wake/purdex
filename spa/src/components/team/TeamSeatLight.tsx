// spa/src/components/team/TeamSeatLight.tsx — a seat's light as the corner badge of its avatar (team panel v2, NA-0 design): an 11 px dot with a 2 px ring in the row's
// background. The same status, unread tint and "lights off" rule as the tab rows (TabStatusIndicator, TabIcon); a seat with no known status shows no dot.
import { STATUS_COLORS } from '../TabStatusIndicator'
import { useSessionAgentIndicator } from '../../hooks/useSessionAgentIndicator'

const SIZE = 11
const UNREAD_COLOR = '#ef4444'

/** The ring is the row's background: the row sets `--seat-ring` for its normal, hover and active looks. */
export function TeamSeatLight({ hostId, sessionCode }: { hostId: string; sessionCode: string }) {
  const { agentStatus, isUnread, tabIndicatorStyle } = useSessionAgentIndicator(hostId, sessionCode)
  if (agentStatus === undefined || tabIndicatorStyle === 'icon') return null // lights off: the icon-only setting
  const tinted = isUnread && agentStatus !== 'waiting' && agentStatus !== 'error' // unread never overrides ask or error
  return (
    <span
      data-testid="team-panel-light-dot"
      data-status={agentStatus}
      className={`block rounded-full ${agentStatus === 'running' && !tinted ? 'animate-breathe' : ''}`}
      style={{ width: SIZE, height: SIZE, backgroundColor: tinted ? UNREAD_COLOR : STATUS_COLORS[agentStatus], boxShadow: '0 0 0 2px var(--seat-ring, var(--surface-secondary))' }}
    />
  )
}
