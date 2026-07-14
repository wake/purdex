import type { Tab } from '../types/tab'
import type { AgentStatus, SubagentRef } from '../stores/useAgentStore'
import type { TabIndicatorStyle } from '../stores/useUISettingsStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useI18nStore } from '../stores/useI18nStore'
import { getPrimaryPane } from '../lib/pane-tree'
import { getPaneIcon, getPaneLabel } from '../lib/pane-labels'
import { compositeKey } from '../lib/composite-key'
import { ICON_MAP } from '../components/tab-icon-map'
import type { Session } from '../lib/host-api'
import { useSessionAgentIndicator } from './useSessionAgentIndicator'
import type { TabIconComponent } from './useSessionAgentIndicator'

const EMPTY_SESSIONS: Session[] = []

export type { TabIconComponent }

export interface TabDisplayData {
  displayTitle: string
  IconComponent: TabIconComponent | undefined
  agentStatus: AgentStatus | undefined
  isUnread: boolean
  subagentCount: number
  subagentRefs: SubagentRef[]
  tabIndicatorStyle: TabIndicatorStyle
  isHostOffline: boolean
  isTerminated: boolean
}

/**
 * Shared tab display state for InlineTab (activity bar) and SortableTab (top
 * TabBar). Centralises label + agent title resolution, agent-icon fallback,
 * host-offline detection, and agent store reads so both surfaces render
 * identically.
 */
export function useTabDisplay(tab: Tab): TabDisplayData {
  const t = useI18nStore((s) => s.t)
  const primaryContent = getPrimaryPane(tab.layout).content
  const hostId = primaryContent.kind === 'tmux-session' ? primaryContent.hostId : ''
  const sessionCode = primaryContent.kind === 'tmux-session' ? primaryContent.sessionCode : undefined
  const ck = sessionCode && hostId ? compositeKey(hostId, sessionCode) : undefined
  const isTerminated = primaryContent.kind === 'tmux-session' && !!primaryContent.terminated

  const sessions = useSessionStore((s) => (hostId ? s.sessions[hostId] : undefined) ?? EMPTY_SESSIONS)
  const workspaces = useWorkspaceStore((s) => s.workspaces)

  const { agentIcon, agentStatus, subagentRefs, isUnread, tabIndicatorStyle } =
    useSessionAgentIndicator(hostId, sessionCode, { isTerminated })
  const subagentCount = subagentRefs.length
  const agentType = useAgentStore((s) => (ck ? s.agentTypes[ck] : undefined))
  const dynamicTabName = useUISettingsStore((s) => s.dynamicTabName)

  const isHostOffline = useHostStore((s) => {
    if (!hostId || isTerminated) return false
    const rt = s.runtime[hostId]
    return rt ? rt.status !== 'connected' : false
  })

  const iconName = getPaneIcon(primaryContent)
  const paneIcon = ICON_MAP[iconName]
  const IconComponent = (agentIcon ?? paneIcon) as TabIconComponent | undefined

  const sessionLookup = { getByCode: (code: string) => sessions.find((sess) => sess.code === code) }
  const workspaceLookup = { getById: (id: string) => workspaces.find((w) => w.id === id) }
  const baseLabel = getPaneLabel(primaryContent, sessionLookup, workspaceLookup, t)
  const session = sessionCode ? sessionLookup.getByCode(sessionCode) : undefined

  const paneTitle = dynamicTabName && !isTerminated && !!agentType ? session?.pane_title : undefined
  const displayTitle = paneTitle ? `${paneTitle} - ${baseLabel}` : baseLabel

  return {
    displayTitle,
    IconComponent,
    agentStatus,
    isUnread,
    subagentCount,
    subagentRefs,
    tabIndicatorStyle,
    isHostOffline,
    isTerminated,
  }
}
