import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  DndContext,
  PointerSensor,
  useSensor,
  useSensors,
  pointerWithin,
  rectIntersection,
  closestCenter,
  type CollisionDetection,
  type DragEndEvent,
  type DragOverEvent,
  type Modifier,
} from '@dnd-kit/core'
import {
  SortableContext,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable'
import {
  useLayoutStore,
  MIN_WIDTH,
  MAX_WIDTH,
  WORKER_LIST_MIN,
  WORKER_LIST_MAX,
} from '../../../stores/useLayoutStore'
import { useWorkspaceStore } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { RegionResize } from '../../../components/RegionResize'
import { PaneSplitter } from '../../../components/PaneSplitter'
import { WorkerList } from '../../../components/executions/WorkerList'
import { WorkspaceRow } from './WorkspaceRow'
import { HomeRow } from './HomeRow'
import { BottomNav } from './BottomNav'
import type { ActivityBarProps } from './activity-bar-props'
import { computeDragEndAction, dispatchDragEndAction, type DragData } from '../lib/computeDragEndAction'
import { useSpringLoad } from '../lib/useSpringLoad'
import { useCrossWorkspaceDragOver } from '../lib/useCrossWorkspaceDragOver'

// Each WorkspaceRow registers two overlapping droppables: the useSortable
// wrapper (id = workspace.id) for workspace reordering, and a useDroppable
// header (id = `ws-header-${id}`) for tab cross-ws drops. When dragging a
// workspace, both contain the pointer; pointerWithin returns both and `over`
// flickers between them — verticalListSortingStrategy displaces siblings only
// when over.id is in its items list, so siblings bounce in/out as the over
// alternates. Filter the droppable set by the active drag's type so each
// drag mode only sees its meaningful targets.
const customCollisionDetection: CollisionDetection = (args) => {
  const activeData = args.active.data.current as DragData | undefined
  const containers =
    activeData?.type === 'workspace'
      ? args.droppableContainers.filter((c) => {
          const d = c.data.current as DragData | undefined
          return d?.type === 'workspace'
        })
      : activeData?.type === 'tab'
        ? args.droppableContainers.filter((c) => {
            const d = c.data.current as DragData | undefined
            // Workspace sortables produce no meaningful action for tab drops
            // (computeDragEndAction returns NOOP); excluding them prevents
            // the same over-flicker between the workspace sortable and its
            // own header droppable when a tab hovers over a row.
            return d?.type !== 'workspace'
          })
        : args.droppableContainers
  const filtered = { ...args, droppableContainers: containers }
  const pw = pointerWithin(filtered)
  if (pw.length > 0) return pw
  const ri = rectIntersection(filtered)
  if (ri.length > 0) return ri
  return closestCenter(filtered)
}

const NOOP = () => {}

// The worker list never takes the workspace zone below this (shell cleanup spec §4.2).
const WORKSPACE_ZONE_MIN = 96
// PaneSplitter's 'v' bar is `h-1`.
const WORKER_DIVIDER_HEIGHT = 4

export function ActivityBarWide(props: ActivityBarProps) {
  const {
    workspaces,
    activeWorkspaceId,
    onSelectWorkspace,
    onSelectHome,
    onAddWorkspace,
    onReorderWorkspaces,
    onContextMenuWorkspace,
    onOpenHosts,
    onOpenSettings,
    tabsById = {},
    activeTabId = null,
    onSelectTab,
    onCloseTab,
    onMiddleClickTab,
    onContextMenuTab,
    onRenameTab,
    onReorderWorkspaceTabs,
    onAddTabToWorkspace,
    onMoveTabToWorkspace,
  } = props

  const wideSize = useLayoutStore((s) => s.activityBarWideSize)
  const setWideSize = useLayoutStore((s) => s.setActivityBarWideSize)
  const tabPosition = useLayoutStore((s) => s.tabPosition)
  const workerListOpen = useLayoutStore((s) => s.workerListOpen)
  const workerListHeight = useLayoutStore((s) => s.workerListHeight)
  const toggleWorkerListOpen = useLayoutStore((s) => s.toggleWorkerListOpen)
  const setWorkerListHeight = useLayoutStore((s) => s.setWorkerListHeight)
  const bottomNavCompact = useLayoutStore((s) => s.bottomNavCompact)
  const toggleBottomNavCompact = useLayoutStore((s) => s.toggleBottomNavCompact)

  // Ephemeral drag state for resize handle — avoid persisting + broadcasting on
  // every mousemove. Commit to store only on mouseup (see RegionResize.onResizeEnd).
  const [draftSize, setDraftSize] = useState<number | null>(null)
  const draftSizeRef = useRef<number | null>(null)
  const renderedSize = draftSize ?? wideSize

  // The worker list's height follows the same draft-then-commit pattern as the width (spec §4.2).
  const [draftListHeight, setDraftListHeight] = useState<number | null>(null)
  const draftListHeightRef = useRef<number | null>(null)
  // `available` = the split box's height (workspace zone + divider + list), measured while the list is open. The
  // rendered list height is capped so the zone keeps WORKSPACE_ZONE_MIN; the stored height is never shrunk by a short
  // window. Without a ResizeObserver report (e.g. jsdom) there is no cap.
  const splitBoxRef = useRef<HTMLDivElement>(null)
  const [splitBoxHeight, setSplitBoxHeight] = useState<number | null>(null)
  useEffect(() => {
    const el = splitBoxRef.current
    if (!workerListOpen || !el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(([entry]) => {
      if (entry) setSplitBoxHeight(entry.contentRect.height)
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [workerListOpen])
  const listHeightCap =
    splitBoxHeight === null
      ? null
      : Math.max(0, splitBoxHeight - WORKSPACE_ZONE_MIN - WORKER_DIVIDER_HEIGHT)
  const capListHeight = (h: number) => (listHeightCap === null ? h : Math.min(h, listHeightCap))
  const renderedListHeight = capListHeight(draftListHeight ?? workerListHeight)

  // Dragging up (dy < 0) grows the list. The drag starts from the height on screen and is held inside what can be
  // shown, so the divider tracks the pointer even while the cap applies.
  const handleListResize = (dy: number) => {
    const base = draftListHeightRef.current ?? renderedListHeight
    const upper = listHeightCap === null ? WORKER_LIST_MAX : Math.min(WORKER_LIST_MAX, listHeightCap)
    const next = Math.min(Math.max(base - dy, WORKER_LIST_MIN), upper)
    draftListHeightRef.current = next
    setDraftListHeight(next)
  }
  const handleListResizeEnd = () => {
    if (draftListHeightRef.current === null) return
    setWorkerListHeight(draftListHeightRef.current)
    draftListHeightRef.current = null
    setDraftListHeight(null)
  }

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
  )
  const wsIds = useMemo(() => workspaces.map((ws) => ws.id), [workspaces])
  const isHomeActive = !activeWorkspaceId

  // Lock workspace drag to the Y axis and clamp inside the scroll zone so the
  // dragged row cannot escape the list. Tab drag must remain unrestricted to
  // preserve cross-workspace movement, so the modifier short-circuits unless
  // the active drag is a workspace. Mirrors ActivityBarNarrow's restriction.
  const wsScrollRef = useRef<HTMLDivElement>(null)
  const restrictWorkspaceDrag = useCallback<Modifier>(
    ({ transform, activeNodeRect, active }) => {
      const activeData = active?.data?.current as DragData | undefined
      if (activeData?.type !== 'workspace') return transform
      if (!activeNodeRect || !wsScrollRef.current) {
        return { ...transform, x: 0 }
      }
      const zoneRect = wsScrollRef.current.getBoundingClientRect()
      const minY = zoneRect.top - activeNodeRect.top
      const maxY = zoneRect.bottom - activeNodeRect.bottom
      return {
        ...transform,
        x: 0,
        y: Math.min(Math.max(transform.y, minY), maxY),
      }
    },
    [],
  )

  const selectTab = onSelectTab ?? NOOP
  const closeTab = onCloseTab ?? NOOP
  const middleClickTab = onMiddleClickTab ?? NOOP
  const contextMenuTab = onContextMenuTab ?? NOOP
  const renameTab = onRenameTab
  const addTabToWs = onAddTabToWorkspace ?? NOOP

  const insertTab = useWorkspaceStore((s) => s.insertTab)
  const setActiveWorkspace = useWorkspaceStore((s) => s.setActiveWorkspace)
  const toggleWorkspaceExpanded = useLayoutStore((s) => s.toggleWorkspaceExpanded)
  const springLoad = useSpringLoad(500)
  const handleCrossWsDragOver = useCrossWorkspaceDragOver()

  // When switching to a mode that renders inline tabs (left/both), ensure the
  // active workspace is expanded so the user can see their tabs without
  // manually opening the accordion. Only flips from collapsed → expanded;
  // never collapses what the user opened.
  useEffect(() => {
    if (tabPosition === 'top' || !activeWorkspaceId) return
    const state = useLayoutStore.getState()
    if (!state.workspaceExpanded[activeWorkspaceId]) {
      state.toggleWorkspaceExpanded(activeWorkspaceId)
    }
  }, [tabPosition, activeWorkspaceId])

  // Read activeTabId via getState() at dispatch time rather than via closure,
  // mirroring the stale-closure fix applied to the resize handler in PR #392.
  // Default behavior mutates the store directly; callers that need to
  // intercept (e.g. workspace-locked mode) can pass onMoveTabToWorkspace via props.
  const handleMoveTabToWorkspace = useCallback(
    (tabId: string, targetWsId: string, afterTabId: string | null) => {
      if (onMoveTabToWorkspace) {
        onMoveTabToWorkspace(tabId, targetWsId, afterTabId)
        return
      }
      const wsStore = useWorkspaceStore.getState()
      const sourceWs = wsStore.findWorkspaceByTab(tabId)
      const sourceWsId = sourceWs?.id ?? null
      insertTab(tabId, targetWsId, afterTabId)
      const movedActiveTab = tabId === useTabStore.getState().activeTabId
      const sourceBecameEmpty =
        sourceWsId !== null &&
        sourceWsId !== targetWsId &&
        (useWorkspaceStore.getState().workspaces.find((w) => w.id === sourceWsId)?.tabs.length ?? 0) === 0
      // Follow the moved tab if it was the global active, or if the source
      // workspace we just vacated was the one currently selected — otherwise
      // the user would stay on an empty workspace with no tabs to show.
      if (movedActiveTab) {
        setActiveWorkspace(targetWsId)
      } else if (sourceBecameEmpty && wsStore.activeWorkspaceId === sourceWsId) {
        setActiveWorkspace(targetWsId)
      }
    },
    [insertTab, setActiveWorkspace, onMoveTabToWorkspace],
  )

  const scheduleSpringLoad = useCallback(
    (key: string) => {
      springLoad.schedule(key, () => {
        // Re-check expanded state at fire time so a user who manually
        // expanded the row during the hover doesn't get collapsed back.
        if (!useLayoutStore.getState().workspaceExpanded[key]) {
          toggleWorkspaceExpanded(key)
        }
      })
    },
    [springLoad, toggleWorkspaceExpanded],
  )

  const handleDragStart = useCallback(() => {
    springLoad.cancel()
  }, [springLoad])

  const handleDragOver = useCallback(
    (e: DragOverEvent) => {
      handleCrossWsDragOver(e)
      const { over, active } = e
      if (!over || !active.data.current) {
        springLoad.cancel()
        return
      }
      const activeData = active.data.current as DragData
      if (activeData.type !== 'tab') {
        springLoad.cancel()
        return
      }
      const overData = over.data.current as DragData | undefined
      if (!overData) {
        springLoad.cancel()
        return
      }

      // Pinned tab is locked to its own workspace; any header / cross-ws target
      // is a forbidden drop, so don't auto-expand into one.
      if (activeData.isPinned) {
        springLoad.cancel()
        return
      }

      if (overData.type === 'workspace-header') {
        const key = overData.wsId
        if (!useLayoutStore.getState().workspaceExpanded[key]) {
          scheduleSpringLoad(key)
        } else {
          springLoad.cancel(key)
        }
        return
      }
      springLoad.cancel()
    },
    [handleCrossWsDragOver, springLoad, scheduleSpringLoad],
  )

  const handleDragEnd = useCallback(
    (e: DragEndEvent) => {
      springLoad.cancel()
      const action = computeDragEndAction(e, { wsIds, workspaces })
      dispatchDragEndAction(action, {
        onReorderWorkspaces,
        onReorderWorkspaceTabs,
        onMoveTabToWorkspace: handleMoveTabToWorkspace,
      })
    },
    [
      wsIds,
      workspaces,
      onReorderWorkspaces,
      onReorderWorkspaceTabs,
      handleMoveTabToWorkspace,
      springLoad,
    ],
  )

  return (
    <>
      <div
        data-testid="activity-bar-wide"
        className="hidden min-h-0 flex-col bg-surface-tertiary border-r border-border-subtle py-2 gap-0.5 flex-shrink-0 overflow-hidden lg:flex"
        style={{ width: renderedSize }}
      >
        <DndContext
          sensors={sensors}
          collisionDetection={customCollisionDetection}
          modifiers={[restrictWorkspaceDrag]}
          onDragStart={handleDragStart}
          onDragOver={handleDragOver}
          onDragEnd={handleDragEnd}
        >
          <HomeRow isActive={isHomeActive} onSelectHome={onSelectHome} />

          {workspaces.length > 0 && (
            <div data-testid="activity-bar-workspace-separator" className="mx-3 my-1 h-px shrink-0 bg-border-default" />
          )}

          {/* The split box holds exactly the workspace zone and the worker list, so its height is what they share
              (spec §4.2). The list registers no draggables or droppables, and the workspace drag stays clamped to
              wsScrollRef. */}
          <div data-testid="worker-split-box" ref={splitBoxRef} className="flex min-h-0 flex-1 flex-col">
            <div
              ref={wsScrollRef}
              data-testid="activity-bar-workspace-scroll"
              className="activity-bar-workspace-scroll min-h-0 flex-1 overflow-y-auto overscroll-contain py-0.5"
            >
              <SortableContext items={wsIds} strategy={verticalListSortingStrategy}>
                <div className="flex flex-col gap-0.5">
                  {workspaces.map((ws) => (
                    <WorkspaceRow
                      key={ws.id}
                      workspace={ws}
                      isActive={activeWorkspaceId === ws.id}
                      tabsById={tabsById}
                      activeTabId={activeTabId}
                      onSelectWorkspace={onSelectWorkspace}
                      onContextMenuWorkspace={onContextMenuWorkspace}
                      onSelectTab={selectTab}
                      onCloseTab={closeTab}
                      onMiddleClickTab={middleClickTab}
                      onContextMenuTab={contextMenuTab}
                      onRenameTab={renameTab}
                      onAddTabToWorkspace={addTabToWs}
                    />
                  ))}
                </div>
              </SortableContext>
            </div>

            {/* Nothing of the list mounts while it is closed: each host section holds an SSE stream. */}
            {workerListOpen && (
              <>
                <PaneSplitter
                  direction="v"
                  testId="worker-list-divider"
                  onResize={handleListResize}
                  onResizeEnd={handleListResizeEnd}
                />
                <div
                  data-testid="worker-list-section"
                  className="min-h-0 shrink-0 overflow-y-auto overscroll-contain"
                  style={{ height: renderedListHeight }}
                >
                  <WorkerList />
                </div>
              </>
            )}
          </div>
        </DndContext>

        <BottomNav
          variant="wide"
          compact={bottomNavCompact}
          workersOpen={workerListOpen}
          onAddWorkspace={onAddWorkspace}
          onToggleWorkers={toggleWorkerListOpen}
          onOpenHosts={onOpenHosts}
          onOpenSettings={onOpenSettings}
          onToggleCompact={toggleBottomNavCompact}
        />
      </div>
      <div data-testid="activity-bar-resize" className="hidden lg:flex">
        <RegionResize
          resizeEdge="right"
          onResize={(delta) => {
            // Read the latest committed value rather than a stale closure;
            // accumulate into an ephemeral local value while dragging.
            const base =
              draftSizeRef.current ?? useLayoutStore.getState().activityBarWideSize
            const next = Math.max(
              MIN_WIDTH,
              Math.min(MAX_WIDTH, base + delta),
            )
            draftSizeRef.current = next
            setDraftSize(next)
          }}
          onResizeEnd={() => {
            if (draftSizeRef.current !== null) {
              setWideSize(draftSizeRef.current)
              draftSizeRef.current = null
              setDraftSize(null)
            }
          }}
        />
      </div>
    </>
  )
}
