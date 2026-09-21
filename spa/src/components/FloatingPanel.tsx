import { useEffect, useLayoutEffect, useRef, type ReactNode, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { X } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { getPlatformCapabilities } from '../lib/platform'

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
/** Height of the Electron title bar's OS drag region (see `TitleBar.tsx`, which
 * is only rendered under Electron — `App.tsx` gates it on the same
 * `getPlatformCapabilities().isElectron` check used below). The panel's top
 * must never land inside it — that region intercepts pointer events for
 * window-dragging, so a panel header there could be neither dragged nor have
 * its × clicked. In the browser there's no such region, so the inset is just
 * the ordinary viewport padding. */
export const TITLE_BAR_HEIGHT = 36
/** Floor on the panel's usable height when auto-placed below an anchor near the
 * bottom of the viewport — it slides up only as much as needed to keep at least
 * this much room, rather than opening with almost nothing to show. */
const MIN_PANEL_HEIGHT = 160
const FOCUSABLE_SELECTOR = 'input, button, [tabindex]:not([tabindex="-1"]), select, textarea'
/** IME composition sends a synthetic Escape to close the IME's own suggestion
 * popup; `keyCode === 229` is the legacy fallback for engines that don't set
 * `isComposing` on that event. Either signal means "not really Escape". */
const isImeEscape = (e: KeyboardEvent) => e.isComposing || e.keyCode === 229
/** Escape only closes the topmost panel when several are open (mouse-driven
 * outside-click stays per-instance — clicking another panel is "outside" for
 * this one, that's fine; only the keyboard needs a single, unambiguous target). */
const openPanels: symbol[] = []

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
  // Only the Electron title bar's drag region needs the panel pushed down —
  // `App.tsx` renders `TitleBar` under the same check, so this stays in sync
  // with whether that region actually exists on screen.
  const topInset = getPlatformCapabilities().isElectron ? TITLE_BAR_HEIGHT : PADDING
  const panelRef = useRef<HTMLDivElement>(null)
  const posRef = useRef<{ left: number; top: number }>({ left: PADDING, top: PADDING })
  const drag = useRef<{ pointerId: number; startX: number; startY: number; left: number; top: number } | null>(null)
  /** Set on the first `pointermove` of a drag; once true, scroll/resize clamp the
   * user's chosen position instead of re-anchoring under a (possibly moved) anchor. */
  const draggedRef = useRef(false)
  const idRef = useRef<symbol>(Symbol())

  const applyPos = (left: number, top: number) => {
    posRef.current = { left, top }
    const el = panelRef.current
    if (el) {
      el.style.left = `${left}px`
      el.style.top = `${top}px`
    }
  }

  // No `MIN_PANEL_HEIGHT` floor here — that belongs only to `place()`, which
  // chooses `top` so 160px fits when the viewport allows it. Flooring the
  // *height* here too would let a dragged-down or resized-small panel grow
  // past the bottom of the viewport; the only floor a height needs is 0.
  const applyMaxHeight = (top: number) => {
    const el = panelRef.current
    if (el) el.style.maxHeight = `${Math.max(0, window.innerHeight - top - PADDING)}px`
  }

  const clamp = (left: number, top: number) => ({
    left: Math.max(MIN_VISIBLE - width, Math.min(left, window.innerWidth - MIN_VISIBLE)),
    top: Math.max(topInset, Math.min(top, window.innerHeight - MIN_VISIBLE)),
  })

  // Always below the anchor — never the "above" fallback, so the panel stays next
  // to the field that opened it instead of jumping to wherever it happens to fit.
  // Clamped on both ends: never above `topInset` (the Electron title bar's drag
  // region, or ordinary padding in the browser), and never so low that less than
  // `MIN_PANEL_HEIGHT` of the viewport remains below it — past that point the
  // panel slides up just enough to keep that floor, rather than opening with
  // almost nothing to show. `maxHeight` then fits the panel from there to the
  // bottom edge, with the body scrolling for the rest. Used for the initial
  // placement and to re-anchor on scroll/resize while the panel hasn't been
  // dragged.
  const place = () => {
    const a = anchorRef.current?.getBoundingClientRect()
    let left = a ? a.left : PADDING
    let top = a ? a.bottom + PADDING : PADDING
    left = Math.max(PADDING, Math.min(left, window.innerWidth - width - PADDING))
    top = Math.max(topInset, Math.min(top, window.innerHeight - PADDING - MIN_PANEL_HEIGHT))
    applyPos(left, top)
    applyMaxHeight(top)
  }

  // Initial position. Runs once per mount (a fresh instance every time the caller
  // re-opens the panel).
  useLayoutEffect(() => {
    place()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Follow the anchor across layout changes: a resize, or a scroll on any ancestor
  // (the Host page's own scroll container included — hence `capture: true` on
  // `document`, which sees scrolls on any element, not just itself). Once the panel
  // has been dragged, the user's placement wins; just keep it on-screen.
  useEffect(() => {
    const onReflow = () => {
      if (draggedRef.current) {
        const next = clamp(posRef.current.left, posRef.current.top)
        applyPos(next.left, next.top)
        applyMaxHeight(next.top)
      } else {
        place()
      }
    }
    window.addEventListener('resize', onReflow)
    document.addEventListener('scroll', onReflow, { capture: true })
    return () => {
      window.removeEventListener('resize', onReflow)
      document.removeEventListener('scroll', onReflow, { capture: true })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Focus management: non-modal, but still moves focus in on open (to the first
  // focusable descendant, skipping the × close button — it shouldn't steal the
  // opening keystroke's follow-through) and restores it to whatever had focus
  // before opening, once on unmount (only if that element is still around).
  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null
    const panel = panelRef.current
    const anchor = anchorRef.current
    if (panel) {
      const candidates = panel.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)
      const target = Array.from(candidates).find((el) => el.dataset.testid !== 'floating-panel-close')
      ;(target ?? panel).focus()
    }
    return () => {
      // Only restore if this panel still owns focus at unmount time — otherwise
      // some other element (e.g. another still-open FloatingPanel) already holds
      // it deliberately, and reclaiming it out from under that would be a bug,
      // not a courtesy.
      const active = document.activeElement
      const stillOwnsFocus = active === null || active === document.body || (panel?.contains(active) ?? false)
      if (!stillOwnsFocus) return
      // The remembered element may itself be gone by the time we unmount (e.g. it
      // belonged to another panel that closed first) — fall back to our own anchor.
      const restoreTarget = previouslyFocused?.isConnected ? previouslyFocused : anchor?.isConnected ? anchor : null
      restoreTarget?.focus()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Track the open-panel stack so Escape (below) only acts on the topmost one.
  useEffect(() => {
    const id = idRef.current
    openPanels.push(id)
    return () => {
      const i = openPanels.indexOf(id)
      if (i !== -1) openPanels.splice(i, 1)
    }
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
      if (isImeEscape(e)) return
      if (openPanels[openPanels.length - 1] !== idRef.current) return
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

  const content = (
    <div
      ref={panelRef}
      role="dialog"
      aria-label={title}
      aria-modal="false"
      tabIndex={-1}
      data-testid={testId}
      className="fixed bg-surface-elevated border border-border-default rounded-lg shadow-xl flex flex-col"
      style={{
        position: 'fixed',
        left: posRef.current.left,
        top: posRef.current.top,
        width,
        zIndex: Z_INDEX,
        maxHeight: window.innerHeight - posRef.current.top - PADDING,
        // Otherwise the panel sits inside the Electron title bar's OS drag
        // region and can neither be dragged nor have its × clicked (see `TitleBar.tsx`).
        WebkitAppRegion: 'no-drag',
      } as React.CSSProperties}
    >
      <div
        data-testid="floating-panel-handle"
        className="flex items-center justify-between gap-2 px-3 py-1.5 border-b border-border-default cursor-move select-none touch-none"
        style={{ flex: '0 0 auto' }}
        onPointerDown={(e) => {
          if (e.button !== 0) return
          drag.current = { pointerId: e.pointerId, startX: e.clientX, startY: e.clientY, left: posRef.current.left, top: posRef.current.top }
          if (typeof e.currentTarget.setPointerCapture === 'function') e.currentTarget.setPointerCapture(e.pointerId)
        }}
        onPointerMove={(e) => {
          const d = drag.current
          if (!d || d.pointerId !== e.pointerId) return
          draggedRef.current = true
          const next = clamp(d.left + (e.clientX - d.startX), d.top + (e.clientY - d.startY))
          applyPos(next.left, next.top)
          // Re-derive as the drag moves: dragged down near the bottom, the panel
          // must not extend past the viewport; dragged back up, it grows back.
          applyMaxHeight(next.top)
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
      <div className="p-3" style={{ overflowY: 'auto', minHeight: 0 }}>{children}</div>
    </div>
  )
  return createPortal(content, document.body)
}
