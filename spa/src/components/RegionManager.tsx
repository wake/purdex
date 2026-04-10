import { DndContext, closestCenter, PointerSensor, useSensor, useSensors, type DragEndEvent } from '@dnd-kit/core'
import { SortableContext, verticalListSortingStrategy, useSortable, arrayMove } from '@dnd-kit/sortable'
import { DotsSixVertical, X, Plus } from '@phosphor-icons/react'
import { useLayoutStore } from '../stores/useLayoutStore'
import { getAllViews } from '../lib/module-registry'
import type { SidebarRegion } from '../types/tab'

interface Props {
  region: SidebarRegion
}

interface SortableViewRowProps {
  viewId: string
  label: string
  icon: React.ComponentType<{ size?: number; className?: string }>
  onRemove: () => void
}

function SortableViewRow({ viewId, label, icon: Icon, onRemove }: SortableViewRowProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: viewId })
  const style = {
    transform: transform
      ? `translate3d(${transform.x}px, ${transform.y}px, 0) scaleX(${transform.scaleX}) scaleY(${transform.scaleY})`
      : undefined,
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

  return (
    <div
      ref={setNodeRef}
      style={style}
      className="flex items-center gap-2 px-2 py-1.5 rounded hover:bg-surface-hover group"
    >
      <button
        {...attributes}
        {...listeners}
        className="text-text-muted hover:text-text-primary cursor-grab active:cursor-grabbing shrink-0"
        aria-label="Drag to reorder"
      >
        <DotsSixVertical size={14} />
      </button>
      <Icon size={14} className="text-text-muted shrink-0" />
      <span className="flex-1 text-xs text-text-primary truncate">{label}</span>
      <button
        data-testid="remove-view-btn"
        className="text-text-muted hover:text-text-primary opacity-0 group-hover:opacity-100 transition-opacity shrink-0"
        onClick={onRemove}
        aria-label={`Remove ${label}`}
      >
        <X size={12} />
      </button>
    </div>
  )
}

export function RegionManager({ region }: Props) {
  const views = useLayoutStore((s) => s.regions[region].views)
  const addView = useLayoutStore((s) => s.addView)
  const removeView = useLayoutStore((s) => s.removeView)
  const reorderViews = useLayoutStore((s) => s.reorderViews)

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  const allViews = getAllViews()
  const enabledSet = new Set(views)
  const availableViews = allViews.filter((v) => !enabledSet.has(v.id))

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const oldIndex = views.indexOf(String(active.id))
    const newIndex = views.indexOf(String(over.id))
    if (oldIndex === -1 || newIndex === -1) return
    reorderViews(region, arrayMove([...views], oldIndex, newIndex))
  }

  return (
    <div data-testid="region-manager" className="flex flex-col gap-3 p-2 text-xs">
      {views.length > 0 && (
        <div>
          <div className="text-text-muted px-2 pb-1 font-medium">已啟用</div>
          <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
            <SortableContext items={views} strategy={verticalListSortingStrategy}>
              {views.map((viewId) => {
                const viewDef = allViews.find((v) => v.id === viewId)
                if (!viewDef) return null
                return (
                  <SortableViewRow
                    key={viewId}
                    viewId={viewId}
                    label={viewDef.label}
                    icon={viewDef.icon}
                    onRemove={() => removeView(region, viewId)}
                  />
                )
              })}
            </SortableContext>
          </DndContext>
        </div>
      )}

      {availableViews.length > 0 && (
        <div>
          <div className="text-text-muted px-2 pb-1 font-medium">可加入</div>
          {availableViews.map((viewDef) => (
            <div
              key={viewDef.id}
              className="flex items-center gap-2 px-2 py-1.5 rounded hover:bg-surface-hover"
            >
              <viewDef.icon size={14} className="text-text-muted shrink-0" />
              <span className="flex-1 text-xs text-text-muted truncate">{viewDef.label}</span>
              <button
                data-testid="add-view-btn"
                className="text-text-muted hover:text-text-primary shrink-0"
                onClick={() => addView(region, viewDef.id)}
                aria-label={`Add ${viewDef.label}`}
              >
                <Plus size={12} />
              </button>
            </div>
          ))}
        </div>
      )}

      {views.length === 0 && availableViews.length === 0 && (
        <div className="text-text-muted px-2 py-2">沒有可用的 views</div>
      )}
    </div>
  )
}
