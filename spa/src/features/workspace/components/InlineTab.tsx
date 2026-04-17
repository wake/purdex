import { X, Lock, WifiSlash } from '@phosphor-icons/react'
import { useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import type { Tab } from '../../../types/tab'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useAgentStore } from '../../../stores/useAgentStore'
import { useHostStore } from '../../../stores/useHostStore'
import { getPrimaryPane } from '../../../lib/pane-tree'
import { compositeKey } from '../../../lib/composite-key'
import { TabStatusDot } from '../../../components/TabStatusDot'
import { SubagentDots } from '../../../components/SubagentDots'

interface Props {
  tab: Tab
  title: string
  isActive: boolean
  sourceWsId?: string | null
  onSelect: (tabId: string) => void
  onClose: (tabId: string) => void
  onMiddleClick: (tabId: string) => void
  onContextMenu: (e: React.MouseEvent, tabId: string) => void
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
  const showOscTitle = useAgentStore((s) => s.showOscTitle)
  const oscTitle = useAgentStore((s) => (ck ? s.oscTitles[ck] : undefined))
  const isTerminated =
    primaryContent.kind === 'tmux-session' && !!primaryContent.terminated

  const useOsc = showOscTitle && !isTerminated && !!oscTitle
  const displayTitle = useOsc && oscTitle ? oscTitle : title
  const tooltip = useOsc && oscTitle ? `${oscTitle} - ${title}` : title
  const isHostOffline = useHostStore((s) => {
    if (!hostId || isTerminated) return false
    const rt = s.runtime[hostId]
    return rt ? rt.status !== 'connected' : false
  })

  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

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
      onMouseDown={handleMouseDown}
      onContextMenu={(e) => onContextMenu(e, tab.id)}
      className={`group relative flex items-center gap-1.5 mx-2 pl-5 pr-1.5 py-1 rounded-md text-xs cursor-pointer transition-colors ${
        isActive
          ? 'bg-surface-hover text-text-primary ring-1 ring-purple-400/60'
          : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
      }`}
    >
      {(agentStatus !== undefined || subagentCount > 0) && (
        <span
          data-testid="inline-tab-status-slot"
          className="relative inline-flex items-center justify-center w-3 h-3 flex-shrink-0"
        >
          <TabStatusDot status={agentStatus} style="overlay" isActive={isActive} />
          {subagentCount > 0 && <SubagentDots count={subagentCount} isActive={isActive} />}
        </span>
      )}
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
          className="opacity-0 group-hover:opacity-100 rounded p-0.5 hover:bg-surface-secondary hover:text-text-primary"
        >
          <X size={12} />
        </button>
      )}
    </div>
  )
}
