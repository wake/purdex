import type { ComponentType } from 'react'
import type { AgentStatus, SubagentRef } from '../stores/useAgentStore'
import type { TabIndicatorStyle } from '../stores/useUISettingsStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { compositeKey } from '../lib/composite-key'
import { getAgentIcon } from '../lib/agent-icons'

export type TabIconComponent = ComponentType<{ size: number; className?: string }>

const EMPTY_SUBAGENT_REFS: SubagentRef[] = []

export interface SessionAgentIndicator {
  agentIcon: TabIconComponent | undefined
  agentStatus: AgentStatus | undefined
  subagentRefs: SubagentRef[]
  isUnread: boolean
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
  const isUnread = useAgentStore((s) => (ck ? !!s.unread[ck] : false))
  const subagentRefs = useAgentStore((s) => (ck ? (s.subagents[ck] ?? EMPTY_SUBAGENT_REFS) : EMPTY_SUBAGENT_REFS))
  const agentType = useAgentStore((s) => (ck ? s.agentTypes[ck] : undefined))
  const tabIndicatorStyle = useUISettingsStore((s) => s.tabIndicatorStyle)
  const ccIconVariant = useUISettingsStore((s) => s.ccIconVariant)
  const codexIconVariant = useUISettingsStore((s) => s.codexIconVariant)

  const agentIcon = !isTerminated && agentType
    ? (getAgentIcon(agentType, { ccVariant: ccIconVariant, codexVariant: codexIconVariant }) as TabIconComponent | undefined)
    : undefined

  return { agentIcon, agentStatus, subagentRefs, isUnread, tabIndicatorStyle }
}
