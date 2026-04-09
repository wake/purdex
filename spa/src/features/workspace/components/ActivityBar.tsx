import { useCallback, useMemo } from 'react'
import { DndContext, closestCenter, PointerSensor, useSensor, useSensors, type DragEndEvent, type Modifier } from '@dnd-kit/core'
import { SortableContext, verticalListSortingStrategy, useSortable } from '@dnd-kit/sortable'
import { Plus, GearSix, HardDrives, SquaresFour } from '@phosphor-icons/react'
import type { Workspace } from '../../../types/tab'
import { useI18nStore } from '../../../stores/useI18nStore'
import { WorkspaceIcon } from './WorkspaceIcon'

interface Props {
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectHome: () => void
  standaloneTabCount: number
  onAddWorkspace: () => void
  onReorderWorkspaces?: (orderedIds: string[]) => void
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
  onOpenHosts: () => void
  onOpenSettings: () => void
}

function SortableWorkspaceButton({ ws, isActive, onSelect, onContextMenu }: {
  ws: Workspace
  isActive: boolean
  onSelect: (wsId: string) => void
  onContextMenu?: (e: React.MouseEvent, wsId: string) => void
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: ws.id })

  const style = {
    transform: transform ? `translate3d(0, ${Math.round(transform.y)}px, 0)` : undefined,
    transition,
    zIndex: isDragging ? 10 : undefined,
    opacity: isDragging ? 0.8 : 1,
  }

  return (
    <div ref={setNodeRef} style={style} className="relative group" {...attributes} {...listeners}>
      <button
        aria-label={ws.name}
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
      <span className="pointer-events-none absolute left-full ml-2 top-1/2 -translate-y-1/2 whitespace-nowrap rounded bg-surface-secondary border border-border-default px-2 py-1 text-xs text-text-primary shadow-lg opacity-0 group-hover:opacity-100 transition-opacity z-50">
        {ws.name}
      </span>
    </div>
  )
}

export function ActivityBar({
  workspaces,
  activeWorkspaceId,
  activeStandaloneTabId,
  onSelectWorkspace,
  onSelectHome,
  standaloneTabCount,
  onAddWorkspace,
  onReorderWorkspaces,
  onContextMenuWorkspace,
  onOpenHosts,
  onOpenSettings,
}: Props) {
  const t = useI18nStore((s) => s.t)
  const wsIds = useMemo(() => workspaces.map((ws) => ws.id), [workspaces])
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  const restrictToVertical: Modifier = useCallback(({ transform }) => {
    return { ...transform, x: 0 }
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

  return (
    <div className="hidden lg:flex w-11 flex-col items-center bg-surface-tertiary border-r border-border-subtle py-2 px-px gap-2.5 flex-shrink-0">
      {/* Home — standalone tabs */}
      <button
        title={t('nav.home')}
        onClick={onSelectHome}
        className={`relative w-[30px] h-[30px] rounded-lg flex items-center justify-center cursor-pointer transition-all ${
          !activeWorkspaceId
            ? 'bg-accent text-white'
            : 'bg-surface-secondary text-text-secondary hover:text-text-primary hover:bg-surface-tertiary'
        }`}
      >
        <SquaresFour size={18} weight={!activeWorkspaceId ? 'fill' : 'regular'} />
        {activeWorkspaceId && standaloneTabCount > 0 && (
          <span className="absolute -top-1 -right-1 min-w-[16px] h-4 rounded-full bg-red-500 text-white text-[10px] font-bold flex items-center justify-center px-0.5">
            {standaloneTabCount}
          </span>
        )}
      </button>

      {workspaces.length > 0 && <div className="w-5 h-px bg-border-default my-0.5" />}

      {/* Workspaces — sortable */}
      <DndContext sensors={sensors} collisionDetection={closestCenter} modifiers={[restrictToVertical]} onDragEnd={handleDragEnd}>
        <SortableContext items={wsIds} strategy={verticalListSortingStrategy}>
          {workspaces.map((ws) => (
            <SortableWorkspaceButton
              key={ws.id}
              ws={ws}
              isActive={activeWorkspaceId === ws.id && !activeStandaloneTabId}
              onSelect={onSelectWorkspace}
              onContextMenu={onContextMenuWorkspace}
            />
          ))}
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
