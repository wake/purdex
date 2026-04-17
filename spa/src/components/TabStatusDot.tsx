// spa/src/components/TabStatusDot.tsx
import type { AgentStatus } from '../stores/useAgentStore'

/** Visual style of the dot itself — orthogonal to the tab-level indicator mode. */
export type DotStyle = 'overlay' | 'replace'

interface Props {
  status: AgentStatus | undefined
  style: DotStyle
  isActive: boolean
}

const STATUS_COLORS: Record<AgentStatus, string> = {
  running: '#4ade80',
  waiting: '#facc15',
  idle: '#6b7280',
  error: '#ef4444',
}

export function TabStatusDot({ status, style, isActive }: Props) {
  if (status === undefined) return null

  const color = STATUS_COLORS[status]
  const isRunning = status === 'running'

  if (style === 'overlay') {
    const ringColor = isActive
      ? 'var(--surface-active)'
      : 'var(--surface-secondary)'
    return (
      <span
        data-testid="tab-status-dot"
        className={`rounded-full flex-shrink-0 ${isRunning ? 'animate-breathe' : ''}`}
        style={{
          width: '6px',
          height: '6px',
          position: 'absolute',
          top: 0,
          right: 0,
          backgroundColor: color,
          boxShadow: `0 0 0 1.5px ${ringColor}`,
        }}
      />
    )
  }

  // replace
  return (
    <span
      data-testid="tab-status-dot"
      className={`rounded-full flex-shrink-0 ${isRunning ? 'animate-breathe' : ''}`}
      style={{
        width: '8px',
        height: '8px',
        backgroundColor: color,
      }}
    />
  )
}
