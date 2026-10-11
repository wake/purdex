// spa/src/hooks/useKeepPlaceOnTypeChange.ts — a new type size or line height re-flows every turn; the reader keeps their place (c9d9c7-60).
// The store is read when it changes (before React draws the new size), so the first showing item and the words at the top of the box are
// captured against the old layout; once the new size is in the DOM the box is moved by however far that place went. The deck's far-off
// plain-text turns (#2469) and the markdown cache (#2510) hold no heights or sizes — the cache keeps rendered HTML, which does not depend on
// the type — so nothing there needs dropping; the observer swaps simply measure the new layout.
import { useLayoutEffect, useEffect, useRef } from 'react'
import { captureViewAnchor, viewAnchorMoved, type ViewAnchor } from '../components/deck/deck-reveal'
import { useDeckTypeStore } from '../stores/useDeckTypeStore'

/** `shiftBy` is the scroll hook's (content above the reader changed height by that much: keep what is on screen where it is; a reader at the bottom stays there). */
export function useKeepPlaceOnTypeChange(box: HTMLElement | null, shiftBy: (delta: number) => void): void {
  const pending = useRef<ViewAnchor | null>(null)
  const fontSize = useDeckTypeStore((s) => s.fontSize)
  const lineHeight = useDeckTypeStore((s) => s.lineHeight)
  useEffect(() => {
    if (!box) return
    return useDeckTypeStore.subscribe((s, prev) => {
      if (s.fontSize !== prev.fontSize || s.lineHeight !== prev.lineHeight) pending.current = captureViewAnchor(box)
    })
  }, [box])
  useLayoutEffect(() => {
    const anchor = pending.current
    pending.current = null
    if (!anchor || !box) return
    const moved = viewAnchorMoved(anchor)
    if (Math.abs(moved) >= 0.5) shiftBy(moved)
  }, [box, shiftBy, fontSize, lineHeight])
}
