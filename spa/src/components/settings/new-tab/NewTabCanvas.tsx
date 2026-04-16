import { useMemo } from 'react'
import { useDroppable } from '@dnd-kit/core'
import { SortableContext, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { X } from '@phosphor-icons/react'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import { getNewTabProviders } from '../../../lib/new-tab-registry'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { ProfileKey } from '../../../lib/resolve-profile'

interface Props { profileKey: ProfileKey }

function SortableItem({ profileKey, id, label, onRemove }: {
  profileKey: ProfileKey; id: string; label: string; onRemove: () => void
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: `item:${profileKey}:${id}`,
    data: { type: 'canvas-item', providerId: id, profileKey },
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
      data-testid={`canvas-item-${profileKey}-${id}`}
      className="flex items-center justify-between px-3 py-2 rounded-md bg-surface-elevated border border-border-subtle text-xs"
    >
      <button {...listeners} {...attributes} className="flex-1 text-left cursor-grab select-none" type="button" aria-label={label}>
        {label}
      </button>
      <button
        type="button"
        onClick={onRemove}
        data-testid={`canvas-remove-${profileKey}-${id}`}
        className="text-text-muted hover:text-text-primary cursor-pointer p-1"
        aria-label="remove"
      >
        <X size={12} />
      </button>
    </div>
  )
}

function Column({ profileKey, colIdx, ids }: { profileKey: ProfileKey; colIdx: number; ids: string[] }) {
  const t = useI18nStore((s) => s.t)
  const removeModule = useNewTabLayoutStore((s) => s.removeModule)
  const providers = useMemo(() => getNewTabProviders(), [])
  const byId = useMemo(() => Object.fromEntries(providers.map((p) => [p.id, p])), [providers])
  const { setNodeRef, isOver } = useDroppable({
    id: `col:${profileKey}:${colIdx}`,
    data: { type: 'column', profileKey, colIdx },
  })
  const sortableIds = ids.map((id) => `item:${profileKey}:${id}`)
  return (
    <div
      ref={setNodeRef}
      data-testid={`canvas-column-${profileKey}-${colIdx}`}
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
              profileKey={profileKey}
              id={id}
              label={t(p.label)}
              onRemove={() => removeModule(profileKey, id)}
            />
          )
        })}
      </SortableContext>
      {ids.length === 0 && (
        <div
          data-testid={`canvas-column-empty-${profileKey}-${colIdx}`}
          className="flex-1 flex items-center justify-center text-[11px] text-text-muted"
        >
          {t('settings.interface.canvas_drop_here')}
        </div>
      )}
    </div>
  )
}

export function NewTabCanvas({ profileKey }: Props) {
  const profile = useNewTabLayoutStore((s) => s.profiles[profileKey])
  const gridCols = profile.columns.length === 3 ? 'grid-cols-3'
                 : profile.columns.length === 2 ? 'grid-cols-2'
                 : 'grid-cols-1'
  return (
    <div className={`grid gap-3 ${gridCols}`} data-testid={`canvas-${profileKey}`}>
      {profile.columns.map((ids, i) => (
        <Column key={i} profileKey={profileKey} colIdx={i} ids={ids} />
      ))}
    </div>
  )
}
