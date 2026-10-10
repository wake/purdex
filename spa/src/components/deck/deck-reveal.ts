// spa/src/components/deck/deck-reveal.ts — when a far-off turn of the deck is drawn in full (#2469). The last EAGER_TURNS turns
// always are; an older turn draws its agent text as plain text until it comes within about one screen of the scroll box, then
// swaps to the real markdown for good (`deck-reveal-memory`). One IntersectionObserver on the scroll box serves every turn.
import { createContext, useEffect, useMemo, useRef, type RefObject } from 'react'
import { flushSync } from 'react-dom'
import { SCROLL_ANCHOR_CLASS } from '../../lib/nex/transcript-scroll-memory'
import { captureTextAnchor, currentTextTop, type TextAnchor } from './deck-anchor'

/** The newest turns, always drawn in full: the reader is at (or near) the end. */
export const EAGER_TURNS = 20

export interface DeckReveal {
  /** The deck's memory key (`${paneId}\0${sessionId}`). */
  memKey: string
  /** Calls `onNear` once `el` is within a screen of the box; returns the way to stop watching. */
  observe(el: Element, onNear: () => void): () => void
}

export const DeckRevealContext = createContext<DeckReveal | null>(null)

/** What the reader is looking at: the first item showing at the top of the box and, when the item reaches above the box, the words at the top. */
export interface ViewAnchor {
  el: Element
  top: number
  text: TextAnchor | null
}

/**
 * The first item showing at the top of `box` (the first one whose bottom is below the box's top) and where its top is, plus the
 * words at the box's top when that item is cut by it and still plain text: a tall message that is itself swapped keeps its top
 * while the line under the reader moves, so the item's top alone cannot hold the place.
 */
export function captureViewAnchor(box: HTMLElement): ViewAnchor | null {
  const rect = box.getBoundingClientRect()
  for (const turn of box.getElementsByClassName(SCROLL_ANCHOR_CLASS)) {
    if (turn.getBoundingClientRect().bottom <= rect.top) continue
    for (const item of turn.children) {
      const r = item.getBoundingClientRect()
      if (r.bottom <= rect.top) continue
      const cut = r.top < rect.top && item.querySelector('[data-testid="room-prose-light"]') !== null
      return { el: item, top: r.top, text: cut ? captureTextAnchor(item, rect.left + 24, rect.top + 2) : null }
    }
    return null
  }
  return null
}

/** How far the anchored place has moved since it was captured (positive: down). */
export function viewAnchorMoved(anchor: ViewAnchor): number {
  if (!anchor.el.isConnected) return 0
  if (anchor.text) {
    const now = currentTextTop(anchor.text)
    if (now !== null) return now - anchor.text.top
  }
  return anchor.el.getBoundingClientRect().top - anchor.top
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
          // The browser's anchoring is switched off while it happens: it compensates some swaps (when its anchor node survives
          // them) and not others, and compensating on top of it moved the reader by the difference twice.
          const before = box ? captureViewAnchor(box) : null
          const anchoring = box?.style.overflowAnchor ?? ''
          if (box) box.style.overflowAnchor = 'none'
          try {
            flushSync(() => { for (const fn of swaps) fn() })
            if (box && before) {
              const moved = viewAnchorMoved(before)
              if (Math.abs(moved) >= 0.5) box.scrollTop += moved
            }
          } finally {
            if (box) box.style.overflowAnchor = anchoring
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
