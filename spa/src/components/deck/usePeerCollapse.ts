// spa/src/components/deck/usePeerCollapse.ts — a peer message folds to its first three visual lines (deck rules 2026-10-11): click anywhere on the
// block to open or close it; hover brightens it, but only when there is something to fold (a short message is not clickable, so it gives no such hint).
// Open/closed lives in the pane's fold memory (`useFold`): the deck and chat unmount with their tab.
import { useCallback, useId, useLayoutEffect, useRef, useState, type MouseEvent } from 'react'
import { useFold } from '../room/fold-context'
import { useI18nStore } from '../../stores/useI18nStore'

export const PEER_FOLD_LINES = 3

/** True when the element's content is taller than three of its lines. Works while clamped (scrollHeight is the full height) and while open. */
export function exceedsLines(el: HTMLElement, lines = PEER_FOLD_LINES): boolean {
  const cs = getComputedStyle(el)
  let lh = parseFloat(cs.lineHeight)
  if (!Number.isFinite(lh)) lh = (parseFloat(cs.fontSize) || 14) * 1.4
  return el.scrollHeight > lh * lines + 1
}

export function usePeerCollapse(foldKey: string, content: string) {
  const [open, toggle] = useFold(foldKey)
  const bodyRef = useRef<HTMLDivElement>(null)
  const [collapsible, setCollapsible] = useState(false)
  const t = useI18nStore((st) => st.t)
  const bodyId = useId()

  useLayoutEffect(() => {
    const el = bodyRef.current
    if (!el) return
    const measure = () => setCollapsible(exceedsLines(el))
    measure()
    // A webfont that lands after the first measure re-wraps the text without necessarily changing the box height: measure again once fonts settle.
    let alive = true
    const remeasure = () => { if (alive) measure() }
    const fonts = typeof document !== 'undefined' ? document.fonts : undefined
    if (fonts) {
      fonts.ready?.then(remeasure, () => {})
      fonts.addEventListener?.('loadingdone', remeasure)
    }
    let ro: ResizeObserver | undefined
    if (typeof ResizeObserver !== 'undefined') {
      ro = new ResizeObserver(measure)
      ro.observe(el)
    }
    return () => {
      alive = false
      ro?.disconnect()
      fonts?.removeEventListener?.('loadingdone', remeasure)
    }
  }, [content])

  const onClick = useCallback((e: MouseEvent<HTMLElement>) => {
    // A link or button inside the block does its own job (the toggle button toggles itself).
    if ((e.target as HTMLElement).closest?.('a, button')) return
    const sel = window.getSelection()
    // A drag-select to copy is not a click on the block.
    if (sel && !sel.isCollapsed && sel.toString() !== '' && sel.anchorNode && e.currentTarget.contains(sel.anchorNode)) return
    toggle()
  }, [toggle])

  const folded = collapsible && !open
  return {
    collapsible,
    open,
    bodyRef,
    /** Spread on the whole block; a mouse click anywhere toggles it. Not a button itself: the keyboard / screen reader goes through `toggleProps`. */
    boxProps: collapsible ? { onClick } : {},
    /** Id for the body element (the toggle's aria-controls). */
    bodyId,
    /** Spread on `<button type="button">` placed inside the block (render only when `collapsible`); sr-only, shown by the frame's focus ring. */
    toggleProps: {
      className: 'sr-only',
      'aria-expanded': open,
      'aria-controls': bodyId,
      'aria-label': t(open ? 'deck.peerFold.collapse' : 'deck.peerFold.expand'),
      onClick: () => toggle(),
    },
    /** Frame class when it can be folded (the hover group lives on the frame). */
    boxClass: collapsible ? 'group cursor-pointer hover:border-peer focus-within:ring-2 focus-within:ring-peer' : '',
    /** Body class: one step brighter on hover, clamped to three lines while folded. */
    bodyClass: `${collapsible ? 'group-hover:text-text-primary ' : ''}${folded ? 'line-clamp-3' : ''}`,
  }
}
