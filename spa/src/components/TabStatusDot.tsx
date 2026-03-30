// spa/src/components/TabStatusDot.tsx
import type { AgentStatus } from '../stores/useAgentStore'

export type TabIndicatorStyle = 'overlay' | 'replace' | 'inline'

interface Props {
  status: AgentStatus | undefined
  style: TabIndicatorStyle
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

  // breathe CSS vars: --breathe-color = dot color, --breathe-bg = tab bg
  const breatheBg = isActive
    ? 'var(--surface-active)'
    : 'var(--surface-secondary)'

  if (style === 'overlay') {
    return (
      <span
        data-testid="tab-status-dot"
        className={`rounded-full flex-shrink-0 ${isRunning ? 'animate-breathe' : ''}`}
        style={{
          width: '6px',
          height: '6px',
          position: 'absolute',
          top: 0,
          right: '-1px',
          backgroundColor: color,
          boxShadow: `0 0 0 1.5px ${breatheBg}`,
          '--breathe-color': color,
          '--breathe-bg': breatheBg,
        } as React.CSSProperties}
      />
    )
  }

  if (style === 'replace') {
    return (
      <span
        data-testid="tab-status-dot"
        className={`rounded-full flex-shrink-0 ${isRunning ? 'animate-breathe' : ''}`}
        style={{
          width: '8px',
          height: '8px',
          backgroundColor: color,
          '--breathe-color': color,
          '--breathe-bg': breatheBg,
        } as React.CSSProperties}
      />
    )
  }

  // inline
  return (
    <span
      data-testid="tab-status-dot"
      className={`rounded-full flex-shrink-0 ${isRunning ? 'animate-breathe' : ''}`}
      style={{
        width: '6px',
        height: '6px',
        backgroundColor: color,
        '--breathe-color': color,
        '--breathe-bg': breatheBg,
      } as React.CSSProperties}
    />
  )
}
