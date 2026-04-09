import { useCallback, useMemo } from 'react'
import { useTabStore } from '../../stores/useTabStore'
import { useAgentStore } from '../../stores/useAgentStore'
import type { AgentStatus } from '../../stores/useAgentStore'
import { getWorkspaceCompositeKeys, aggregateStatus } from './workspace-indicators'

interface WorkspaceIndicators {
  unreadCount: number
  aggregatedStatus: AgentStatus | undefined
}

export function useWorkspaceIndicators(tabIds: string[]): WorkspaceIndicators {
  const tabs = useTabStore((s) => s.tabs)

  const compositeKeys = useMemo(
    () => getWorkspaceCompositeKeys(tabIds, tabs),
    [tabIds, tabs],
  )

  const unreadCount = useAgentStore(
    useCallback(
      (s: { unread: Record<string, boolean> }) =>
        compositeKeys.reduce((n, k) => n + (s.unread[k] ? 1 : 0), 0),
      [compositeKeys],
    ),
  )

  const aggregatedStatus = useAgentStore(
    useCallback(
      (s: { statuses: Record<string, AgentStatus> }) =>
        aggregateStatus(compositeKeys.map((k) => s.statuses[k])),
      [compositeKeys],
    ),
  )

  return { unreadCount, aggregatedStatus }
}
