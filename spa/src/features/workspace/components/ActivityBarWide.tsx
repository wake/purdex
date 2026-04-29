import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Plus, Sliders, HardDrives } from '@phosphor-icons/react'
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
import { useI18nStore } from '../../../stores/useI18nStore'
import {
  useLayoutStore,
  MIN_WIDTH,
  MAX_WIDTH,
  HOME_WS_KEY,
} from '../../../stores/useLayoutStore'
import { useWorkspaceStore } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { RegionResize } from '../../../components/RegionResize'
import { WorkspaceRow } from './WorkspaceRow'
import { HomeRow } from './HomeRow'
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

export function ActivityBarWide(props: ActivityBarProps) {
  const {
    workspaces,
    activeWorkspaceId,
    activeStandaloneTabId,
    onSelectWorkspace,
    onSelectHome,
    standaloneTabIds,
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
    onReorderStandaloneTabs,
    onAddTabToWorkspace,
    onMoveTabToWorkspace,
    onMoveTabToStandalone,
  } = props

  const t = useI18nStore((s) => s.t)
  const wideSize = useLayoutStore((s) => s.activityBarWideSize)
  const setWideSize = useLayoutStore((s) => s.setActivityBarWideSize)
  const tabPosition = useLayoutStore((s) => s.tabPosition)

  // Ephemeral drag state for resize handle — avoid persisting + broadcasting on
  // every mousemove. Commit to store only on mouseup (see RegionResize.onResizeEnd).
  const [draftSize, setDraftSize] = useState<number | null>(null)
  const draftSizeRef = useRef<number | null>(null)
  const renderedSize = draftSize ?? wideSize

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
  const removeTabFromWorkspace = useWorkspaceStore((s) => s.removeTabFromWorkspace)
  const setActiveWorkspace = useWorkspaceStore((s) => s.setActiveWorkspace)
  const toggleWorkspaceExpanded = useLayoutStore((s) => s.toggleWorkspaceExpanded)
  const springLoad = useSpringLoad(500)
  const handleCrossWsDragOver = useCrossWorkspaceDragOver()

  // When switching to a mode that renders inline tabs (left/both), ensure the
  // active workspace (or Home, when a standalone tab is active) is expanded so
  // the user can see their tabs without manually opening the accordion. Only
  // flips from collapsed → expanded; never collapses what the user opened.
  useEffect(() => {
    if (tabPosition === 'top') return
    const state = useLayoutStore.getState()
    const targetKey = activeStandaloneTabId || !activeWorkspaceId ? HOME_WS_KEY : activeWorkspaceId
    if (!state.workspaceExpanded[targetKey]) {
      state.toggleWorkspaceExpanded(targetKey)
    }
  }, [tabPosition, activeWorkspaceId, activeStandaloneTabId])

  // Read activeTabId via getState() at dispatch time rather than via closure,
  // mirroring the stale-closure fix applied to the resize handler in PR #392.
  // Default behavior mutates the store directly; callers that need to
  // intercept (e.g. workspace-locked mode) can pass onMoveTabToWorkspace /
  // onMoveTabToStandalone via props.
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

  const handleMoveTabToStandalone = useCallback(
    (tabId: string, sourceWsId: string) => {
      if (onMoveTabToStandalone) {
        onMoveTabToStandalone(tabId, sourceWsId)
        return
      }
      const wsStore = useWorkspaceStore.getState()
      const wasSourceActive = wsStore.activeWorkspaceId === sourceWsId
      removeTabFromWorkspace(sourceWsId, tabId)
      const movedActiveTab = tabId === useTabStore.getState().activeTabId
      const sourceBecameEmpty =
        (useWorkspaceStore.getState().workspaces.find((w) => w.id === sourceWsId)?.tabs.length ?? 0) === 0
      if (movedActiveTab || (wasSourceActive && sourceBecameEmpty)) {
        setActiveWorkspace(null)
      }
    },
    [removeTabFromWorkspace, setActiveWorkspace, onMoveTabToStandalone],
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
      if (overData.type === 'home-header') {
        if (!useLayoutStore.getState().workspaceExpanded[HOME_WS_KEY]) {
          scheduleSpringLoad(HOME_WS_KEY)
        } else {
          springLoad.cancel(HOME_WS_KEY)
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
      const action = computeDragEndAction(e, { wsIds, workspaces, standaloneTabIds })
      dispatchDragEndAction(action, {
        onReorderWorkspaces,
        onReorderStandaloneTabs,
        onReorderWorkspaceTabs,
        onMoveTabToWorkspace: handleMoveTabToWorkspace,
        onMoveTabToStandalone: handleMoveTabToStandalone,
      })
    },
    [
      wsIds,
      workspaces,
      standaloneTabIds,
      onReorderWorkspaces,
      onReorderWorkspaceTabs,
      onReorderStandaloneTabs,
      handleMoveTabToWorkspace,
      handleMoveTabToStandalone,
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
          <HomeRow
            isActive={isHomeActive}
            standaloneTabIds={standaloneTabIds}
            tabsById={tabsById}
            activeTabId={activeTabId}
            onSelectHome={onSelectHome}
            onSelectTab={selectTab}
            onCloseTab={closeTab}
            onMiddleClickTab={middleClickTab}
            onContextMenuTab={contextMenuTab}
            onRenameTab={renameTab}
          />

          {workspaces.length > 0 && (
            <div data-testid="activity-bar-workspace-separator" className="mx-3 my-1 h-px shrink-0 bg-border-default" />
          )}

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
                    isActive={
                      activeWorkspaceId === ws.id && !activeStandaloneTabId
                    }
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
        </DndContext>

        <div className="flex shrink-0 flex-col gap-1 px-2 pb-1 pt-2">
          <button
            title={t('nav.new_workspace')}
            onClick={onAddWorkspace}
            className="flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
          >
            <Plus size={16} />
            <span className="truncate">{t('nav.new_workspace')}</span>
          </button>
          <button
            title={t('nav.hosts')}
            onClick={onOpenHosts}
            className="flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
          >
            <HardDrives size={16} />
            <span className="truncate">{t('nav.hosts')}</span>
          </button>
          <button
            title={t('nav.settings')}
            onClick={onOpenSettings}
            className="flex items-center gap-2 px-2 py-1.5 rounded-md text-sm text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
          >
            <Sliders size={16} />
            <span className="truncate">{t('nav.settings')}</span>
          </button>
        </div>
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
