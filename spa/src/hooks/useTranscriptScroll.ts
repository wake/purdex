// spa/src/hooks/useTranscriptScroll.ts — a transcript's stick-to-bottom, shared
// by RoomTranscript and ChatTranscript.
//
// The transcript calls `follow()` from its own effect whenever it grows (its
// deps say what growth is). The first call (mount, or a view switch
// remounting it) jumps to the bottom at once; later ones animate (F3).
//
// **Holding (R3 plan T3.3, A4).** While `hold` is on — the search bar is
// open — growth follows the bottom only if the reader was already there, so
// a streaming reply cannot pull them away from a match. "Already there" is a
// flag kept from the scroll position: reaching the bottom sets it; moving up
// clears it; moving down short of the bottom leaves it as it was — that is
// what a smooth scroll to the bottom looks like while it is still under way.
// `follow` re-reads the position itself before deciding, because a jump to a
// match moves `scrollTop` synchronously and its scroll event has not arrived
// yet when the next commit lands.
import { useCallback, useLayoutEffect, useMemo, useRef, type Ref, type UIEvent } from 'react'

/** Within this many pixels of the end counts as the bottom (sub-pixel rounding, a last line's margin). */
const NEAR_BOTTOM = 24

export interface TranscriptScroll {
  /** The container's callback ref: set on the scrolling div (forwards to the caller's `scrollRef`). */
  attach: (node: HTMLDivElement | null) => void
  /** The container's onScroll. */
  onScroll: (e: UIEvent<HTMLDivElement>) => void
  /** Scroll to the bottom, unless holding and the reader is elsewhere. */
  follow: () => void
}

/** Hands `node` to a caller's ref, whichever kind it is. */
function assignRef(target: Ref<HTMLDivElement> | undefined, node: HTMLDivElement | null): void {
  if (typeof target === 'function') target(node)
  else if (target) target.current = node
}

export function useTranscriptScroll(external: Ref<HTMLDivElement> | undefined, hold: boolean): TranscriptScroll {
  const box = useRef<HTMLDivElement | null>(null)
  const atBottom = useRef(true)
  const lastTop = useRef(0)
  const scrolled = useRef(false)
  const holding = useRef(hold)
  // Before any passive effect of the same commit reads it.
  useLayoutEffect(() => { holding.current = hold }, [hold])

  const attach = useCallback((node: HTMLDivElement | null) => {
    box.current = node
    assignRef(external, node)
  }, [external])

  const observe = useCallback(() => {
    const el = box.current
    if (!el) return
    const top = el.scrollTop
    if (el.scrollHeight - top - el.clientHeight <= NEAR_BOTTOM) atBottom.current = true
    else if (top < lastTop.current) atBottom.current = false
    lastTop.current = top
  }, [])

  const follow = useCallback(() => {
    const el = box.current
    if (!el?.scrollTo) return
    observe()
    if (holding.current && scrolled.current && !atBottom.current) return
    el.scrollTo({ top: el.scrollHeight, behavior: scrolled.current ? 'smooth' : 'auto' })
    scrolled.current = true
    atBottom.current = true
  }, [observe])

  return useMemo(() => ({ attach, onScroll: observe, follow }), [attach, observe, follow])
}
