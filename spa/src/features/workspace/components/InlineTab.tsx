import { X, Lock, WifiSlash } from '@phosphor-icons/react'
import { useSortable } from '@dnd-kit/sortable'
import type { Tab } from '../../../types/tab'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabDisplay } from '../../../hooks/useTabDisplay'
import { shouldShowGlobalUnreadPip } from '../../../components/tab-icon-helpers'
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

  const {
    displayTitle,
    tooltip,
    IconComponent,
    agentStatus,
    isUnread,
    subagentCount,
    tabIndicatorStyle,
    isHostOffline,
  } = useTabDisplay(tab, { titleOverride: title })

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

  // Active surface — no visible border; both states keep a transparent 1px
  // border so sibling rows don't shift when toggling active state.
  const activeClasses = isActive
    ? 'bg-surface-active text-white border border-transparent'
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
        isUnread,
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
      {!isActive && isUnread && shouldShowGlobalUnreadPip(tabIndicatorStyle, agentStatus) && (
        <span
          data-testid="inline-tab-unread"
          className="absolute -top-[4px] -right-[4px] w-2 h-2 rounded-full z-20"
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
