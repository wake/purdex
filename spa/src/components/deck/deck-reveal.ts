// spa/src/components/deck/deck-reveal.ts — when a far-off turn of the deck is drawn in full (#2469). The last EAGER_TURNS turns
// always are; an older turn draws its agent text as plain text until it comes within about one screen of the scroll box, then
// swaps to the real markdown for good (`deck-reveal-memory`). One IntersectionObserver on the scroll box serves every turn.
import { createContext, useEffect, useMemo, useRef, type RefObject } from 'react'

/** The newest turns, always drawn in full: the reader is at (or near) the end. */
export const EAGER_TURNS = 20

export interface DeckReveal {
  /** The deck's memory key (`${paneId}\0${sessionId}`). */
  memKey: string
  /** Calls `onNear` once `el` is within a screen of the box; returns the way to stop watching. */
  observe(el: Element, onNear: () => void): () => void
}

export const DeckRevealContext = createContext<DeckReveal | null>(null)

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
          for (const entry of entries) {
            if (!entry.isIntersecting) continue
            const fn = waiting.current.get(entry.target)
            if (!fn) continue
            waiting.current.delete(entry.target)
            observer.current?.unobserve(entry.target)
            fn()
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
