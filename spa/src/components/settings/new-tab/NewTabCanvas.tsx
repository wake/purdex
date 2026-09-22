import { useMemo } from 'react'
import { useDroppable } from '@dnd-kit/core'
import { SortableContext, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { X } from '@phosphor-icons/react'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import { useNewTabProviders } from '../../../hooks/useNewTabProviders'
import { useI18nStore } from '../../../stores/useI18nStore'
import { colsClass } from '../../../lib/cols-class'
import type { PresetKey } from '../../../lib/resolve-preset'

interface Props { presetKey: PresetKey }

function SortableItem({ presetKey, id, label, onRemove }: {
  presetKey: PresetKey; id: string; label: string; onRemove: () => void
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: `item:${presetKey}:${id}`,
    data: { type: 'canvas-item', providerId: id, presetKey },
  })
  const style = {
    transform: CSS.Translate.toString(transform),
    transition,
    opacity: isDragging ? 0.4 : 1,
  }
  return (
    <div
      ref={setNodeRef}
      style={style}
      data-testid={`canvas-item-${presetKey}-${id}`}
      className="flex items-center justify-between px-3 py-2 rounded-md bg-surface-elevated border border-border-subtle text-xs"
    >
      <button {...listeners} {...attributes} className="flex-1 text-left cursor-grab select-none" type="button" aria-label={label}>
        {label}
      </button>
      <button
        type="button"
        onClick={onRemove}
        data-testid={`canvas-remove-${presetKey}-${id}`}
        className="text-text-muted hover:text-text-primary cursor-pointer p-1"
        aria-label="remove"
      >
        <X size={12} />
      </button>
    </div>
  )
}

function Column({ presetKey, colIdx, ids }: { presetKey: PresetKey; colIdx: number; ids: string[] }) {
  const t = useI18nStore((s) => s.t)
  const removeModule = useNewTabLayoutStore((s) => s.removeModule)
  const providers = useNewTabProviders()
  const byId = useMemo(() => Object.fromEntries(providers.map((p) => [p.id, p])), [providers])
  const { setNodeRef, isOver } = useDroppable({
    id: `col:${presetKey}:${colIdx}`,
    data: { type: 'column', presetKey, colIdx },
  })
  const sortableIds = ids.map((id) => `item:${presetKey}:${id}`)
  return (
    <div
      ref={setNodeRef}
      data-testid={`canvas-column-${presetKey}-${colIdx}`}
      data-over={isOver ? 'true' : undefined}
      className={[
        'flex flex-col gap-2 p-2 rounded-md min-h-32 border',
        isOver ? 'border-border-active bg-white/5' : 'border-border-subtle',
      ].join(' ')}
    >
      <SortableContext items={sortableIds} strategy={verticalListSortingStrategy}>
        {ids.map((id) => {
          const p = byId[id]
          if (!p) return null
          return (
            <SortableItem
              key={id}
              presetKey={presetKey}
              id={id}
              label={t(p.label, p.labelParams)}
              onRemove={() => removeModule(presetKey, id)}
            />
          )
        })}
      </SortableContext>
      {ids.length === 0 && (
        <div
          data-testid={`canvas-column-empty-${presetKey}-${colIdx}`}
          className="flex-1 flex items-center justify-center text-[11px] text-text-muted"
        >
          {t('settings.interface.canvas_drop_here')}
        </div>
      )}
    </div>
  )
}

export function NewTabCanvas({ presetKey }: Props) {
  const preset = useNewTabLayoutStore((s) => s.profiles[presetKey])
  const gridCols = colsClass(preset.columns.length)
  return (
    <div className={`grid gap-3 ${gridCols}`} data-testid={`canvas-${presetKey}`}>
      {preset.columns.map((ids, i) => (
        <Column key={i} presetKey={presetKey} colIdx={i} ids={ids} />
      ))}
    </div>
  )
}
