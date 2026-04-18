// spa/src/components/TabStatusIndicator.tsx
import { WarningDiamond } from '@phosphor-icons/react'
import type { AgentStatus } from '../stores/useAgentStore'

/** Render mode — orthogonal to the tab-level indicator style. */
export type IndicatorRenderMode = 'overlay' | 'replace'

interface Props {
  status: AgentStatus | undefined
  mode: IndicatorRenderMode
  isActive: boolean
  isUnread?: boolean
}

const STATUS_COLORS: Record<AgentStatus, string> = {
  running: '#4ade80',
  waiting: '#facc15',
  idle: '#6b7280',
  error: '#ef4444',
}

const UNREAD_COLOR = '#ef4444'

export function TabStatusIndicator({ status, mode, isActive, isUnread = false }: Props) {
  if (status === undefined) return null

  const isRunning = status === 'running'
  const isError = status === 'error'

  if (mode === 'overlay') {
    const ringColor = isActive
      ? 'var(--surface-active)'
      : 'var(--surface-secondary)'

    if (isError) {
      return (
        <WarningDiamond
          data-testid="tab-status-error"
          size={10}
          weight="duotone"
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

    const color = isUnread ? UNREAD_COLOR : STATUS_COLORS[status]
    return (
      <span
        data-testid="tab-status-indicator"
        className={`rounded-full flex-shrink-0 ${isRunning ? 'animate-breathe' : ''}`}
        style={{
          width: '6px',
          height: '6px',
          position: 'absolute',
          top: -1,
          right: -2,
          backgroundColor: color,
          boxShadow: `0 0 0 1.5px ${ringColor}`,
        }}
      />
    )
  }

  // replace mode (dot-only / icon+dot)
  if (isError) {
    return (
      <WarningDiamond
        data-testid="tab-status-error"
        size={14}
        weight="duotone"
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
        width: '8px',
        height: '8px',
        backgroundColor: STATUS_COLORS[status],
      }}
    />
  )
}
