import { X, Lock, WifiSlash } from '@phosphor-icons/react'
import { useSortable } from '@dnd-kit/sortable'
import type { Tab } from '../../../types/tab'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useTabDisplay } from '../../../hooks/useTabDisplay'
import { shouldShowGlobalUnreadPip } from '../../../components/tab-icon-helpers'
import { HoverTooltip } from '../../../components/HoverTooltip'
import { renderInlineTabIcon } from '../lib/renderInlineTabIcon'
import { Avatar } from '../../../components/Avatar'
import { useSessionSeat } from '../../../hooks/useSessionSeat'
import { useSeat } from '../../../lib/names/seat'
import { useTeamUiStore } from '../../../stores/useTeamUiStore'
import { getPrimaryPane } from '../../../lib/pane-tree'
import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { HostBadge } from '../../../components/HostBadge'
import { useTabHostBadge } from '../../../hooks/useTabHostBadge'
import { hasHostBadge } from '../../../lib/host-color'
import { INLINE_TAB_ROW_CLASSES } from '../lib/inline-tab-row-classes'
import { HalfHandoffMark } from '../../../components/relay/HalfHandoffMark'
import { useFreshHandoffOfTab } from '../../../hooks/useFreshHandoff'

interface Props {
  tab: Tab
  isActive: boolean
  sourceWsId?: string | null
  onSelect: (tabId: string) => void
  onClose: (tabId: string) => void
  onMiddleClick: (tabId: string) => void
  onContextMenu: (e: React.MouseEvent, tabId: string) => void
  onRename?: (tabId: string) => void
  /** The team key when this row is a team's lead row (the handoff mark then reads the roster). */
  leadOfTeam?: string
  /** Not a drag source: the task-group view lists the sessions by group, and a drop there means nothing. */
  noDrag?: boolean
  /** The row's drag data when it is not a workspace tab: the task-group view tags its rows (lib/group-drag.ts). */
  dragData?: object
  /** The row's sortable id when it is not the tab's id: a split tab carrying several team seats has one row per seat. */
  dragId?: string
  /** The row stands for a team seat (the task-group view): its persona comes from the seat's address and its title is the seat's label — a split tab can carry several seats, each with its own row. */
  seatOverride?: { address: string; title: string }
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
  leadOfTeam,
  noDrag = false,
  dragData,
  dragId,
  seatOverride,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const tabNameTooltipMode = useUISettingsStore((s) => s.tabNameTooltipMode)
  const badgeEnabled = useUISettingsStore((s) => s.hostBadgeSidebarEnabled)
  const badgeLineColor = useUISettingsStore((s) => s.hostBadgeSidebarLineColor)
  const badgeBox = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  const badgeInset = useUISettingsStore((s) => s.hostBadgeSidebarInset)
  const badgeRadius = useUISettingsStore((s) => s.hostBadgeSidebarRadius)
  const hostBadge = useTabHostBadge(tab)
  const freshHandoff = useFreshHandoffOfTab(tab, leadOfTeam)
  // `attributes` is deliberately not spread: sensors are pointer-only, so its aria-roledescription / aria-describedby
  // ("press space to pick up") would be false. The row sets its own role=button + tabIndex=0 below (#2531).
  const { listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: dragId ?? tab.id,
    data: (dragData as Record<string, unknown> | undefined) ?? { type: 'tab', tabId: tab.id, sourceWsId, isPinned: tab.pinned },
    disabled: noDrag,
  })

  const {
    displayTitle,
    IconComponent,
    agentStatus,
    isUnread,
    subagentRefs,
    tabIndicatorStyle,
    isHostOffline,
    isAwaitingApproval,
    background,
    backgroundWaiters,
  } = useTabDisplay(tab)

  // Who the row is (name pool v2 spec §9.6): the persona of the session the tab shows, when the App already knows it (useSessionSeat reads, never asks). With none the row is as it was.
  const primary = getPrimaryPane(tab.layout).content
  const tmux = primary.kind === 'tmux-session' && !primary.terminated ? primary : null
  // A seat's row names the session by its tab's title (the one the tab bar shows); the seat's label stands in when the tab has none.
  const shownTitle = displayTitle || seatOverride?.title || ''
  const bySession = useSessionSeat(tmux?.hostId ?? '', tmux?.sessionCode, tmux?.tmuxInstance ?? '')
  const bySeat = useSeat(seatOverride?.address ?? '')
  // Only the task-group view dresses a row as its persona; the workspace view keeps the plain agent-icon row (owner call 10-20, overturning spec §9.6 U10).
  const groupsView = useTeamUiStore((s) => s.sidebarView === 'groups')
  const seat = groupsView ? (seatOverride ? bySeat : bySession) : null

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

  // Same action as a click. Keyboard parity for the role=button row (#2538): Enter / Space select it.
  const select = () => onSelect(tab.id)
  const handleKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    // Only the row itself: keys pressed in an inner control (close button, rename input) bubble up here.
    if (e.target !== e.currentTarget) return
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault() // Space would otherwise scroll the sidebar
      select()
    }
  }

  const handleCloseClick = (e: React.MouseEvent) => {
    e.stopPropagation()
    onClose(tab.id)
  }
  const handleMouseDown = (e: React.MouseEvent) => {
    if (e.button === 1) {
      e.preventDefault()
      // a seat's row never closes (see showClose)
      if (!seatOverride) onMiddleClick(tab.id)
    }
  }

  // A seat's row has no close: its tab may carry other seats too (a split tab), and closing it would close them all.
  const showClose = !tab.locked && !seatOverride
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
      {...otherListeners}
      onPointerDown={handlePointerDown}
      role="button"
      tabIndex={0}
      onClick={select}
      onKeyDown={handleKeyDown}
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
        awaitingApproval: isAwaitingApproval,
        background,
        backgroundWaiters,
        avatar: seat !== null ? <Avatar size={18} code={seat.code} name={seat.name} src={seat.avatar} /> : undefined,
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
        {seat !== null ? (
          <>
            <span data-testid="inline-tab-name" className={`font-semibold${isActive ? '' : ' text-text-secondary group-hover:text-text-primary'}`}>{seat.name}</span>
            {shownTitle !== '' && shownTitle !== seat.name && <span data-testid="inline-tab-subtitle" className="ml-1.5 text-text-muted">{shownTitle}</span>}
          </>
        ) : shownTitle}
      </span>
      {/*
        placement=right mirrors ActivityBarNarrow's pattern — the sidebar is an
        overflow-y-auto scroll container, so a top-anchored tooltip can be
        clipped near the edge. Right escapes into the main content area.
      */}
      {showTooltip && (
        <HoverTooltip placement="right" data-testid="inline-tab-tooltip">
          {shownTitle}
        </HoverTooltip>
      )}
      <HalfHandoffMark handoff={freshHandoff} variant="icon" />
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
          aria-label={`Close ${shownTitle}`}
          title={t('common.close')}
          onClick={handleCloseClick}
          onMouseDown={(e) => e.stopPropagation()}
          onPointerDown={(e) => e.stopPropagation()} // a press on the button is the button's, never the start of a row drag
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
