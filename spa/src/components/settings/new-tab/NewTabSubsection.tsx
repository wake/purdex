import { useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import {
  DndContext, DragOverlay, PointerSensor, useSensor, useSensors,
  type DragEndEvent, type DragStartEvent, pointerWithin,
} from '@dnd-kit/core'
import { getNewTabProviders } from '../../../lib/new-tab-registry'
import { useNewTabLayoutStore, shortestColIdx } from '../../../stores/useNewTabLayoutStore'
import type { ProfileKey } from '../../../lib/resolve-profile'
import { useI18nStore } from '../../../stores/useI18nStore'
import { NewTabModulePalette, type PaletteItem } from './NewTabModulePalette'
import { NewTabProfileSwitcher } from './NewTabProfileSwitcher'
import { NewTabCanvas } from './NewTabCanvas'
import { NewTabThumbnail } from './NewTabThumbnail'

export function NewTabSubsection() {
  const t = useI18nStore((s) => s.t)
  const providers = useMemo(() => getNewTabProviders(), [])
  const profiles = useNewTabLayoutStore((s) => s.profiles)
  const active = useNewTabLayoutStore((s) => s.activeEditingProfile)
  const setEditing = useNewTabLayoutStore((s) => s.setEditing)
  const setEnabled = useNewTabLayoutStore((s) => s.setEnabled)
  const placeModule = useNewTabLayoutStore((s) => s.placeModule)
  const removeModule = useNewTabLayoutStore((s) => s.removeModule)

  const [dragging, setDragging] = useState<null | { providerId: string; source: 'palette' | 'canvas' }>(null)

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  const paletteItems: PaletteItem[] = useMemo(() => {
    const activeIds = new Set(profiles[active].columns.flat())
    return providers.map((p) => ({
      id: p.id,
      label: p.label,
      inUse: activeIds.has(p.id),
      unavailable: p.disabled,
    }))
  }, [providers, profiles, active])

  const handleClickAdd = (id: string) => {
    const cols = useNewTabLayoutStore.getState().profiles[active].columns
    const col = shortestColIdx(cols)
    placeModule(active, id, col, cols[col].length)
  }

  const handleDragStart = (e: DragStartEvent) => {
    const id = String(e.active.id)
    if (id.startsWith('palette:')) {
      setDragging({ providerId: id.slice('palette:'.length), source: 'palette' })
    } else if (id.startsWith('item:')) {
      const parts = id.split(':')
      setDragging({ providerId: parts[2], source: 'canvas' })
    }
  }

  const handleDragEnd = (e: DragEndEvent) => {
    const activeEvt = e.active
    const over = e.over
    setDragging(null)
    if (!over) return

    const src = activeEvt.data.current as { type?: string; providerId?: string; profileKey?: ProfileKey } | undefined
    const dst = over.data.current as { type?: string; profileKey?: ProfileKey; colIdx?: number } | undefined
    if (!src?.providerId) return

    // Drop into palette zone = remove from canvas
    if (dst?.type === 'palette-zone') {
      if (src.type === 'canvas-item' && src.profileKey) {
        removeModule(src.profileKey, src.providerId)
      }
      return
    }

    // Drop onto another canvas sortable item = insert at its position
    const overIdStr = String(over.id)
    if (overIdStr.startsWith('item:')) {
      const [, overProfile, overId] = overIdStr.split(':')
      const profileKey = overProfile as ProfileKey
      const cols = useNewTabLayoutStore.getState().profiles[profileKey].columns
      const colIdx = cols.findIndex((c) => c.includes(overId))
      if (colIdx < 0) return
      const rowIdx = cols[colIdx].indexOf(overId)
      placeModule(profileKey, src.providerId, colIdx, rowIdx)
      return
    }

    // Drop onto column empty area = append to that column
    if (dst?.type === 'column' && dst.profileKey && typeof dst.colIdx === 'number') {
      const cols = useNewTabLayoutStore.getState().profiles[dst.profileKey].columns
      placeModule(dst.profileKey, src.providerId, dst.colIdx, cols[dst.colIdx].length)
    }
  }

  const overlayLabel = dragging
    ? (providers.find((p) => p.id === dragging.providerId)?.label ?? dragging.providerId)
    : null

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={pointerWithin}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
      onDragCancel={() => setDragging(null)}
    >
      <div className="flex flex-col h-full">
        <NewTabModulePalette items={paletteItems} onClickAdd={handleClickAdd} />
        <NewTabProfileSwitcher
          active={active}
          onSelect={setEditing}
          onToggleEnabled={setEnabled}
          renderMain={(k) => <NewTabCanvas profileKey={k} />}
          renderThumb={(k) => <NewTabThumbnail profileKey={k} />}
        />
      </div>
      {typeof document !== 'undefined' && createPortal(
        <DragOverlay>
          {overlayLabel && (
            <div className="px-2 py-1 rounded-md bg-surface-elevated border border-border-active text-xs shadow-lg">
              {t(overlayLabel)}
            </div>
          )}
        </DragOverlay>,
        document.body,
      )}
    </DndContext>
  )
}
