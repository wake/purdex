import { X, Lock, WifiSlash } from '@phosphor-icons/react'
import { useSortable } from '@dnd-kit/sortable'
import type { Tab } from '../../../types/tab'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabDisplay } from '../../../hooks/useTabDisplay'
import { shouldShowGlobalUnreadPip } from '../../../components/tab-icon-helpers'
import { HoverTooltip } from '../../../components/HoverTooltip'
import { renderInlineTabIcon } from '../lib/renderInlineTabIcon'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { HostBadge } from '../../../components/HostBadge'
import { useTabHostBadge } from '../../../hooks/useTabHostBadge'
import { hasHostBadge } from '../../../lib/host-color'
import { INLINE_TAB_ROW_CLASSES } from '../lib/inline-tab-row-classes'

interface Props {
  tab: Tab
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
  isActive,
  sourceWsId = null,
  onSelect,
  onClose,
  onMiddleClick,
  onContextMenu,
  onRename,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const tabNameTooltipMode = useUISettingsStore((s) => s.tabNameTooltipMode)
  const badgeEnabled = useUISettingsStore((s) => s.hostBadgeSidebarEnabled)
  const badgeLineColor = useUISettingsStore((s) => s.hostBadgeSidebarLineColor)
  const badgeBox = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  const badgeInset = useUISettingsStore((s) => s.hostBadgeSidebarInset)
  const badgeRadius = useUISettingsStore((s) => s.hostBadgeSidebarRadius)
  const hostBadge = useTabHostBadge(tab)
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: tab.id,
    data: { type: 'tab', tabId: tab.id, sourceWsId, isPinned: tab.pinned },
  })

  const {
    displayTitle,
    IconComponent,
    agentStatus,
    isUnread,
    subagentRefs,
    tabIndicatorStyle,
    isHostOffline,
  } = useTabDisplay(tab)

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
  const showTooltip = tabNameTooltipMode === 'left' || tabNameTooltipMode === 'both'

  // Active surface — no visible border; both states keep a transparent 1px
  // border so sibling rows don't shift when toggling active state.
  const activeClasses = isActive ? INLINE_TAB_ROW_CLASSES.active : INLINE_TAB_ROW_CLASSES.inactive

  return (
    <div
      ref={setNodeRef}
      style={style}
      data-testid="inline-tab-row"
      data-active={String(isActive)}
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
        subagentRefs,
        isUnread,
      })}
      {badgeEnabled && hasHostBadge(hostBadge) && (
        <HostBadge
          colors={hostBadge.colors}
          icon={hostBadge.icon}
          iconWeight={hostBadge.iconWeight}
          box={badgeBox}
          inset={badgeInset}
          radius={badgeRadius}
          lineColor={badgeLineColor}
        />
      )}
      <span data-testid="inline-tab-title" className="flex-1 truncate">
        {displayTitle}
      </span>
      {/*
        placement=right mirrors ActivityBarNarrow's pattern — the sidebar is an
        overflow-y-auto scroll container, so a top-anchored tooltip can be
        clipped near the edge. Right escapes into the main content area.
      */}
      {showTooltip && (
        <HoverTooltip placement="right" data-testid="inline-tab-tooltip">
          {displayTitle}
        </HoverTooltip>
      )}
      {isHostOffline && (
        <WifiSlash
          size={12}
          data-testid="inline-tab-host-offline"
          className="text-red-400 flex-shrink-0"
        />
      )}
      {tab.locked && (
        // Same 16x16 box as the close button so the lock lands on the exact
        // same center — the row's right edge is shared by both icons.
        <span
          data-testid="inline-tab-lock"
          className="flex h-4 w-4 flex-shrink-0 items-center justify-center"
        >
          <Lock size={10} />
        </span>
      )}
      {!isActive && isUnread && shouldShowGlobalUnreadPip(tabIndicatorStyle, agentStatus) && (
        <span
          data-testid="inline-tab-unread"
          className="absolute -top-[4px] -right-[4px] w-2 h-2 rounded-full z-20"
          style={{ backgroundColor: '#ef4444' }}
        />
      )}
      {showClose && (
        <button
          type="button"
          aria-label={`Close ${displayTitle}`}
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
