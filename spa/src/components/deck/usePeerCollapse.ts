// spa/src/components/deck/usePeerCollapse.ts — a peer message folds to its first three visual lines (deck rules 2026-10-11): click anywhere on the
// block to open or close it; hover brightens it, but only when there is something to fold (a short message is not clickable, so it gives no such hint).
// Open/closed lives in the pane's fold memory (`useFold`): the deck and chat unmount with their tab.
import { useCallback, useLayoutEffect, useRef, useState, type KeyboardEvent, type MouseEvent } from 'react'
import { useFold } from '../room/fold-context'

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

  useLayoutEffect(() => {
    const el = bodyRef.current
    if (!el) return
    const measure = () => setCollapsible(exceedsLines(el))
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [content])

  const onClick = useCallback((e: MouseEvent<HTMLElement>) => {
    const sel = window.getSelection()
    // A drag-select to copy is not a click on the block.
    if (sel && !sel.isCollapsed && sel.toString() !== '' && sel.anchorNode && e.currentTarget.contains(sel.anchorNode)) return
    toggle()
  }, [toggle])
  const onKeyDown = useCallback((e: KeyboardEvent<HTMLElement>) => {
    if (e.target !== e.currentTarget) return
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggle() }
  }, [toggle])

  const folded = collapsible && !open
  return {
    collapsible,
    open,
    bodyRef,
    /** Spread on the whole block; empty for a message that fits in three lines (not a button, no hint). */
    boxProps: collapsible
      ? { role: 'button' as const, tabIndex: 0, 'aria-expanded': open, onClick, onKeyDown }
      : {},
    /** Frame class when it can be folded (the hover group lives on the frame). */
    boxClass: collapsible ? 'group cursor-pointer hover:border-peer' : '',
    /** Body class: one step brighter on hover, clamped to three lines while folded. */
    bodyClass: `${collapsible ? 'group-hover:text-text-primary ' : ''}${folded ? 'line-clamp-3' : ''}`,
  }
}
