import { useSortable } from '@dnd-kit/sortable'
import { X, Lock, WifiSlash } from '@phosphor-icons/react'
import type { Tab } from '../types/tab'
import { getPaneIcon, getPaneLabel } from '../lib/pane-labels'
import { getPrimaryPane } from '../lib/pane-tree'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useAgentStore } from '../stores/useAgentStore'
import type { AgentStatus, TabIndicatorStyle } from '../stores/useAgentStore'
import { compositeKey } from '../lib/composite-key'
import { AGENT_ICONS } from '../lib/agent-icons'
import type { Session } from '../lib/host-api'

const EMPTY_SESSIONS: Session[] = []
import { TabStatusDot } from './TabStatusDot'
import { SubagentDots } from './SubagentDots'

function renderTabIcon(
  IconComponent: React.ComponentType<{ size: number; className?: string }> | undefined,
  agentStatus: AgentStatus | undefined,
  tabIndicatorStyle: TabIndicatorStyle,
  isActive: boolean,
  iconSize: number,
  subagentCount: number,
) {
  if (tabIndicatorStyle === 'replace' && agentStatus) {
    return (
      <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
        <TabStatusDot status={agentStatus} style="replace" isActive={isActive} />
        {subagentCount > 0 && <SubagentDots count={subagentCount} isActive={isActive} />}
      </span>
    )
  }
  if (tabIndicatorStyle === 'inline') {
    return (
      <span className="relative inline-flex items-center justify-center flex-shrink-0" style={{ minWidth: 16 }}>
        {IconComponent && <IconComponent size={iconSize} className="flex-shrink-0" />}
        <TabStatusDot status={agentStatus} style="inline" isActive={isActive} />
        {subagentCount > 0 && <SubagentDots count={subagentCount} isActive={isActive} />}
      </span>
    )
  }
  return (
    <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
      {IconComponent && <IconComponent size={iconSize} className="flex-shrink-0" />}
      <TabStatusDot status={agentStatus} style="overlay" isActive={isActive} />
      {subagentCount > 0 && <SubagentDots count={subagentCount} isActive={isActive} />}
    </span>
  )
}

interface Props {
  tab: Tab
  isActive: boolean
  pinned?: boolean
  onSelect: (tabId: string) => void
  onClose: (tabId: string) => void
  onMiddleClick: (tabId: string) => void
  onContextMenu: (e: React.MouseEvent, tabId: string) => void
  onHover?: (tabId: string | null) => void
  iconMap: Record<string, React.ComponentType<{ size: number; className?: string }>>
}

// Composite bg colors (canvas-verified for opaque X button bg)
// Uses CSS vars so they follow the current theme.
const TAB_BG_INACTIVE = 'var(--surface-secondary)'
const TAB_BG_ACTIVE = 'var(--surface-active)'

export function SortableTab({ tab, isActive, pinned, onSelect, onClose, onMiddleClick, onContextMenu, onHover, iconMap }: Props) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: tab.id })

  const style = {
    transform: transform ? `translate3d(${Math.round(transform.x)}px, 0, 0)` : undefined,
    transition,
    zIndex: isDragging ? 10 : undefined,
    opacity: 1,
  }

  const primaryContent = getPrimaryPane(tab.layout).content
  const iconName = getPaneIcon(primaryContent)
  const paneIcon = iconMap[iconName]

  const t = useI18nStore((s) => s.t)
  const hostId = primaryContent.kind === 'tmux-session' ? primaryContent.hostId : ''
  const sessions = useSessionStore((s) => (hostId ? s.sessions[hostId] : undefined) ?? EMPTY_SESSIONS)
  const workspaces = useWorkspaceStore((s) => s.workspaces)

  const sessionCode = primaryContent.kind === 'tmux-session' ? primaryContent.sessionCode : undefined
  const ck = sessionCode && hostId ? compositeKey(hostId, sessionCode) : undefined
  const agentStatus = useAgentStore((s) => {
    if (!ck) return undefined
    // No fallback — only show indicator when we have an actual hook event.
    // Previously fell back to 'idle' when hooksInstalled was true, but that
    // made it impossible to distinguish "agent running, first event pending"
    // from "no agent running at all".
    return s.statuses[ck]
  })
  const isUnread = useAgentStore((s) => ck ? !!s.unread[ck] : false)
  const subagentCount = useAgentStore((s) => ck ? (s.subagents[ck]?.length ?? 0) : 0)
  const agentType = useAgentStore((s) => ck ? s.agentTypes[ck] : undefined)
  const tabIndicatorStyle = useAgentStore((s) => s.tabIndicatorStyle)
  const isTerminated = primaryContent.kind === 'tmux-session' && !!primaryContent.terminated
  // Keep the terminated pane's SmileySad tombstone instead of the agent icon.
  const agentIcon = !isTerminated && agentType ? AGENT_ICONS[agentType] : undefined
  const IconComponent = (agentIcon ?? paneIcon) as React.ComponentType<{ size: number; className?: string }> | undefined
  const isHostOffline = useHostStore((s) => {
    if (!hostId || isTerminated) return false
    const rt = s.runtime[hostId]
    return rt ? rt.status !== 'connected' : false
  })
  const sessionLookup = { getByCode: (code: string) => sessions.find((s) => s.code === code) }
  const workspaceLookup = { getById: (id: string) => workspaces.find((w) => w.id === id) }
  const label = getPaneLabel(primaryContent, sessionLookup, workspaceLookup, t)

  // Prevent focus theft when clicking the already-active tab.
  // Must wrap dnd-kit's onPointerDown to avoid overriding it.
  const handlePointerDown = (e: React.PointerEvent) => {
    // Forward to dnd-kit FIRST — dnd-kit checks nativeEvent.defaultPrevented
    // and silently aborts if true, so we must call it before preventDefault.
    const dndHandler = listeners?.onPointerDown as ((e: React.PointerEvent) => void) | undefined
    dndHandler?.(e)
    if (isActive) e.preventDefault()
  }

  const handleMouseEnter = () => onHover?.(tab.id)
  const handleMouseLeave = () => onHover?.(null)
  const handleMouseUp = (e: React.MouseEvent) => {
    if (e.button === 1) { e.preventDefault(); onMiddleClick(tab.id) }
  }
  const handleContextMenu = (e: React.MouseEvent) => {
    e.preventDefault()
    onContextMenu(e, tab.id)
  }

  const tabBg = isActive ? TAB_BG_ACTIVE : TAB_BG_INACTIVE

  if (pinned) {
    return (
      <button
        ref={setNodeRef}
        data-tab-id={tab.id}
        style={{ ...style, height: 26, margin: '0 1px', marginTop: 2 }}
        {...attributes}
        {...listeners}
        onClick={() => onSelect(tab.id)}
        onPointerDown={handlePointerDown}
        onMouseUp={handleMouseUp}
        onMouseEnter={handleMouseEnter}
        onMouseLeave={handleMouseLeave}
        onContextMenu={handleContextMenu}
        className={`relative flex items-center justify-center w-9 rounded-[6px] cursor-pointer transition-colors duration-150 ease-out ${
          isActive
            ? 'text-white bg-surface-active border border-accent-muted'
            : 'text-text-muted hover:text-text-primary bg-surface-secondary hover:bg-surface-hover border border-transparent'
        }`}
        title={label}
      >
        {renderTabIcon(IconComponent, agentStatus, tabIndicatorStyle, isActive, 14, subagentCount)}
        {tab.locked && <Lock size={10} className="absolute bottom-0.5 right-0.5" />}
        {!isActive && isUnread && (
          <span className="absolute -top-[4px] -right-[4px] w-2 h-2 rounded-full z-20"
            style={{ backgroundColor: '#b91c1c' }} />
        )}
      </button>
    )
  }

  const showClose = !tab.locked

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); onSelect(tab.id) }
  }

  return (
    <div
      ref={setNodeRef}
      data-tab-id={tab.id}
      style={{ ...style, height: 26, margin: '0 1px', marginTop: 2, flex: '0 1 140px', width: 140, minWidth: 80 }}
      {...attributes}
      {...listeners}
      role="tab"
      aria-selected={isActive}
      onClick={() => onSelect(tab.id)}
      onPointerDown={handlePointerDown}
      onKeyDown={handleKeyDown}
      onMouseUp={handleMouseUp}
      onMouseEnter={handleMouseEnter}
      onMouseLeave={handleMouseLeave}
      onContextMenu={handleContextMenu}
      className={`group relative flex items-center gap-1.5 pl-2 pr-1 text-xs whitespace-nowrap cursor-pointer transition-colors duration-150 ease-out rounded-[6px] ${
        isActive
          ? 'text-white bg-surface-active border border-accent-muted'
          : 'text-text-muted hover:text-text-primary bg-surface-secondary hover:bg-surface-hover border border-transparent'
      }`}
    >
      {renderTabIcon(IconComponent, agentStatus, tabIndicatorStyle, isActive, 14, subagentCount)}
      <span className="overflow-hidden flex-1 min-w-0 text-left">{label}</span>
      {isHostOffline && <WifiSlash size={12} className="text-red-400 flex-shrink-0" />}
      {tab.locked && <Lock size={10} className="ml-0.5 flex-shrink-0" />}
      {!isActive && isUnread && (
        <span className="absolute -top-[4px] -right-[4px] w-2 h-2 rounded-full z-20"
          style={{ backgroundColor: '#b91c1c' }} />
      )}
      {showClose && (
        <span className="absolute right-0 top-0 bottom-0 flex items-center">
          {/* Gradient fade -- always visible */}
          <span className="w-3 self-stretch" style={{ background: `linear-gradient(to right, transparent, ${tabBg})` }} />
          {/* Solid padding after fade (visible when X hidden) */}
          <span className={`self-stretch ${isActive ? 'w-0' : 'w-1.5 group-hover:w-0'}`} style={{ backgroundColor: tabBg }} />
          {/* X button -- real <button> for a11y (no nested interactive elements) */}
          <button
            type="button"
            tabIndex={-1}
            title={t('tab.close')}
            onClick={(e) => { e.stopPropagation(); onClose(tab.id) }}
            className={`self-stretch flex items-center cursor-pointer rounded-r-[6px] border-none p-0 ${
              isActive
                ? 'w-6 opacity-100'
                : 'w-0 overflow-hidden opacity-0 group-hover:w-6 group-hover:overflow-visible group-hover:opacity-100'
            }`}
            style={{ backgroundColor: tabBg }}
          >
            <X size={12} className="mx-auto flex-shrink-0" />
          </button>
        </span>
      )}
    </div>
  )
}
