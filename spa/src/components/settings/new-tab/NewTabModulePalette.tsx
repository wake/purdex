import { useDraggable, useDroppable } from '@dnd-kit/core'
import { useI18nStore } from '../../../stores/useI18nStore'

export interface PaletteItem {
  id: string
  label: string       // i18n key
  inUse: boolean
  unavailable?: boolean
}

interface Props {
  items: PaletteItem[]
  onClickAdd: (id: string) => void
}

function Chip({ item, onClickAdd }: { item: PaletteItem; onClickAdd: (id: string) => void }) {
  const t = useI18nStore((s) => s.t)
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: `palette:${item.id}`,
    disabled: item.inUse,
    data: { type: 'palette', providerId: item.id },
  })

  const disabled = item.inUse
  const className = [
    'inline-flex items-center gap-1 px-2 py-1 rounded-md text-xs border select-none',
    disabled ? 'text-text-muted border-border-subtle bg-transparent cursor-not-allowed'
             : 'text-text-primary border-border-default bg-surface-elevated cursor-grab hover:bg-white/5',
    isDragging ? 'opacity-40' : '',
    item.unavailable ? 'italic' : '',
  ].filter(Boolean).join(' ')

  return (
    <button
      ref={setNodeRef}
      {...listeners}
      {...attributes}
      aria-label={t(item.label)}
      data-testid={`palette-chip-${item.id}`}
      data-unavailable={item.unavailable ? 'true' : undefined}
      data-in-use={item.inUse ? 'true' : undefined}
      disabled={disabled}
      onClick={() => { if (!disabled) onClickAdd(item.id) }}
      className={className}
      type="button"
    >
      <span>{t(item.label)}</span>
      {item.inUse && <span className="ml-1 text-[10px]">{t('settings.interface.palette_in_use')}</span>}
      {item.unavailable && <span className="ml-1 text-[10px]">{t('settings.interface.palette_unavailable')}</span>}
    </button>
  )
}

export function NewTabModulePalette({ items, onClickAdd }: Props) {
  const { setNodeRef, isOver } = useDroppable({ id: 'palette-zone', data: { type: 'palette-zone' } })
  return (
    <div
      ref={setNodeRef}
      data-testid="new-tab-palette"
      data-over={isOver ? 'true' : undefined}
      className={`flex flex-wrap gap-2 p-3 border-b border-border-subtle ${isOver ? 'bg-white/5' : ''}`}
    >
      {items.map((it) => <Chip key={it.id} item={it} onClickAdd={onClickAdd} />)}
    </div>
  )
}
