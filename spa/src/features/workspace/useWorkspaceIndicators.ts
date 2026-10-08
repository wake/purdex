import { useCallback, useMemo } from 'react'
import { useTabStore } from '../../stores/useTabStore'
import { useAgentStore } from '../../stores/useAgentStore'
import type { AgentStatus } from '../../stores/useAgentStore'
import { getWorkspaceTabKeys, aggregateStatus, type ActiveStatus } from './workspace-indicators'

interface WorkspaceIndicators {
  unreadCount: number
  aggregatedStatus: ActiveStatus | undefined
}

export function useWorkspaceIndicators(tabIds: string[]): WorkspaceIndicators {
  const tabs = useTabStore((s) => s.tabs)

  // one key array per tab: the workspace counts TABS with any unread agent pane, not panes (U1-3 plan review #2)
  const tabKeys = useMemo(
    () => getWorkspaceTabKeys(tabIds, tabs),
    [tabIds, tabs],
  )

  const unreadCount = useAgentStore(
    useCallback(
      (s: { unread: Record<string, boolean> }) =>
        tabKeys.reduce((n, keys) => n + (keys.some((k) => s.unread[k]) ? 1 : 0), 0),
      [tabKeys],
    ),
  )

  const aggregatedStatus = useAgentStore(
    useCallback(
      (s: { statuses: Record<string, AgentStatus> }) =>
        aggregateStatus(tabKeys.flatMap((keys) => keys.map((k) => s.statuses[k]))),
      [tabKeys],
    ),
  )

  return { unreadCount, aggregatedStatus }
}
