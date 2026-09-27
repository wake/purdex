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
//
// Mounted while holding (room ⇄ chat under an open bar), the first call
// does not jump: the search bar puts the reader back on the current match,
// or at the bottom when there is none (R1-1).
//
// **Releasing (A F4).** A jump to a match in the last screen leaves
// `scrollTop` clamped within NEAR_BOTTOM of the end, which reads as "at the
// bottom", and the next line would push the match off screen. The search bar
// calls `release()` after every jump: following stops, and the position the
// jump left is remembered so its own (late) scroll event does not count —
// only the reader moving away from it, back to the bottom, resumes it.
// The release holds whether or not the search bar is open: the dock's
// inspect jump (R4 T3.2) releases with the bar closed, and the next streamed
// line must not pull the reader back down. Without a release, a closed bar
// follows growth as ever — and closing the bar drops any release, so the
// next growth follows the bottom again.
import { useCallback, useImperativeHandle, useLayoutEffect, useMemo, useRef, type Ref, type UIEvent } from 'react'

/** Within this many pixels of the end counts as the bottom (sub-pixel rounding, a last line's margin). */
const NEAR_BOTTOM = 24

export interface TranscriptScroll {
  /** The container's callback ref: set on the scrolling div (forwards to the caller's `scrollRef`). */
  attach: (node: HTMLDivElement | null) => void
  /** The container's onScroll. */
  onScroll: (e: UIEvent<HTMLDivElement>) => void
  /** Scroll to the bottom, unless holding (or released) and the reader is elsewhere. */
  follow: () => void
  /** Stop following where the box is now, until the reader scrolls (A F4). */
  release: () => void
}

/** What a transcript hands its `scrollControl` ref (the search bar's handle). */
export interface TranscriptScrollControl {
  release: () => void
}

/** Exposes `scroll.release` on a transcript's `scrollControl` prop. */
export function useScrollControl(control: Ref<TranscriptScrollControl> | undefined, scroll: TranscriptScroll): void {
  const { release } = scroll
  useImperativeHandle(control, () => ({ release }), [release])
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
  // The scrollTop a `release()` left, until the reader moves off it.
  const released = useRef<number | null>(null)
  // A `release()` holds the bottom-follow — bar open or not — until the
  // reader is back at the bottom.
  const releaseHold = useRef(false)
  const holding = useRef(hold)
  // Before any passive effect of the same commit reads it.
  useLayoutEffect(() => {
    // The bar closing (hold true → false) is the explicit "back to live"
    // gesture: it drops any release — a search jump's or an earlier inspect
    // jump's — so the next growth follows the bottom again (alpha.463:
    // 關掉搜尋列就恢復自動捲到底). An inspect release with the bar closed
    // throughout never sees this transition and keeps holding.
    if (holding.current && !hold) {
      releaseHold.current = false
      released.current = null
    }
    holding.current = hold
  }, [hold])

  const attach = useCallback((node: HTMLDivElement | null) => {
    box.current = node
    assignRef(external, node)
  }, [external])

  const observe = useCallback(() => {
    const el = box.current
    if (!el) return
    const top = el.scrollTop
    if (released.current !== null) {
      // Still where the jump left it (its own scroll event, or growth).
      if (top === released.current) return
      released.current = null
    }
    if (el.scrollHeight - top - el.clientHeight <= NEAR_BOTTOM) {
      atBottom.current = true
      releaseHold.current = false
    } else if (top < lastTop.current) atBottom.current = false
    lastTop.current = top
  }, [])

  const follow = useCallback(() => {
    const el = box.current
    if (!el?.scrollTo) return
    observe()
    if (holding.current && !scrolled.current) {
      // Mounted under an open search bar (a view switch): the bar places
      // the reader — at the current match, or the bottom — not this (R1-1).
      scrolled.current = true
      return
    }
    if ((holding.current || releaseHold.current) && !atBottom.current) return
    el.scrollTo({ top: el.scrollHeight, behavior: scrolled.current ? 'smooth' : 'auto' })
    scrolled.current = true
    atBottom.current = true
    released.current = null
    releaseHold.current = false
  }, [observe])

  const release = useCallback(() => {
    const el = box.current
    if (!el) return
    atBottom.current = false
    releaseHold.current = true
    released.current = el.scrollTop
    lastTop.current = el.scrollTop
  }, [])

  return useMemo(() => ({ attach, onScroll: observe, follow, release }), [attach, observe, follow, release])
}
