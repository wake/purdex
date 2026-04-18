import type { ComponentType } from 'react'
import type { Tab } from '../types/tab'
import type { AgentStatus, TabIndicatorStyle } from '../stores/useAgentStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useI18nStore } from '../stores/useI18nStore'
import { getPrimaryPane } from '../lib/pane-tree'
import { getPaneIcon, getPaneLabel } from '../lib/pane-labels'
import { compositeKey } from '../lib/composite-key'
import { getAgentIcon } from '../lib/agent-icons'
import { ICON_MAP } from '../components/tab-icon-map'
import type { Session } from '../lib/host-api'

const EMPTY_SESSIONS: Session[] = []

export type TabIconComponent = ComponentType<{ size: number; className?: string }>

export interface TabDisplayData {
  displayTitle: string
  tooltip: string
  IconComponent: TabIconComponent | undefined
  agentStatus: AgentStatus | undefined
  isUnread: boolean
  subagentCount: number
  tabIndicatorStyle: TabIndicatorStyle
  isHostOffline: boolean
  isTerminated: boolean
  hostId: string
  sessionCode: string | undefined
  compositeKey: string | undefined
}

interface Options {
  /** Override the base label; OSC still takes precedence when active. */
  titleOverride?: string
}

/**
 * Shared tab display state for InlineTab (activity bar) and SortableTab (top
 * TabBar). Centralises OSC title resolution, agent-icon fallback, host-offline
 * detection, and agent store reads so both surfaces render consistently.
 */
export function useTabDisplay(tab: Tab, options?: Options): TabDisplayData {
  const t = useI18nStore((s) => s.t)
  const primaryContent = getPrimaryPane(tab.layout).content
  const hostId = primaryContent.kind === 'tmux-session' ? primaryContent.hostId : ''
  const sessionCode = primaryContent.kind === 'tmux-session' ? primaryContent.sessionCode : undefined
  const ck = sessionCode && hostId ? compositeKey(hostId, sessionCode) : undefined
  const isTerminated = primaryContent.kind === 'tmux-session' && !!primaryContent.terminated

  const sessions = useSessionStore((s) => (hostId ? s.sessions[hostId] : undefined) ?? EMPTY_SESSIONS)
  const workspaces = useWorkspaceStore((s) => s.workspaces)

  const agentStatus = useAgentStore((s) => (ck ? s.statuses[ck] : undefined))
  const isUnread = useAgentStore((s) => (ck ? !!s.unread[ck] : false))
  const subagentCount = useAgentStore((s) => (ck ? (s.subagents[ck]?.length ?? 0) : 0))
  const agentType = useAgentStore((s) => (ck ? s.agentTypes[ck] : undefined))
  const tabIndicatorStyle = useAgentStore((s) => s.tabIndicatorStyle)
  const ccIconVariant = useAgentStore((s) => s.ccIconVariant)
  const showOscTitle = useAgentStore((s) => s.showOscTitle)
  const oscTitle = useAgentStore((s) => (ck ? s.oscTitles[ck] : undefined))

  const isHostOffline = useHostStore((s) => {
    if (!hostId || isTerminated) return false
    const rt = s.runtime[hostId]
    return rt ? rt.status !== 'connected' : false
  })

  const iconName = getPaneIcon(primaryContent)
  const paneIcon = ICON_MAP[iconName]
  const agentIcon = !isTerminated && agentType ? getAgentIcon(agentType, { ccVariant: ccIconVariant }) : undefined
  const IconComponent = (agentIcon ?? paneIcon) as TabIconComponent | undefined

  let baseLabel: string
  if (options?.titleOverride !== undefined) {
    baseLabel = options.titleOverride
  } else {
    const sessionLookup = { getByCode: (code: string) => sessions.find((sess) => sess.code === code) }
    const workspaceLookup = { getById: (id: string) => workspaces.find((w) => w.id === id) }
    baseLabel = getPaneLabel(primaryContent, sessionLookup, workspaceLookup, t)
  }

  const useOsc = showOscTitle && !isTerminated && !!agentType && !!oscTitle
  const displayTitle = useOsc && oscTitle ? oscTitle : baseLabel
  const tooltip = useOsc && oscTitle ? `${oscTitle} - ${baseLabel}` : baseLabel

  return {
    displayTitle,
    tooltip,
    IconComponent,
    agentStatus,
    isUnread,
    subagentCount,
    tabIndicatorStyle,
    isHostOffline,
    isTerminated,
    hostId,
    sessionCode,
    compositeKey: ck,
  }
}
