import { Fragment, useRef, useState, useCallback, useMemo } from 'react'
import { DndContext, closestCenter, PointerSensor, useSensor, useSensors, type DragEndEvent, type Modifier } from '@dnd-kit/core'
import { SortableContext, horizontalListSortingStrategy } from '@dnd-kit/sortable'
import { Plus, CaretLeft, CaretRight } from '@phosphor-icons/react'
import { SortableTab } from './SortableTab'
import { useScrollOverflow } from '../hooks/useScrollOverflow'
import type { Tab } from '../types/tab'
import { useI18nStore } from '../stores/useI18nStore'
import { useTeamUiStore } from '../stores/useTeamUiStore'
import { useTeamDisplay, type TeamTabMark } from './team/team-display'
import { groupSegments } from './team/groupSegments'
import { TeamTabGroupFrame } from './team/TeamTabGroup'

interface Props {
  tabs: Tab[]
  activeTabId: string | null
  onSelectTab: (tabId: string) => void
  onCloseTab: (tabId: string) => void
  onAddTab: () => void
  onReorderTabs: (newOrder: string[]) => void
  onMiddleClick: (tabId: string) => void
  onContextMenu: (e: React.MouseEvent, tabId: string) => void
  onRenameTab?: (tabId: string) => void
  /** When embedded in Electron title bar — no border, no fixed height */
  embedded?: boolean
}

function TabSeparator({ show }: { show: boolean }) {
  return <div data-testid="tab-separator" className={`w-px h-3.5 flex-shrink-0 transition-opacity duration-150 ease-out ${show ? 'bg-border-default' : 'bg-transparent'}`} />
}

export function TabBar({ tabs, activeTabId, onSelectTab, onCloseTab, onAddTab, onReorderTabs, onMiddleClick, onContextMenu, onRenameTab, embedded }: Props) {
  const t = useI18nStore((s) => s.t)
  const pinnedTabs = useMemo(() => tabs.filter((t) => t.pinned), [tabs])
  const normalTabs = useMemo(() => tabs.filter((t) => !t.pinned), [tabs])
  const pinnedIds = useMemo(() => pinnedTabs.map((t) => t.id), [pinnedTabs])
  const normalIds = useMemo(() => normalTabs.map((t) => t.id), [normalTabs])
  // Team runs (spec §4.2): the unpinned tabs split into plain tabs and label + lead + visible members. Only the tabs
  // that are drawn are sortable items; the members a collapse hides are still in `normalIds` for the reorder below.
  const team = useTeamDisplay()
  const collapsed = useTeamUiStore((s) => s.collapsed)
  const segments = useMemo(() => groupSegments(normalTabs, (id) => team?.tabMark(id) ?? null, collapsed), [normalTabs, team, collapsed])
  const shownIds = useMemo(() => segments.flatMap((s) => (s.kind === 'tab' ? [s.tab.id] : s.tabs.map((t) => t.id))), [segments])
  /** Tab id → the run it is drawn in (grouped tabs only). */
  const runOf = useMemo(() => {
    const m = new Map<string, { teamKey: string; role: 'lead' | 'member'; leadTabId: string }>()
    for (const s of segments) {
      if (s.kind !== 'team') continue
      for (const tab of s.tabs) m.set(tab.id, { teamKey: s.mark.teamKey, role: tab === s.tabs[0] ? 'lead' : 'member', leadTabId: s.tabs[0].id })
    }
    return m
  }, [segments])
  const [hoveredTabId, setHoveredTabId] = useState<string | null>(null)
  const pinnedZoneRef = useRef<HTMLDivElement>(null)
  const normalTabsRef = useRef<HTMLDivElement>(null)
  const { containerRef: normalZoneRef, canScrollLeft, canScrollRight, scrollLeft, scrollRight } = useScrollOverflow()
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  // Custom modifier: restrict drag to the zone containing the active item
  const restrictToTabZone: Modifier = useCallback(({ transform, activeNodeRect, active }) => {
    if (!activeNodeRect || !active) return { ...transform, y: 0 }
    const activeId = String(active.id)
    const isPinned = pinnedIds.includes(activeId)
    const zone = isPinned ? pinnedZoneRef.current : normalZoneRef.current
    if (!zone) return { ...transform, y: 0 }
    const zoneRect = zone.getBoundingClientRect()
    const minX = zoneRect.left - activeNodeRect.left
    let maxX = zoneRect.right - activeNodeRect.right
    // Normal zone: clamp right to tabs area (exclude trailing separator + add button)
    if (!isPinned && normalTabsRef.current) {
      const tabsRight = normalTabsRef.current.getBoundingClientRect().right
      maxX = Math.min(maxX, tabsRight - activeNodeRect.right)
    }
    return { ...transform, x: Math.min(Math.max(transform.x, minX), maxX), y: 0 }
  }, [pinnedIds, normalZoneRef])

  const handleDragEnd = useCallback((event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return

    const activeId = String(active.id)
    const overId = String(over.id)

    const inPinned = pinnedIds.includes(activeId)
    const overInPinned = pinnedIds.includes(overId)

    // Only allow same-zone reorder
    if (inPinned !== overInPinned) return

    // A team run (spec §4.2): members swap places inside it (written to the shared member order; the tab list follows from
    // the lifecycle subscriber). The lead stays first and the run is not draggable as a whole; a drop across its boundary,
    // or a plain tab dropped into it, snaps back.
    const from = runOf.get(activeId)
    const to = runOf.get(overId)
    if (from || to) {
      if (!from || !to || from.teamKey !== to.teamKey || from.role !== 'member' || to.role !== 'member') return
      const order = team?.panelTeam(from.leadTabId)?.members ?? []
      // The whole member order (unopened members included): the dragged seat takes the target's index, as dnd-kit's arrayMove.
      const ids = order.map((m) => m.sessionId)
      const fromIdx = order.findIndex((m) => m.tabId === activeId)
      const toIdx = order.findIndex((m) => m.tabId === overId)
      if (fromIdx < 0 || toIdx < 0) return
      const [moved] = ids.splice(fromIdx, 1)
      ids.splice(toIdx, 0, moved)
      team?.onReorderMembers(from.teamKey, ids)
      return
    }

    const zone = inPinned ? [...pinnedIds] : [...normalIds]
    const oldIdx = zone.indexOf(activeId)
    const newIdx = zone.indexOf(overId)
    zone.splice(oldIdx, 1)
    zone.splice(newIdx, 0, activeId)

    const newOrder = inPinned ? [...zone, ...normalIds] : [...pinnedIds, ...zone]
    onReorderTabs(newOrder)
  }, [pinnedIds, normalIds, onReorderTabs, runOf, team])

  // Separator visibility: hide near active or hovered tab
  const shouldShowSeparator = (leftTab: Tab | undefined, rightTab: Tab | undefined) => {
    if (!leftTab || !rightTab) return false
    const hide = [activeTabId, hoveredTabId]
    if (hide.includes(leftTab.id) || hide.includes(rightTab.id)) return false
    return true
  }

  const renderTab = (tab: Tab, group?: TeamTabMark) => (
    <SortableTab
      key={tab.id}
      tab={tab}
      isActive={tab.id === activeTabId}
      group={group}
      onSelect={onSelectTab}
      onClose={onCloseTab}
      onMiddleClick={onMiddleClick}
      onContextMenu={onContextMenu}
      onRename={onRenameTab}
      onHover={setHoveredTabId}
    />
  )

  return (
    <div className={`flex items-center px-1 ${embedded ? 'h-full flex-1 min-w-0' : 'flex-shrink-0 bg-surface-secondary border-b border-border-subtle'}`} style={embedded ? undefined : { height: 41 }}>
      <DndContext sensors={sensors} collisionDetection={closestCenter} modifiers={[restrictToTabZone]} onDragEnd={handleDragEnd}>
        {/* Pinned zone */}
        {pinnedTabs.length > 0 && (
          <>
            <SortableContext items={pinnedIds} strategy={horizontalListSortingStrategy}>
              <div ref={pinnedZoneRef} className="flex items-center h-full">
                {pinnedTabs.map((tab, i) => (
                  <Fragment key={tab.id}>
                    {i > 0 && <TabSeparator show={shouldShowSeparator(pinnedTabs[i - 1], tab)} />}
                    <SortableTab
                      tab={tab}
                      isActive={tab.id === activeTabId}
                      pinned
                      onSelect={onSelectTab}
                      onClose={onCloseTab}
                      onMiddleClick={onMiddleClick}
                      onContextMenu={onContextMenu}
                      onRename={onRenameTab}
                      onHover={setHoveredTabId}
                    />
                  </Fragment>
                ))}
              </div>
            </SortableContext>
            <div className="w-px h-4 bg-border-default mx-1 flex-shrink-0" />
          </>
        )}

        {/* Normal zone with overflow arrows */}
        <div className="relative flex-1 min-w-0 h-full">
          {canScrollLeft && (
            <div data-testid="tab-scroll-left-fade" className="absolute left-0 top-0 bottom-0 z-10 w-16 flex justify-start pointer-events-none">
              <button
                onClick={scrollLeft}
                className="w-8 h-full flex items-center justify-center bg-surface-secondary cursor-pointer pointer-events-auto"
                aria-label={t('nav.scroll_left')}
              >
                <CaretLeft size={14} className="text-text-secondary" />
              </button>
              <div data-testid="tab-scroll-left-gradient" className="w-8 h-full bg-gradient-to-r from-surface-secondary to-transparent" />
            </div>
          )}
          <div ref={normalZoneRef} className="flex items-center h-full overflow-x-auto scrollbar-hide">
            <div ref={normalTabsRef} className="flex items-center h-full flex-1 min-w-0" style={{ maxWidth: 'max-content' }}>
              <SortableContext items={shownIds} strategy={horizontalListSortingStrategy}>
                {segments.map((seg, si) => {
                  const prev = segments[si - 1]
                  // Separators before the group / after the group stay in the row but are never drawn next to a group.
                  const sep = si > 0 && <TabSeparator show={seg.kind === 'tab' && prev.kind === 'tab' && shouldShowSeparator(prev.tab, seg.tab)} />
                  if (seg.kind === 'tab') {
                    return <Fragment key={seg.tab.id}>{sep}{renderTab(seg.tab)}</Fragment>
                  }
                  return (
                    <Fragment key={`g-${seg.mark.teamKey}`}>
                      {sep}
                      <TeamTabGroupFrame mark={seg.mark}>
                        {seg.tabs.map((tab) => renderTab(tab, team?.tabMark(tab.id) ?? undefined))}
                      </TeamTabGroupFrame>
                    </Fragment>
                  )
                })}
              </SortableContext>
            </div>
            {/* Trailing separator + add button (outside SortableContext, inside scroll) */}
            {segments.length > 0 && <TabSeparator show={(() => {
              const last = segments[segments.length - 1]
              if (last.kind === 'team') return false
              return last.tab.id !== activeTabId && last.tab.id !== hoveredTabId
            })()} />}
            <button
              onClick={onAddTab}
              className="flex items-center justify-center w-7 h-7 rounded-md text-text-secondary hover:text-text-primary hover:bg-white/10 cursor-pointer flex-shrink-0"
              title={t('nav.new_tab')}
              style={{ marginTop: 2 }}
            >
              <Plus size={14} />
            </button>
          </div>
          {canScrollRight && (
            <div data-testid="tab-scroll-right-fade" className="absolute right-0 top-0 bottom-0 z-10 w-16 flex justify-end pointer-events-none">
              <div data-testid="tab-scroll-right-gradient" className="w-8 h-full bg-gradient-to-r from-transparent to-surface-secondary" />
              <button
                onClick={scrollRight}
                className="w-8 h-full flex items-center justify-center bg-surface-secondary cursor-pointer pointer-events-auto"
                aria-label={t('nav.scroll_right')}
              >
                <CaretRight size={14} className="text-text-secondary" />
              </button>
            </div>
          )}
        </div>
      </DndContext>
    </div>
  )
}
