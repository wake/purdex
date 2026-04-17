import { useCallback, useMemo, useRef, useState } from 'react'
import { Plus, GearSix, HardDrives } from '@phosphor-icons/react'
import {
  DndContext,
  PointerSensor,
  useSensor,
  useSensors,
  closestCenter,
  type DragEndEvent,
} from '@dnd-kit/core'
import {
  SortableContext,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useLayoutStore, MIN_WIDTH, MAX_WIDTH } from '../../../stores/useLayoutStore'
import { RegionResize } from '../../../components/RegionResize'
import { CollapseButton } from './CollapseButton'
import { WorkspaceRow } from './WorkspaceRow'
import { HomeRow } from './HomeRow'
import type { ActivityBarProps } from './activity-bar-props'
import { computeDragEndAction, dispatchDragEndAction } from '../lib/computeDragEndAction'

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
  } = props

  const t = useI18nStore((s) => s.t)
  const wideSize = useLayoutStore((s) => s.activityBarWideSize)
  const setWideSize = useLayoutStore((s) => s.setActivityBarWideSize)

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

  const handleDragEnd = useCallback(
    (e: DragEndEvent) => {
      const action = computeDragEndAction(e, { wsIds, workspaces, standaloneTabIds })
      dispatchDragEndAction(action, {
        onReorderWorkspaces,
        onReorderStandaloneTabs,
        onReorderWorkspaceTabs,
      })
    },
    [
      wsIds,
      workspaces,
      standaloneTabIds,
      onReorderWorkspaces,
      onReorderWorkspaceTabs,
      onReorderStandaloneTabs,
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
          collisionDetection={closestCenter}
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
