// spa/src/components/team/TeamSeatLight.tsx — a seat's light on its agent icon (team panel v2). It sits on the same 16 px icon as the tab rows' dot, so it is drawn from the tab rows' overlay dot (`overlay-dot.ts`, shared with TabStatusIndicator):
// the same size, ring width and place (`SEAT_LIGHT_POSITION`, which the wrappers in the team row and the personal card put on their absolute box). The same status, unread tint and "lights off" rule as the tab rows
// (TabStatusIndicator, TabIcon); a seat with no known status shows no dot, and an error is the tab rows' warning diamond rather than a dot.
// 「等待核准」 (#3830) is drawn as the tab row draws it: a worker waiting on a permission request is a waiting dot whatever its status says (it may not have caught up), with the warning-coloured hand to the dot's left,
// the words as tooltip and accessible name. As in the tab row it is a must-show exception to the icon-only setting, and unread never tints it.
import { STATUS_COLORS } from '../status-colors'
import { HandPalm, WarningDiamond } from '@phosphor-icons/react'
import { OVERLAY_DOT, OVERLAY_ERROR, OVERLAY_HAND_RIGHT, OVERLAY_HAND_SIZE, OVERLAY_RING } from '../overlay-dot'
import { useSessionAgentIndicator } from '../../hooks/useSessionAgentIndicator'
import { useI18nStore } from '../../stores/useI18nStore'

const UNREAD_COLOR = '#ef4444'

/** The ring is the row's background: the row sets `--seat-ring` for its normal, hover and active looks. */
export function TeamSeatLight({ hostId, sessionCode }: { hostId: string; sessionCode: string }) {
  const t = useI18nStore((s) => s.t)
  const { agentStatus: reported, isUnread, isAwaitingApproval, tabIndicatorStyle } = useSessionAgentIndicator(hostId, sessionCode)
  const lightsOff = tabIndicatorStyle === 'icon'
  const agentStatus = isAwaitingApproval ? 'waiting' : reported
  if (agentStatus === undefined || (lightsOff && !isAwaitingApproval)) return null // lights off: the icon-only setting (an awaiting worker is the one exception, as in the tab row)
  const tinted = isUnread && !lightsOff && agentStatus !== 'waiting' && agentStatus !== 'error' // unread never overrides ask or error
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
  const dot = (
    <span
      data-testid="team-panel-light-dot"
      data-status={agentStatus}
      className={`block rounded-full ${agentStatus === 'running' && !tinted ? 'animate-breathe' : ''}`}
      style={{ width: OVERLAY_DOT.size, height: OVERLAY_DOT.size, backgroundColor: tinted ? UNREAD_COLOR : STATUS_COLORS[agentStatus], boxShadow: `0 0 0 ${OVERLAY_RING}px var(--seat-ring, var(--surface-secondary))` }}
    />
  )
  if (!isAwaitingApproval) return dot
  const label = t('executions.activity.awaiting_approval')
  // `display: contents`: the wrapper makes no box, so the dot and the hand both sit against the row's light box exactly as the bare dot does, while hovering either one still finds the wrapper's title.
  // The light's box is already at the dot's place, so the hand is offset by what separates the two places (as the diamond is).
  return (
    <span data-testid="team-panel-light-awaiting" role="img" aria-label={label} title={label} style={{ display: 'contents' }}>
      {dot}
      <HandPalm
        data-testid="team-panel-light-hand"
        size={OVERLAY_HAND_SIZE}
        weight="fill"
        color={STATUS_COLORS.waiting}
        aria-hidden="true"
        style={{ position: 'absolute', top: 0, right: OVERLAY_HAND_RIGHT - OVERLAY_DOT.right, filter: 'drop-shadow(0 0 1px var(--seat-ring, var(--surface-secondary)))' }}
      />
    </span>
  )
}
