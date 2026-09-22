import { useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import {
  DndContext, DragOverlay, PointerSensor, useSensor, useSensors,
  type DragEndEvent, type DragStartEvent, pointerWithin,
} from '@dnd-kit/core'
import { useNewTabProviders } from '../../../hooks/useNewTabProviders'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import type { PresetKey } from '../../../lib/resolve-preset'
import { useI18nStore } from '../../../stores/useI18nStore'
import { NewTabModulePalette, type PaletteItem } from './NewTabModulePalette'
import { NewTabPresetSwitcher } from './NewTabPresetSwitcher'
import { NewTabCanvas } from './NewTabCanvas'
import { NewTabThumbnail } from './NewTabThumbnail'

export function NewTabSubsection() {
  const t = useI18nStore((s) => s.t)
  const providers = useNewTabProviders()
  const presets = useNewTabLayoutStore((s) => s.presets)
  const active = useNewTabLayoutStore((s) => s.activeEditingPreset)
  const setEditing = useNewTabLayoutStore((s) => s.setEditing)
  const setEnabled = useNewTabLayoutStore((s) => s.setEnabled)
  const placeModule = useNewTabLayoutStore((s) => s.placeModule)
  const removeModule = useNewTabLayoutStore((s) => s.removeModule)
  const placeModuleInShortest = useNewTabLayoutStore((s) => s.placeModuleInShortest)

  const [dragging, setDragging] = useState<null | { providerId: string; source: 'palette' | 'canvas' }>(null)

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))

  const paletteItems: PaletteItem[] = useMemo(() => {
    const activeIds = new Set(presets[active].columns.flat())
    return providers.map((p) => ({
      id: p.id,
      label: p.label,
      labelParams: p.labelParams,
      inUse: activeIds.has(p.id),
      unavailable: p.disabled,
    }))
  }, [providers, presets, active])

  const handleClickAdd = (id: string) => {
    placeModuleInShortest(active, id)
  }

  const handleDragStart = (e: DragStartEvent) => {
    const d = e.active.data.current as { type?: string; providerId?: string } | undefined
    if (!d?.providerId) return
    if (d.type === 'palette') {
      setDragging({ providerId: d.providerId, source: 'palette' })
    } else if (d.type === 'canvas-item') {
      setDragging({ providerId: d.providerId, source: 'canvas' })
    }
  }

  const handleDragEnd = (e: DragEndEvent) => {
    const activeEvt = e.active
    const over = e.over
    setDragging(null)
    if (!over) return

    const src = activeEvt.data.current as { type?: string; providerId?: string; presetKey?: PresetKey } | undefined
    const dst = over.data.current as { type?: string; presetKey?: PresetKey; colIdx?: number; providerId?: string } | undefined
    if (!src?.providerId) return

    // Drop into palette zone = remove from canvas
    if (dst?.type === 'palette-zone') {
      if (src.type === 'canvas-item' && src.presetKey) {
        removeModule(src.presetKey, src.providerId)
      }
      return
    }

    // Drop onto another canvas item = insert at its position
    if (dst?.type === 'canvas-item' && dst.presetKey && dst.providerId) {
      const cols = useNewTabLayoutStore.getState().presets[dst.presetKey].columns
      const colIdx = cols.findIndex((c) => c.includes(dst.providerId!))
      if (colIdx < 0) return
      const rowIdx = cols[colIdx].indexOf(dst.providerId)
      placeModule(dst.presetKey, src.providerId, colIdx, rowIdx)
      return
    }

    // Drop onto column empty area = append to that column
    if (dst?.type === 'column' && dst.presetKey && typeof dst.colIdx === 'number') {
      const cols = useNewTabLayoutStore.getState().presets[dst.presetKey].columns
      placeModule(dst.presetKey, src.providerId, dst.colIdx, cols[dst.colIdx].length)
    }
  }

  const draggingProvider = dragging ? providers.find((p) => p.id === dragging.providerId) : undefined
  const overlayLabel = dragging
    ? (draggingProvider ? t(draggingProvider.label, draggingProvider.labelParams) : dragging.providerId)
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
        <NewTabPresetSwitcher
          active={active}
          onSelect={setEditing}
          onToggleEnabled={setEnabled}
          renderMain={(k) => <NewTabCanvas presetKey={k} />}
          renderThumb={(k) => <NewTabThumbnail presetKey={k} />}
        />
      </div>
      {typeof document !== 'undefined' && createPortal(
        <DragOverlay>
          {overlayLabel && (
            <div className="px-2 py-1 rounded-md bg-surface-elevated border border-border-active text-xs shadow-lg">
              {overlayLabel}
            </div>
          )}
        </DragOverlay>,
        document.body,
      )}
    </DndContext>
  )
}
