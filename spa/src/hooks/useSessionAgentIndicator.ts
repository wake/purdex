import { useMemo, type ComponentType } from 'react'
import type { AgentStatus, SubagentRef } from '../stores/useAgentStore'
import type { TabIndicatorStyle } from '../stores/useUISettingsStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { compositeKey } from '../lib/composite-key'
import { getAgentIcon } from '../lib/agent-icons'
import { executionIdOfAgentCode } from '../lib/nex/worker-agent-status'
import { useActiveMemberKeys } from './useActiveMemberKeys'
import { awaitingKeys, listAwaitingFlags, liveAwaitingFlags } from './useTabAgentAggregate'

export type TabIconComponent = ComponentType<{ size: number; className?: string }>

const EMPTY_SUBAGENT_REFS: SubagentRef[] = []

export interface SessionAgentIndicator {
  agentIcon: TabIconComponent | undefined
  agentStatus: AgentStatus | undefined
  subagentRefs: SubagentRef[]
  isUnread: boolean
  /** 「等待核准」: this session is a worker waiting on a permission request. The same projection as the tab bar's `isAwaitingApproval` (the host's execution list row, the live summary where the list is cut short). */
  isAwaitingApproval: boolean
  tabIndicatorStyle: TabIndicatorStyle
}

/**
 * Resolves the agent-layer indicator (icon/status/subagents/unread + the user's
 * tabIndicatorStyle) for a single (hostId, sessionCode). Shared by useTabDisplay
 * (tab bar) and the New-Tab Sessions list so both render an identical prefix.
 */
export function useSessionAgentIndicator(
  hostId: string,
  sessionCode: string | undefined,
  opts?: { isTerminated?: boolean },
): SessionAgentIndicator {
  const isTerminated = opts?.isTerminated ?? false
  const ck = sessionCode && hostId ? compositeKey(hostId, sessionCode) : undefined

  const agentStatus = useAgentStore((s) => (ck ? s.statuses[ck] : undefined))
  // A team member's unread mark is hidden, its state kept (member spec R3); status is not touched.
  const members = useActiveMemberKeys()
  const isUnread = useAgentStore((s) => (ck ? !!s.unread[ck] && !members.has(ck) : false))
  const subagentRefs = useAgentStore((s) => (ck ? (s.subagents[ck] ?? EMPTY_SUBAGENT_REFS) : EMPTY_SUBAGENT_REFS))
  const agentType = useAgentStore((s) => (ck ? s.agentTypes[ck] : undefined))
  const tabIndicatorStyle = useUISettingsStore((s) => s.tabIndicatorStyle)
  const ccIconVariant = useUISettingsStore((s) => s.ccIconVariant)
  const codexIconVariant = useUISettingsStore((s) => s.codexIconVariant)

  const agentIcon = !isTerminated && agentType
    ? (getAgentIcon(agentType, { ccVariant: ccIconVariant, codexVariant: codexIconVariant }) as TabIconComponent | undefined)
    : undefined

  // A worker's session code is `exec-<executionId>`; the tab bar decides its hand from the same list row (useTabAgentAggregate), so the very same helpers decide it here.
  const executionId = sessionCode ? executionIdOfAgentCode(sessionCode) : null
  const panes = useMemo(() => (ck && executionId !== null ? [{ key: ck, hostId, executionId }] : []), [ck, hostId, executionId])
  const listFlags = useExecutionListStore((s) => listAwaitingFlags(s.byHost, panes))
  const liveFlags = useExecutionStore((s) => (listFlags.includes('L') ? liveAwaitingFlags(s.executions, panes) : ''))
  const isAwaitingApproval = awaitingKeys(panes, listFlags, liveFlags).length > 0

  return { agentIcon, agentStatus, subagentRefs, isUnread, isAwaitingApproval, tabIndicatorStyle }
}
