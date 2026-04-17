import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Plus, GearSix, HardDrives } from '@phosphor-icons/react'
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
import { CollapseButton } from './CollapseButton'
import { WorkspaceRow } from './WorkspaceRow'
import { HomeRow } from './HomeRow'
import type { ActivityBarProps } from './activity-bar-props'
import { computeDragEndAction, dispatchDragEndAction, type DragData } from '../lib/computeDragEndAction'
import { useSpringLoad } from '../lib/useSpringLoad'

const customCollisionDetection: CollisionDetection = (args) => {
  const pw = pointerWithin(args)
  if (pw.length > 0) return pw
  const ri = rectIntersection(args)
  if (ri.length > 0) return ri
  return closestCenter(args)
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

  const selectTab = onSelectTab ?? NOOP
  const closeTab = onCloseTab ?? NOOP
  const middleClickTab = onMiddleClickTab ?? NOOP
  const contextMenuTab = onContextMenuTab ?? NOOP
  const addTabToWs = onAddTabToWorkspace ?? NOOP

  const insertTab = useWorkspaceStore((s) => s.insertTab)
  const removeTabFromWorkspace = useWorkspaceStore((s) => s.removeTabFromWorkspace)
  const setActiveWorkspace = useWorkspaceStore((s) => s.setActiveWorkspace)
  const toggleWorkspaceExpanded = useLayoutStore((s) => s.toggleWorkspaceExpanded)
  const springLoad = useSpringLoad(500)

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
    [springLoad, scheduleSpringLoad],
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
        className="hidden lg:flex flex-col bg-surface-tertiary border-r border-border-subtle py-2 gap-0.5 flex-shrink-0 overflow-y-auto"
        style={{ width: renderedSize }}
      >
        <DndContext
          sensors={sensors}
          collisionDetection={customCollisionDetection}
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
          />

          {workspaces.length > 0 && (
            <div className="mx-3 my-1 h-px bg-border-default" />
          )}

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
                  onAddTabToWorkspace={addTabToWs}
                />
              ))}
            </div>
          </SortableContext>
        </DndContext>

        <div className="mt-auto flex flex-col gap-1 px-2 pb-1 pt-2">
          <div className="flex items-center justify-end">
            <CollapseButton />
          </div>
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
            <GearSix size={16} />
            <span className="truncate">{t('nav.settings')}</span>
          </button>
        </div>
      </div>
      <div data-testid="activity-bar-resize" className="hidden lg:block">
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
