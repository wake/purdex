import { useEffect, useLayoutEffect, useRef, type ReactNode, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { X } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'

export interface FloatingPanelProps {
  title: string
  /** Element the panel opens under; its rect decides the initial position. */
  anchorRef: RefObject<HTMLElement | null>
  onClose: () => void
  width?: number
  testId?: string
  children: ReactNode
}

const PADDING = 4
const Z_INDEX = 100
/** How much of the panel must stay on screen when dragged. */
const MIN_VISIBLE = 40

/**
 * Draggable floating window rendered into `document.body`: opens under `anchorRef`,
 * clamped to the viewport; the title bar is the drag handle; closes on Escape, on
 * the × button, and on mousedown outside the panel **and** outside the anchor (the
 * anchor's own click toggles the panel — swallowing its mousedown would re-open
 * what we just closed).
 *
 * Position is imperative DOM style (like `BreadcrumbPopover`'s anchor-rect layout
 * effect), not React state: dragging fires on every `pointermove`, and driving that
 * through `setState` would either lint-fail (`set-state-in-effect`-style cascades
 * apply to the initial placement effect too) or force a re-render per pixel moved.
 */
export function FloatingPanel({ title, anchorRef, onClose, width = 320, testId = 'floating-panel', children }: FloatingPanelProps) {
  const t = useI18nStore((s) => s.t)
  const panelRef = useRef<HTMLDivElement>(null)
  const posRef = useRef<{ left: number; top: number }>({ left: PADDING, top: PADDING })
  const drag = useRef<{ pointerId: number; startX: number; startY: number; left: number; top: number } | null>(null)

  const applyPos = (left: number, top: number) => {
    posRef.current = { left, top }
    const el = panelRef.current
    if (el) {
      el.style.left = `${left}px`
      el.style.top = `${top}px`
    }
  }

  // Initial position: below the anchor, clamped so the whole panel is visible.
  // Runs once per mount (a fresh instance every time the caller re-opens the panel).
  useLayoutEffect(() => {
    const el = panelRef.current
    const a = anchorRef.current?.getBoundingClientRect()
    const h = el?.offsetHeight ?? 0
    let left = a ? a.left : PADDING
    let top = a ? a.bottom + PADDING : PADDING
    left = Math.max(PADDING, Math.min(left, window.innerWidth - width - PADDING))
    if (top + h > window.innerHeight - PADDING) top = Math.max(PADDING, (a ? a.top : window.innerHeight) - PADDING - h)
    top = Math.min(top, window.innerHeight - PADDING)
    applyPos(left, top)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    const onMouseDown = (e: MouseEvent) => {
      const target = e.target as Node
      if (panelRef.current?.contains(target)) return
      if (anchorRef.current?.contains(target)) return
      onClose()
    }
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.preventDefault()
      onClose()
    }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [anchorRef, onClose])

  const clamp = (left: number, top: number) => ({
    left: Math.max(MIN_VISIBLE - width, Math.min(left, window.innerWidth - MIN_VISIBLE)),
    top: Math.max(0, Math.min(top, window.innerHeight - MIN_VISIBLE)),
  })

  const content = (
    <div
      ref={panelRef}
      role="dialog"
      aria-label={title}
      data-testid={testId}
      className="fixed bg-surface-elevated border border-border-default rounded-lg shadow-xl flex flex-col"
      style={{ position: 'fixed', left: posRef.current.left, top: posRef.current.top, width, zIndex: Z_INDEX }}
    >
      <div
        data-testid="floating-panel-handle"
        className="flex items-center justify-between gap-2 px-3 py-1.5 border-b border-border-default cursor-move select-none touch-none"
        onPointerDown={(e) => {
          if (e.button !== 0) return
          drag.current = { pointerId: e.pointerId, startX: e.clientX, startY: e.clientY, left: posRef.current.left, top: posRef.current.top }
          if (typeof e.currentTarget.setPointerCapture === 'function') e.currentTarget.setPointerCapture(e.pointerId)
        }}
        onPointerMove={(e) => {
          const d = drag.current
          if (!d || d.pointerId !== e.pointerId) return
          const next = clamp(d.left + (e.clientX - d.startX), d.top + (e.clientY - d.startY))
          applyPos(next.left, next.top)
        }}
        onPointerUp={(e) => { if (drag.current?.pointerId === e.pointerId) drag.current = null }}
        onPointerCancel={() => { drag.current = null }}
        onLostPointerCapture={() => { drag.current = null }}
      >
        <span className="text-xs font-medium text-text-primary truncate">{title}</span>
        <button
          type="button"
          data-testid="floating-panel-close"
          aria-label={t('common.close')}
          onPointerDown={(e) => e.stopPropagation()}
          onClick={onClose}
          className="rounded p-0.5 text-text-muted hover:text-text-primary hover:bg-surface-hover cursor-pointer"
        >
          <X size={14} />
        </button>
      </div>
      <div className="p-3">{children}</div>
    </div>
  )
  return createPortal(content, document.body)
}
