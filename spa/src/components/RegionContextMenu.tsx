import { useEffect, useLayoutEffect, useRef } from 'react'
import { CheckSquare, Square } from '@phosphor-icons/react'
import { useLayoutStore } from '../stores/useLayoutStore'
import { getAllViews } from '../lib/module-registry'
import type { SidebarRegion } from '../types/layout'

interface Props {
  region: SidebarRegion
  position: { x: number; y: number }
  onClose: () => void
}

export function RegionContextMenu({ region, position, onClose }: Props) {
  const views = useLayoutStore((s) => s.regions[region].views)
  const addView = useLayoutStore((s) => s.addView)
  const removeView = useLayoutStore((s) => s.removeView)

  const ref = useRef<HTMLDivElement>(null)

  const allViews = getAllViews()
  const enabledSet = new Set(views)

  // Enabled views in region order, then available in registry order
  const enabledViews = views
    .map((id) => allViews.find((v) => v.id === id))
    .filter(Boolean) as typeof allViews
  const availableViews = allViews.filter((v) => !enabledSet.has(v.id))
  const orderedViews = [...enabledViews, ...availableViews]

  // Viewport correction before paint
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const rect = el.getBoundingClientRect()
    let { x, y } = position
    if (x + rect.width > window.innerWidth) x = window.innerWidth - rect.width - 4
    if (y + rect.height > window.innerHeight) y = window.innerHeight - rect.height - 4
    if (x < 0) x = 4
    if (y < 0) y = 4
    el.style.left = `${x}px`
    el.style.top = `${y}px`
  }, [position])

  // Close on outside mousedown
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        onClose()
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [onClose])

  // Close on Escape
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [onClose])

  const handleToggle = (viewId: string) => {
    if (enabledSet.has(viewId)) {
      removeView(region, viewId)
    } else {
      addView(region, viewId)
    }
  }

  return (
    <div
      ref={ref}
      className="fixed z-50 bg-surface-elevated border border-border-default rounded-lg shadow-xl py-1 min-w-[180px] text-xs"
      style={{ left: position.x, top: position.y }}
    >
      {orderedViews.map((viewDef) => {
        const enabled = enabledSet.has(viewDef.id)
        return (
          <button
            key={viewDef.id}
            className="w-full flex items-center gap-2 px-3 py-1.5 hover:bg-surface-hover transition-colors cursor-pointer"
            onClick={() => handleToggle(viewDef.id)}
          >
            {enabled ? (
              <CheckSquare size={14} className="text-text-primary shrink-0" />
            ) : (
              <Square size={14} className="text-text-muted shrink-0" />
            )}
            <span className="flex-1 text-left">{viewDef.label}</span>
          </button>
        )
      })}
      {orderedViews.length === 0 && (
        <div className="px-3 py-1.5 text-text-muted">沒有可用的 views</div>
      )}
    </div>
  )
}
