// spa/src/components/SessionStatusBadge.tsx
import type { AgentStatus } from '../stores/useAgentStore'

const STATUS_COLORS: Record<AgentStatus, string> = {
  running: 'bg-green-400',
  waiting: 'bg-yellow-400',
  idle: 'bg-gray-500',
}

interface Props {
  status: AgentStatus | undefined
}

export default function SessionStatusBadge({ status }: Props) {
  if (!status) return null
  return (
    <span
      data-testid="status-badge"
      className={`inline-block w-2 h-2 rounded-full ${STATUS_COLORS[status] || 'bg-border-default'}`}
      title={status}
    />
  )
}
