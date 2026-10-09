// spa/src/components/TabStatusIndicator.tsx
import { HandPalm, WarningDiamond } from '@phosphor-icons/react'
import type { AgentStatus } from '../stores/useAgentStore'
import { useI18nStore } from '../stores/useI18nStore'

/** Render mode — orthogonal to the tab-level indicator style. */
export type IndicatorRenderMode = 'overlay' | 'replace'

interface Props {
  status: AgentStatus | undefined
  mode: IndicatorRenderMode
  isActive: boolean
  isUnread?: boolean
  /**
   * A live worker waiting on a permission request (useTabDisplay's `isAwaitingApproval`): a waiting light — whatever
   * `status` says, which may not have caught up yet — plus a warning-coloured HandPalm, with 「等待核准」 as the
   * tooltip and accessible name. The light keeps its exact geometry (a request arriving or being answered never
   * moves or resizes it): overlay keeps the waiting dot and adds the hand immediately to its left; replace draws the
   * hand inside the dot's own 8×8 slot. Unread never overrides ask: in overlay (single-dot) the waiting dot stays yellow even when unread (unread tints
   * only running / idle); replace shows the caller's separate pip. The hand itself stays the warning colour.
   */
  awaitingApproval?: boolean
}

const STATUS_COLORS: Record<AgentStatus, string> = {
  running: '#4ade80',
  waiting: '#facc15',
  idle: '#6b7280',
  error: '#ef4444',
}

const UNREAD_COLOR = '#ef4444'

/** The overlay dot's box, in px from the icon slot's top-right corner. */
const OVERLAY_DOT = { size: 6, top: -1, right: -2 }
/** The 「等待核准」 hand in overlay mode: this size, 1px left of the dot, on the dot's top line. */
const OVERLAY_HAND_SIZE = 8
const OVERLAY_HAND_RIGHT = OVERLAY_DOT.right + OVERLAY_DOT.size + 1
/** The replace-mode slot (the dot itself, or the hand drawn in its place). */
const REPLACE_SLOT = 8

export function TabStatusIndicator({ status, mode, isActive, isUnread = false, awaitingApproval = false }: Props) {
  const t = useI18nStore((s) => s.t)
  const shown: AgentStatus | undefined = awaitingApproval ? 'waiting' : status
  if (shown === undefined) return null

  const isRunning = shown === 'running'
  const isError = shown === 'error'
  const awaitingLabel = awaitingApproval ? t('executions.activity.awaiting_approval') : ''

  if (mode === 'overlay') {
    const ringColor = isActive
      ? 'var(--surface-active)'
      : 'var(--surface-secondary)'

    if (isError) {
      return (
        <WarningDiamond
          data-testid="tab-status-error"
          size={10}
          weight="fill"
          color={STATUS_COLORS.error}
          style={{
            position: 'absolute',
            top: -2,
            right: -3,
            filter: `drop-shadow(0 0 1px ${ringColor})`,
          }}
        />
      )
    }

    // Unread never overrides ask: a waiting dot stays yellow; unread tints only running / idle.
    const tintUnread = isUnread && shown !== 'waiting'
    const color = tintUnread ? UNREAD_COLOR : STATUS_COLORS[shown]
    const dot = (
      <span
        data-testid="tab-status-indicator"
        className={`rounded-full flex-shrink-0 ${isRunning && !tintUnread ? 'animate-breathe' : ''}`}
        style={{
          width: `${OVERLAY_DOT.size}px`,
          height: `${OVERLAY_DOT.size}px`,
          position: 'absolute',
          top: OVERLAY_DOT.top,
          right: OVERLAY_DOT.right,
          backgroundColor: color,
          boxShadow: `0 0 0 1.5px ${ringColor}`,
        }}
      />
    )
    if (!awaitingApproval) return dot

    // `display: contents`: the wrapper makes no box, so the dot and the hand both position against the icon slot
    // exactly as the bare dot does — while hovering either one still finds the wrapper's title.
    return (
      <span
        data-testid="tab-status-awaiting"
        role="img"
        aria-label={awaitingLabel}
        title={awaitingLabel}
        style={{ display: 'contents' }}
      >
        {dot}
        <HandPalm
          data-testid="tab-status-awaiting-hand"
          size={OVERLAY_HAND_SIZE}
          weight="fill"
          color={STATUS_COLORS.waiting}
          aria-hidden="true"
          style={{
            position: 'absolute',
            top: OVERLAY_DOT.top,
            right: OVERLAY_HAND_RIGHT,
            filter: `drop-shadow(0 0 1px ${ringColor})`,
          }}
        />
      </span>
    )
  }

  // replace mode (dot-only / icon+dot)
  if (awaitingApproval) {
    return (
      <span
        data-testid="tab-status-awaiting"
        role="img"
        aria-label={awaitingLabel}
        title={awaitingLabel}
        className="inline-flex flex-shrink-0"
        style={{ width: `${REPLACE_SLOT}px`, height: `${REPLACE_SLOT}px` }}
      >
        <HandPalm
          data-testid="tab-status-awaiting-hand"
          size={REPLACE_SLOT}
          weight="fill"
          color={STATUS_COLORS.waiting}
          aria-hidden="true"
        />
      </span>
    )
  }

  if (isError) {
    return (
      <WarningDiamond
        data-testid="tab-status-error"
        size={14}
        weight="fill"
        color={STATUS_COLORS.error}
        className="flex-shrink-0"
      />
    )
  }

  return (
    <span
      data-testid="tab-status-indicator"
      className={`rounded-full flex-shrink-0 ${isRunning ? 'animate-breathe' : ''}`}
      style={{
        width: `${REPLACE_SLOT}px`,
        height: `${REPLACE_SLOT}px`,
        backgroundColor: STATUS_COLORS[shown],
      }}
    />
  )
}
