// spa/src/components/deck/deck-reveal.ts — when a far-off turn of the deck is drawn in full (#2469). The last EAGER_TURNS turns
// always are; an older turn draws its agent text as plain text until it comes within about one screen of the scroll box, then
// swaps to the real markdown for good (`deck-reveal-memory`). One IntersectionObserver on the scroll box serves every turn.
import { createContext, useEffect, useMemo, useRef, type RefObject } from 'react'
import { flushSync } from 'react-dom'
import { SCROLL_ANCHOR_CLASS } from '../../lib/nex/transcript-scroll-memory'

/** The newest turns, always drawn in full: the reader is at (or near) the end. */
export const EAGER_TURNS = 20

export interface DeckReveal {
  /** The deck's memory key (`${paneId}\0${sessionId}`). */
  memKey: string
  /** Calls `onNear` once `el` is within a screen of the box; returns the way to stop watching. */
  observe(el: Element, onNear: () => void): () => void
}

export const DeckRevealContext = createContext<DeckReveal | null>(null)

/**
 * The first item showing at the top of `box` (the first one whose bottom is below the box's top) and where its top is. What the
 * reader is looking at, to be put back after a swap changed the height above it.
 */
function firstShowingItem(box: HTMLElement): { el: Element; top: number } | null {
  const boxTop = box.getBoundingClientRect().top
  for (const turn of box.getElementsByClassName(SCROLL_ANCHOR_CLASS)) {
    if (turn.getBoundingClientRect().bottom <= boxTop) continue
    for (const item of turn.children) {
      const rect = item.getBoundingClientRect()
      if (rect.bottom > boxTop) return { el: item, top: rect.top }
    }
    return null
  }
  return null
}

export function useDeckReveal(memKey: string, boxRef: RefObject<HTMLElement | null>): DeckReveal {
  const observer = useRef<IntersectionObserver | null>(null)
  const waiting = useRef(new Map<Element, () => void>())
  useEffect(() => () => {
    observer.current?.disconnect()
    observer.current = null
  }, [])
  return useMemo<DeckReveal>(() => ({
    memKey,
    observe(el, onNear) {
      // No observer to ask (an old webview, jsdom): draw everything in full, as before.
      if (typeof IntersectionObserver === 'undefined') { onNear(); return () => {} }
      if (!observer.current) {
        observer.current = new IntersectionObserver((entries) => {
          const swaps: Array<() => void> = []
          for (const entry of entries) {
            if (!entry.isIntersecting) continue
            const fn = waiting.current.get(entry.target)
            if (!fn) continue
            waiting.current.delete(entry.target)
            observer.current?.unobserve(entry.target)
            swaps.push(fn)
          }
          if (swaps.length === 0) return
          // A swap changes the height above whatever the reader is looking at. The browser's own scroll anchoring does it after
          // a smooth scroll, but not after a jump (a scrollbar drag lands on plain-text turns that swap a frame later and the
          // text under the reader moved by the whole difference, 55-110 px measured), so put the first showing item back
          // where it was: the swaps land at once, then the box is moved by however far that item moved. When the browser has
          // already compensated the difference is 0.
          const box = boxRef.current
          const before = box ? firstShowingItem(box) : null
          flushSync(() => { for (const fn of swaps) fn() })
          if (box && before?.el.isConnected) {
            const moved = before.el.getBoundingClientRect().top - before.top
            if (Math.abs(moved) >= 0.5) box.scrollTop += moved
          }
        }, { root: boxRef.current, rootMargin: '100% 0px' })
      }
      waiting.current.set(el, onNear)
      observer.current.observe(el)
      return () => {
        waiting.current.delete(el)
        observer.current?.unobserve(el)
      }
    },
  }), [memKey, boxRef])
}
