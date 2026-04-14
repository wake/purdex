import { useCallback, useMemo, useRef } from 'react'
import { DndContext, closestCenter, PointerSensor, useSensor, useSensors, type DragEndEvent, type Modifier } from '@dnd-kit/core'
import { SortableContext, verticalListSortingStrategy, useSortable } from '@dnd-kit/sortable'
import { Plus, GearSix, HardDrives } from '@phosphor-icons/react'
import type { Workspace } from '../../../types/tab'
import { useI18nStore } from '../../../stores/useI18nStore'
import { WorkspaceIcon } from './WorkspaceIcon'
import { useWorkspaceIndicators } from '../useWorkspaceIndicators'
import type { ActiveStatus } from '../workspace-indicators'

const PILL_COLORS: Record<ActiveStatus, string> = {
  running: '#4ade80',
  waiting: '#facc15',
  error: '#ef4444',
}

function SortableWorkspaceButton({ workspace: ws, isActive, onSelect, onContextMenu }: {
  workspace: Workspace
  isActive: boolean
  onSelect: (wsId: string) => void
  onContextMenu?: (e: React.MouseEvent, wsId: string) => void
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: ws.id })
  const { unreadCount, aggregatedStatus } = useWorkspaceIndicators(ws.tabs)
  const showBadge = !isActive && unreadCount > 0
  const tooltipExtras = [
    showBadge && `${unreadCount} unread`,
    aggregatedStatus && !isActive && aggregatedStatus,
  ].filter(Boolean)
  const tooltipText = tooltipExtras.length > 0 ? `${ws.name} (${tooltipExtras.join(', ')})` : ws.name

  const style = {
    transform: transform ? `translate3d(0, ${Math.round(transform.y)}px, 0)` : undefined,
    transition,
    zIndex: isDragging ? 10 : undefined,
    opacity: isDragging ? 0.8 : 1,
  }

  return (
    <div ref={setNodeRef} style={style} className="relative group" {...attributes} {...listeners}>
      {aggregatedStatus && !isActive && (
        <span
          className={`absolute rounded-full ${aggregatedStatus === 'running' ? 'animate-breathe' : ''}`}
          style={{
            width: '5px',
            height: '5px',
            left: '-1px',
            top: '50%',
            transform: 'translateY(calc(-50% - 1px))',
            backgroundColor: PILL_COLORS[aggregatedStatus],
            boxShadow: '0 0 0 1.5px var(--surface-tertiary)',
            '--breathe-color': PILL_COLORS[aggregatedStatus],
            '--breathe-bg': 'var(--surface-tertiary)',
          } as React.CSSProperties}
        />
      )}
      <button
        aria-label={[
          ws.name,
          showBadge && `${unreadCount} unread`,
          aggregatedStatus && !isActive && aggregatedStatus,
        ].filter(Boolean).join(', ')}
        onClick={() => onSelect(ws.id)}
        onContextMenu={(e) => {
          e.preventDefault()
          onContextMenu?.(e, ws.id)
        }}
        className={`w-[30px] h-[30px] rounded-md flex items-center justify-center text-sm cursor-pointer transition-all ${
          isActive
            ? 'bg-[#8b5cf6]/35 text-text-primary ring-2 ring-purple-400'
            : 'bg-surface-secondary text-text-secondary hover:bg-surface-hover hover:text-text-primary'
        }`}
      >
        <WorkspaceIcon icon={ws.icon} name={ws.name} size={16} weight={ws.iconWeight} />
      </button>
      {showBadge && (
        <span
          data-testid="ws-unread-badge"
          className="absolute -top-[5px] -right-[6px] min-w-[15px] h-[15px] rounded-full flex items-center justify-center text-white text-[9px] font-bold px-[3px] leading-none z-10"
          style={{ backgroundColor: '#dc2626', boxShadow: '0 0 0 2px var(--surface-tertiary)' }}
        >
          {unreadCount > 99 ? '99+' : unreadCount}
        </span>
      )}
      <span data-testid="ws-tooltip" className="pointer-events-none absolute left-full ml-2 top-1/2 -translate-y-1/2 whitespace-nowrap rounded bg-surface-secondary border border-border-default px-2 py-1 text-xs text-text-primary shadow-lg opacity-0 group-hover:opacity-100 transition-opacity z-50">
        {tooltipText}
      </span>
    </div>
  )
}

interface Props {
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectHome: () => void
  standaloneTabIds: string[]
  onAddWorkspace: () => void
  onReorderWorkspaces?: (orderedIds: string[]) => void
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
  onOpenHosts: () => void
  onOpenSettings: () => void
}

export function ActivityBar({
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
}: Props) {
  const t = useI18nStore((s) => s.t)
  const wsIds = useMemo(() => workspaces.map((ws) => ws.id), [workspaces])
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))
  const wsZoneRef = useRef<HTMLDivElement>(null)

  const restrictToVertical: Modifier = useCallback(({ transform, activeNodeRect }) => {
    if (!activeNodeRect || !wsZoneRef.current) return { ...transform, x: 0 }
    const zoneRect = wsZoneRef.current.getBoundingClientRect()
    const minY = zoneRect.top - activeNodeRect.top
    const maxY = zoneRect.bottom - activeNodeRect.bottom
    return { ...transform, x: 0, y: Math.min(Math.max(transform.y, minY), maxY) }
  }, [])

  const handleDragEnd = useCallback((event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const oldIdx = wsIds.indexOf(String(active.id))
    const newIdx = wsIds.indexOf(String(over.id))
    if (oldIdx === -1 || newIdx === -1) return
    const newOrder = [...wsIds]
    newOrder.splice(oldIdx, 1)
    newOrder.splice(newIdx, 0, String(active.id))
    onReorderWorkspaces?.(newOrder)
  }, [wsIds, onReorderWorkspaces])

  const { unreadCount: homeUnreadCount, aggregatedStatus: homeStatus } = useWorkspaceIndicators(standaloneTabIds)
  const isHomeActive = !activeWorkspaceId
  const showHomeBadge = (!isHomeActive || !!activeStandaloneTabId) && homeUnreadCount > 0
  return (
    <div className="hidden lg:flex w-11 flex-col items-center bg-surface-tertiary border-r border-border-subtle py-2 px-px gap-2.5 flex-shrink-0">
      {/* Home — standalone tabs */}
      <div className="relative group">
        {homeStatus && (!isHomeActive || !!activeStandaloneTabId) && (
          <span
            className={`absolute rounded-full ${homeStatus === 'running' ? 'animate-breathe' : ''}`}
            style={{
              width: '5px',
              height: '5px',
              left: '-1px',
              top: '50%',
              transform: 'translateY(calc(-50% - 1px))',
              backgroundColor: PILL_COLORS[homeStatus],
              boxShadow: '0 0 0 1.5px var(--surface-tertiary)',
              '--breathe-color': PILL_COLORS[homeStatus],
              '--breathe-bg': 'var(--surface-tertiary)',
            } as React.CSSProperties}
          />
        )}
        <button
          title={t('nav.home')}
          onClick={onSelectHome}
          className={`w-[30px] h-[30px] rounded-lg flex items-center justify-center cursor-pointer transition-all ${
            isHomeActive
              ? 'ring-2 ring-purple-400'
              : 'hover:bg-surface-tertiary opacity-70 hover:opacity-100'
          }`}
        >
          <img src="/icons/logo-transparent.png" alt="Purdex" width={20} height={20} className="rounded-sm" />
        </button>
        {showHomeBadge && (
          <span
            data-testid="home-unread-badge"
            className="absolute -top-[5px] -right-[6px] min-w-[15px] h-[15px] rounded-full flex items-center justify-center text-white text-[9px] font-bold px-[3px] leading-none z-10"
            style={{ backgroundColor: '#dc2626', boxShadow: '0 0 0 2px var(--surface-tertiary)' }}
          >
            {homeUnreadCount > 99 ? '99+' : homeUnreadCount}
          </span>
        )}
      </div>

      {workspaces.length > 0 && <div className="w-5 h-px bg-border-default my-0.5" />}

      {/* Workspaces — sortable */}
      <DndContext sensors={sensors} collisionDetection={closestCenter} modifiers={[restrictToVertical]} onDragEnd={handleDragEnd}>
        <SortableContext items={wsIds} strategy={verticalListSortingStrategy}>
          <div ref={wsZoneRef} className="flex flex-col items-center gap-2.5">
            {workspaces.map((ws) => (
              <SortableWorkspaceButton
                key={ws.id}
                workspace={ws}
                isActive={activeWorkspaceId === ws.id && !activeStandaloneTabId}
                onSelect={onSelectWorkspace}
                onContextMenu={onContextMenuWorkspace}
              />
            ))}
          </div>
        </SortableContext>
      </DndContext>

      {/* Add + Settings */}
      <div className="mt-auto flex flex-col items-center gap-2 pb-1">
        <button
          title={t('nav.new_workspace')}
          onClick={onAddWorkspace}
          className="w-[30px] h-[30px] rounded-md flex items-center justify-center text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer"
        >
          <Plus size={16} />
        </button>
        <button
          title={t('nav.hosts')}
          onClick={onOpenHosts}
          className="w-[30px] h-[30px] rounded-md flex items-center justify-center text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer"
        >
          <HardDrives size={16} />
        </button>
        <button
          title={t('nav.settings')}
          onClick={onOpenSettings}
          className="w-[30px] h-[30px] rounded-md flex items-center justify-center text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer"
        >
          <GearSix size={16} />
        </button>
      </div>
    </div>
  )
}
