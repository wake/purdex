import { X, Lock, WifiSlash } from '@phosphor-icons/react'
import { useSortable } from '@dnd-kit/sortable'
import type { Tab } from '../../../types/tab'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useAgentStore } from '../../../stores/useAgentStore'
import { useHostStore } from '../../../stores/useHostStore'
import { getPrimaryPane } from '../../../lib/pane-tree'
import { getPaneIcon } from '../../../lib/pane-labels'
import { compositeKey } from '../../../lib/composite-key'
import { getAgentIcon } from '../../../lib/agent-icons'
import { ICON_MAP } from '../../../components/tab-icon-map'
import { renderInlineTabIcon } from '../lib/renderInlineTabIcon'

interface Props {
  tab: Tab
  title: string
  isActive: boolean
  sourceWsId?: string | null
  onSelect: (tabId: string) => void
  onClose: (tabId: string) => void
  onMiddleClick: (tabId: string) => void
  onContextMenu: (e: React.MouseEvent, tabId: string) => void
  onRename?: (tabId: string) => void
}

export function InlineTab({
  tab,
  title,
  isActive,
  sourceWsId = null,
  onSelect,
  onClose,
  onMiddleClick,
  onContextMenu,
  onRename,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: tab.id,
    data: { type: 'tab', tabId: tab.id, sourceWsId, isPinned: tab.pinned },
  })

  const primaryContent = getPrimaryPane(tab.layout).content
  const hostId = primaryContent.kind === 'tmux-session' ? primaryContent.hostId : ''
  const sessionCode = primaryContent.kind === 'tmux-session' ? primaryContent.sessionCode : undefined
  const ck = sessionCode && hostId ? compositeKey(hostId, sessionCode) : undefined

  const agentStatus = useAgentStore((s) => (ck ? s.statuses[ck] : undefined))
  const isUnread = useAgentStore((s) => (ck ? !!s.unread[ck] : false))
  const subagentCount = useAgentStore((s) => (ck ? (s.subagents[ck]?.length ?? 0) : 0))
  const agentType = useAgentStore((s) => (ck ? s.agentTypes[ck] : undefined))
  const tabIndicatorStyle = useAgentStore((s) => s.tabIndicatorStyle)
  const ccIconVariant = useAgentStore((s) => s.ccIconVariant)
  const showOscTitle = useAgentStore((s) => s.showOscTitle)
  const oscTitle = useAgentStore((s) => (ck ? s.oscTitles[ck] : undefined))

  const isTerminated = primaryContent.kind === 'tmux-session' && !!primaryContent.terminated

  const useOsc = showOscTitle && !isTerminated && !!agentType && !!oscTitle
  const displayTitle = useOsc && oscTitle ? oscTitle : title
  const tooltip = useOsc && oscTitle ? `${oscTitle} - ${title}` : title
  const isHostOffline = useHostStore((s) => {
    if (!hostId || isTerminated) return false
    const rt = s.runtime[hostId]
    return rt ? rt.status !== 'connected' : false
  })

  const iconName = getPaneIcon(primaryContent)
  const paneIcon = ICON_MAP[iconName]
  const agentIcon = !isTerminated && agentType ? getAgentIcon(agentType, { ccVariant: ccIconVariant }) : undefined
  const IconComponent = (agentIcon ?? paneIcon) as React.ComponentType<{ size: number; className?: string }> | undefined

  // Vertical-only drag — x locked to 0 so the row never slides horizontally
  // across the activity bar border.
  const style: React.CSSProperties = transform
    ? {
        transform: `translate3d(0, ${Math.round(transform.y)}px, 0)`,
        transition,
        opacity: isDragging ? 0.5 : 1,
      }
    : { transition, opacity: isDragging ? 0.5 : 1 }

  // Prevent focus theft when clicking the already-active tab.
  // Must wrap dnd-kit's onPointerDown to avoid overriding it.
  const handlePointerDown = (e: React.PointerEvent) => {
    // Forward to dnd-kit FIRST — dnd-kit checks nativeEvent.defaultPrevented
    // and silently aborts if true, so we must call it before preventDefault.
    const dndHandler = listeners?.onPointerDown as ((e: React.PointerEvent) => void) | undefined
    dndHandler?.(e)
    if (isActive) e.preventDefault()
  }

  // Destructure onPointerDown off listeners so the wrapper wins; spread the rest.
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { onPointerDown: _omit, ...otherListeners } = listeners ?? {}

  const handleCloseClick = (e: React.MouseEvent) => {
    e.stopPropagation()
    onClose(tab.id)
  }
  const handleMouseDown = (e: React.MouseEvent) => {
    if (e.button === 1) {
      e.preventDefault()
      onMiddleClick(tab.id)
    }
  }

  const showClose = !tab.locked

  // Match upper TabBar visual: no left-border accent; active uses subtle
  // accent-muted ring + elevated surface, inactive keeps a transparent border
  // so sibling rows don't shift when toggling active state.
  const activeClasses = isActive
    ? 'bg-surface-active text-white border border-accent-muted'
    : 'text-text-muted hover:bg-surface-hover hover:text-text-primary border border-transparent'

  return (
    <div
      ref={setNodeRef}
      style={style}
      data-testid="inline-tab-row"
      {...attributes}
      {...otherListeners}
      onPointerDown={handlePointerDown}
      role="button"
      tabIndex={0}
      onClick={() => onSelect(tab.id)}
      onDoubleClick={() => onRename?.(tab.id)}
      onMouseDown={handleMouseDown}
      onContextMenu={(e) => onContextMenu(e, tab.id)}
      className={`group relative flex items-center gap-1.5 mx-2 pl-[18px] pr-1.5 py-1 rounded-md text-xs cursor-pointer transition-colors ${activeClasses}`}
    >
      {renderInlineTabIcon({
        IconComponent,
        agentStatus,
        tabIndicatorStyle,
        isActive,
        subagentCount,
      })}
      <span className="flex-1 truncate" title={tooltip}>
        {displayTitle}
      </span>
      {isHostOffline && (
        <WifiSlash
          size={12}
          data-testid="inline-tab-host-offline"
          className="text-red-400 flex-shrink-0"
        />
      )}
      {tab.locked && (
        <Lock size={10} data-testid="inline-tab-lock" className="flex-shrink-0" />
      )}
      {!isActive && isUnread && (
        <span
          data-testid="inline-tab-unread"
          className="absolute -top-[2px] -right-[2px] w-1.5 h-1.5 rounded-full z-20"
          style={{ backgroundColor: '#b91c1c' }}
        />
      )}
      {showClose && (
        <button
          type="button"
          aria-label={`Close ${title}`}
          title={t('common.close')}
          onClick={handleCloseClick}
          onMouseDown={(e) => e.stopPropagation()}
          className={`rounded p-0.5 hover:bg-surface-secondary hover:text-text-primary ${
            isActive ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'
          }`}
        >
          <X size={12} />
        </button>
      )}
    </div>
  )
}
