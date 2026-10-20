import { Fragment, useRef, useState, useCallback, useMemo } from 'react'
import { DndContext, closestCenter, PointerSensor, useSensor, useSensors, type CollisionDetection, type DragEndEvent, type Modifier } from '@dnd-kit/core'
import { SortableContext, horizontalListSortingStrategy } from '@dnd-kit/sortable'
import { Plus, CaretLeft, CaretRight, ArrowLeft } from '@phosphor-icons/react'
import { SortableTab } from './SortableTab'
import { useScrollOverflow } from '../hooks/useScrollOverflow'
import type { Tab } from '../types/tab'
import { useI18nStore } from '../stores/useI18nStore'
import { useTeamUiStore } from '../stores/useTeamUiStore'
import { useTeamDisplay, type TeamTabMark } from './team/team-display'
import { groupSegments } from './team/groupSegments'
import { TeamTabGroupFrame } from './team/TeamTabGroup'
import { teamGroupDragId } from './team/group-drag'
import { dragReorder } from '../lib/team/team-group-drag'

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
  /** The watcher area's row: no "+", no lock icon, no menu, no middle click, no close, no team group. Drag-reorder stays. */
  fixed?: boolean
  /** The watcher area's way back (journal v5 §3.7): given, a back button leads the row. */
  onLeave?: () => void
  /**
   * The task-group view's bar (task groups spec §6.2): `tabs` already come in the group's order (lead, seats with all their tabs, plain tabs by rank) and are drawn as they are — no team run (frame, shadow,
   * collapse), nothing hidden. Dragging does not reorder yet (the group's order is not `tabOrder`; TG-4c). An empty group says so.
   */
  flat?: boolean
}

function TabSeparator({ show }: { show: boolean }) {
  return <div data-testid="tab-separator" className={`w-px h-3.5 flex-shrink-0 transition-opacity duration-150 ease-out ${show ? 'bg-border-default' : 'bg-transparent'}`} />
}

export function TabBar({ tabs, activeTabId, onSelectTab, onCloseTab, onAddTab, onReorderTabs, onMiddleClick, onContextMenu, onRenameTab, embedded, fixed, onLeave, flat }: Props) {
  const t = useI18nStore((s) => s.t)
  const pinnedTabs = useMemo(() => tabs.filter((t) => t.pinned), [tabs])
  const normalTabs = useMemo(() => tabs.filter((t) => !t.pinned), [tabs])
  const pinnedIds = useMemo(() => pinnedTabs.map((t) => t.id), [pinnedTabs])
  const normalIds = useMemo(() => normalTabs.map((t) => t.id), [normalTabs])
  // Team runs (spec §4.2): the unpinned tabs split into plain tabs and label + lead + visible members. Only the tabs
  // that are drawn are sortable items; the members a collapse hides are still in `normalIds` for the reorder below.
  // The watcher area's row has no team look (journal v5 §3.7): its guardian tab may show a lead's session, yet every
  // tab there is drawn, and dragged, as a plain tab.
  const team = useTeamDisplay()
  const collapsed = useTeamUiStore((s) => s.collapsed)
  const markOf = useCallback((id: string): TeamTabMark | null => (fixed || flat ? null : team?.tabMark(id) ?? null), [fixed, flat, team])
  const segments = useMemo(() => groupSegments(normalTabs, markOf, collapsed), [normalTabs, markOf, collapsed])
  /** Tab id → the run it is drawn in (grouped tabs only). */
  const runOf = useMemo(() => {
    const m = new Map<string, { teamKey: string; role: 'lead' | 'member'; leadTabId: string }>()
    for (const s of segments) {
      if (s.kind !== 'team') continue
      for (const tab of s.tabs) m.set(tab.id, { teamKey: s.mark.teamKey, role: tab === s.tabs[0] ? 'lead' : 'member', leadTabId: s.tabs[0].id })
    }
    return m
  }, [segments])
  /** What the outer SortableContext sorts (TG-1): a plain tab, or a whole group by its frame; members sort inside the frame. */
  const unitIds = useMemo(() => segments.map((s) => (s.kind === 'tab' ? s.tab.id : teamGroupDragId(s.mark.teamKey))), [segments])
  /** Group frame id → its lead tab (a drag or drop on the frame stands for the lead). */
  const leadOfFrame = useMemo(() => {
    const m = new Map<string, string>()
    for (const s of segments) if (s.kind === 'team') m.set(teamGroupDragId(s.mark.teamKey), s.tabs[0].id)
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

  // Which droppables a drag may land on (TG-1). A group is one unit: the frame stands for it, so while it is dragged its
  // own tabs (which travel with it) are no targets, and a plain tab or a group never targets the inside of another group.
  // A member only ever targets the members of its own group (its inner SortableContext: any other id would stop the
  // sortable transforms mid-drag); with the pointer outside the group's frame the target is itself, so it keeps following
  // the pointer and snaps back on release.
  const collisionDetection: CollisionDetection = useCallback((args) => {
    const activeId = String(args.active.id)
    const from = runOf.get(activeId)
    if (from?.role === 'member') {
      const frame = args.droppableRects.get(teamGroupDragId(from.teamKey))
      const centre = args.collisionRect.left + args.collisionRect.width / 2
      if (frame && (centre < frame.left || centre > frame.right)) return [{ id: args.active.id }]
      const members = args.droppableContainers.filter((c) => {
        const hit = runOf.get(String(c.id))
        return hit?.teamKey === from.teamKey && hit.role === 'member'
      })
      return closestCenter({ ...args, droppableContainers: members })
    }
    const droppableContainers = args.droppableContainers.filter((c) => {
      const id = String(c.id)
      return leadOfFrame.has(id) || !runOf.has(id)
    })
    return closestCenter({ ...args, droppableContainers })
  }, [leadOfFrame, runOf])

  const handleDragEnd = useCallback((event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return
    if (flat) return // the group's order is not `tabOrder`; dragging in a group's bar is TG-4c

    // A group's frame stands for its lead tab.
    const activeId = leadOfFrame.get(String(active.id)) ?? String(active.id)
    const overId = leadOfFrame.get(String(over.id)) ?? String(over.id)

    const inPinned = pinnedIds.includes(activeId)
    const overInPinned = pinnedIds.includes(overId)

    // Only allow same-zone reorder
    if (inPinned !== overInPinned) return

    // A member (spec §4.2) swaps places inside its run only (written to the shared member order; the tab list follows from
    // the lifecycle subscriber). The lead stays first; a drop on the lead or across the run's boundary snaps back.
    const from = runOf.get(activeId)
    if (from?.role === 'member') {
      const to = runOf.get(overId)
      if (!to || from.teamKey !== to.teamKey || to.role !== 'member') return
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

    if (inPinned) {
      const zone = [...pinnedIds]
      const oldIdx = zone.indexOf(activeId)
      const newIdx = zone.indexOf(overId)
      zone.splice(oldIdx, 1)
      zone.splice(newIdx, 0, activeId)
      onReorderTabs([...zone, ...normalIds])
      return
    }

    // The lead (the whole run, hidden members included), or a plain tab: a unit moves to where the target unit is; a drop on
    // any tab of a group lands before or after the whole group.
    const next = dragReorder(normalIds, activeId, overId, (id) => {
      const m = markOf(id)
      return m ? { key: m.teamKey, role: m.role } : null
    })
    if (next) onReorderTabs([...pinnedIds, ...next])
  }, [pinnedIds, normalIds, onReorderTabs, runOf, leadOfFrame, team, markOf, flat])

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
      fixed={fixed}
    />
  )

  return (
    <div className={`flex items-center px-1 ${embedded ? 'h-full flex-1 min-w-0' : 'flex-shrink-0 bg-surface-secondary border-b border-border-subtle'}`} style={embedded ? undefined : { height: 41 }}>
      {/* The way out of the watcher area, ahead of every tab; the box and colours of the "+" button, hover in a token. */}
      {onLeave && (
        <>
          <button
            type="button"
            data-testid="watch-area-back"
            onClick={onLeave}
            className="flex items-center justify-center w-7 h-7 rounded-md text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer flex-shrink-0 focus:outline-none focus-visible:ring-1 focus-visible:ring-border-active"
            title={t('watch.area.leave')}
            aria-label={t('watch.area.leave')}
            style={{ marginTop: 2 }}
          >
            <ArrowLeft size={14} />
          </button>
          <div className="w-px h-4 bg-border-default mx-1 flex-shrink-0" />
        </>
      )}
      <DndContext sensors={sensors} collisionDetection={collisionDetection} modifiers={[restrictToTabZone]} onDragEnd={handleDragEnd}>
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
                      fixed={fixed}
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
              <SortableContext items={unitIds} strategy={horizontalListSortingStrategy}>
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
                        {renderTab(seg.tabs[0], markOf(seg.tabs[0].id) ?? undefined)}
                        <SortableContext items={seg.tabs.slice(1).map((t) => t.id)} strategy={horizontalListSortingStrategy}>
                          {seg.tabs.slice(1).map((tab) => renderTab(tab, markOf(tab.id) ?? undefined))}
                        </SortableContext>
                      </TeamTabGroupFrame>
                    </Fragment>
                  )
                })}
              </SortableContext>
            </div>
            {flat && tabs.length === 0 && (
              <span data-testid="tab-bar-group-empty" className="px-2 text-xs text-text-muted whitespace-nowrap">{t('taskgroups.bar_empty')}</span>
            )}
            {/* Trailing separator + add button (outside SortableContext, inside scroll) */}
            {segments.length > 0 && <TabSeparator show={(() => {
              const last = segments[segments.length - 1]
              if (last.kind === 'team') return false
              return last.tab.id !== activeTabId && last.tab.id !== hoveredTabId
            })()} />}
            {!fixed && <button
              onClick={onAddTab}
              className="flex items-center justify-center w-7 h-7 rounded-md text-text-secondary hover:text-text-primary hover:bg-white/10 cursor-pointer flex-shrink-0"
              title={t('nav.new_tab')}
              style={{ marginTop: 2 }}
            >
              <Plus size={14} />
            </button>}
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
