import { useSortable } from '@dnd-kit/sortable'
import { X, Lock, WifiSlash } from '@phosphor-icons/react'
import type { Tab } from '../types/tab'
import { useI18nStore } from '../stores/useI18nStore'
import { useTabDisplay } from '../hooks/useTabDisplay'
import { TabIcon } from './TabIcon'
import { shouldShowGlobalUnreadPip } from './tab-icon-helpers'

interface Props {
  tab: Tab
  isActive: boolean
  pinned?: boolean
  onSelect: (tabId: string) => void
  onClose: (tabId: string) => void
  onMiddleClick: (tabId: string) => void
  onContextMenu: (e: React.MouseEvent, tabId: string) => void
  onRename?: (tabId: string) => void
  onHover?: (tabId: string | null) => void
}

// Composite bg colors (canvas-verified for opaque X button bg)
// Uses CSS vars so they follow the current theme.
const TAB_BG_INACTIVE = 'var(--surface-secondary)'
const TAB_BG_ACTIVE = 'var(--surface-active)'

export function SortableTab({ tab, isActive, pinned, onSelect, onClose, onMiddleClick, onContextMenu, onRename, onHover }: Props) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: tab.id })

  const style = {
    transform: transform ? `translate3d(${Math.round(transform.x)}px, 0, 0)` : undefined,
    transition,
    zIndex: isDragging ? 10 : undefined,
    opacity: 1,
  }

  const t = useI18nStore((s) => s.t)
  const {
    displayTitle: label,
    tooltip,
    IconComponent,
    agentStatus,
    isUnread,
    subagentCount,
    tabIndicatorStyle,
    isHostOffline,
  } = useTabDisplay(tab)

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
  const handleDoubleClick = () => onRename?.(tab.id)

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
        onDoubleClick={handleDoubleClick}
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
        title={tooltip}
      >
        <TabIcon IconComponent={IconComponent} agentStatus={agentStatus} tabIndicatorStyle={tabIndicatorStyle} isActive={isActive} iconSize={14} subagentCount={subagentCount} isUnread={isUnread} />
        {tab.locked && <Lock size={10} className="absolute bottom-0.5 right-0.5" />}
        {!isActive && isUnread && shouldShowGlobalUnreadPip(tabIndicatorStyle, agentStatus) && (
          <span className="absolute -top-[4px] -right-[4px] w-2 h-2 rounded-full z-20"
            style={{ backgroundColor: '#ef4444' }} />
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
      onDoubleClick={handleDoubleClick}
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
      <TabIcon IconComponent={IconComponent} agentStatus={agentStatus} tabIndicatorStyle={tabIndicatorStyle} isActive={isActive} iconSize={14} subagentCount={subagentCount} isUnread={isUnread} />
      <span className="overflow-hidden flex-1 min-w-0 text-left" title={tooltip}>{label}</span>
      {isHostOffline && <WifiSlash size={12} className="text-red-400 flex-shrink-0" />}
      {tab.locked && <Lock size={10} className="ml-0.5 flex-shrink-0" />}
      {!isActive && isUnread && shouldShowGlobalUnreadPip(tabIndicatorStyle, agentStatus) && (
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
