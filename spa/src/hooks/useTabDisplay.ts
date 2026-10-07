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
import { stripAgentTitleMarker } from '../lib/agent-title-marker'
import { compositeKey } from '../lib/composite-key'
import { ICON_MAP } from '../components/tab-icon-map'
import type { Session } from '../lib/host-api'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { useWorkerSettingsStore } from '../stores/useWorkerSettingsStore'
import { execAgentCode } from '../lib/nex/worker-agent-status'
import { isAwaitingApproval, liveWorkerSummary, rowWorkerSummary, workerTitleOf } from '../lib/nex/worker-summary'
import { selectSessionTitleSupported, useNexHostStore } from '../stores/useNexHostStore'
import { workerIcon } from '../lib/worker-icon'
import type { ExecutionSummary } from '../lib/nex/types'
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
  const exec = primaryContent.kind === 'execution' ? primaryContent : undefined
  // A worker tab's light lives under `exec-<id>` (useWorkerAgentProjection,
  // spec §8.1); its host resolves like ExecutionPaneWrapper's (hint, else the first host).
  const execHostId = useHostStore((s) => (exec ? exec.host || s.hostOrder[0] || '' : ''))
  const hostId = primaryContent.kind === 'tmux-session' ? primaryContent.hostId : execHostId
  const sessionCode = primaryContent.kind === 'tmux-session'
    ? primaryContent.sessionCode
    : exec ? execAgentCode(exec.executionId) : undefined
  const ck = sessionCode && hostId ? compositeKey(hostId, sessionCode) : undefined
  const isTerminated = primaryContent.kind === 'tmux-session' && !!primaryContent.terminated

  const sessions = useSessionStore((s) => (hostId ? s.sessions[hostId] : undefined) ?? EMPTY_SESSIONS)
  const workspaces = useWorkspaceStore((s) => s.workspaces)

  const { agentIcon, agentStatus, subagentRefs, isUnread, tabIndicatorStyle } =
    useSessionAgentIndicator(hostId, sessionCode, { isTerminated })
  const subagentCount = subagentRefs.length
  const agentType = useAgentStore((s) => (ck ? s.agentTypes[ck] : undefined))
  const dynamicTabName = useUISettingsStore((s) => s.dynamicTabName)
  const stripMarker = useUISettingsStore((s) => s.stripAgentTitleMarker)

  const isHostOffline = useHostStore((s) => {
    if (!hostId || isTerminated) return false
    const rt = s.runtime[hostId]
    return rt ? rt.status !== 'connected' : false
  })

  // Worker summary: the live pane state when present, else the host's list row (same order as the projection).
  const execSummary = useExecutionStore((s): ExecutionSummary | null =>
    exec && hostId ? liveWorkerSummary(s.executions, hostId, exec.executionId) : null)
  const execRow = useExecutionListStore((s): ExecutionSummary | null =>
    exec && hostId && !execSummary ? rowWorkerSummary(s.byHost, hostId, exec.executionId) : null)
  const workerSummary = execSummary ?? execRow
  const titleSupported = useNexHostStore(selectSessionTitleSupported(hostId))
  const workerIconStyle = useWorkerSettingsStore((s) => s.iconStyle)
  const workerCustomIcon = useWorkerSettingsStore((s) => s.customIcon)
  const ccIconVariant = useUISettingsStore((s) => s.ccIconVariant)
  const codexIconVariant = useUISettingsStore((s) => s.codexIconVariant)

  const iconName = getPaneIcon(primaryContent)
  const paneIcon = ICON_MAP[iconName]
  const IconComponent = (exec
    ? workerIcon(workerSummary?.provider ?? '', workerIconStyle, { ccVariant: ccIconVariant, codexVariant: codexIconVariant, customIcon: workerCustomIcon })
    : agentIcon ?? paneIcon) as TabIconComponent | undefined

  const sessionLookup = { getByCode: (code: string) => sessions.find((sess) => sess.code === code) }
  const workspaceLookup = { getById: (id: string) => workspaces.find((w) => w.id === id) }
  const baseLabel = getPaneLabel(primaryContent, sessionLookup, workspaceLookup, t)
  const session = sessionCode ? sessionLookup.getByCode(sessionCode) : undefined

  const rawPaneTitle = dynamicTabName && !isTerminated && !!agentType ? session?.pane_title : undefined
  const paneTitle = rawPaneTitle && stripMarker ? stripAgentTitleMarker(rawPaneTitle, agentType) : rawPaneTitle
  // Shared with the notification dispatcher's worker title (worker-summary.ts).
  const workerTitle = exec ? workerTitleOf(exec, workerSummary, titleSupported) ?? baseLabel : ''
  const displayTitle = exec
    // Permission channel PC2 (spec §5.4): 「（等待核准）」 while a request is pending — a suffix, like the closed-terminal
    // one (`page.pane.terminated`); gone once `pending_permission` is null, never there on an old daemon.
    ? isAwaitingApproval(workerSummary) ? t('page.pane.awaiting_approval', { name: workerTitle }) : workerTitle
    : paneTitle ? `${paneTitle} - ${baseLabel}` : baseLabel

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
