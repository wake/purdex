// spa/src/hooks/useTranscriptScroll.ts — a transcript's stick-to-bottom, shared
// by RoomTranscript and ChatTranscript.
//
// The transcript calls `follow()` from its own effect whenever it grows (its
// deps say what growth is). The first call (mount, or a view switch
// remounting it) places the reader at once; later ones animate (F3).
//
// **One rule: growth follows only a reader at the bottom** (worker pane
// theme spec §6), search bar open or not. A tab the alive pool keeps under
// visibility:hidden that grows while its reader is scrolled up leaves them
// where they were. "At the bottom" is a flag kept from the scroll position,
// read on every scroll event and again by `follow` itself (a jump to a match
// moves `scrollTop` synchronously, before its scroll event arrives):
//
// - within NEAR_BOTTOM of the end → at the bottom;
// - anywhere else, once the reader has moved → not at the bottom, moving up
//   or down alike — except while a smooth scroll that `follow` itself
//   started is still travelling down (`smoothTarget`): that is the box
//   catching up, not the reader leaving. Moving up, or arriving, ends it;
// - `scrollTop` unchanged → the flag unchanged: growth makes the box taller
//   under a reader at the bottom without moving them.
//
// **Holding (R3 plan T3.3, A4)** is that same rule: while the search bar is
// open, a streaming reply cannot pull the reader off a match, because a
// match is not the bottom. What `hold` adds is two edges. Mounted while
// holding (room ⇄ chat under an open bar), the first call does not place the
// reader — the search bar puts them back on the current match, or at the
// bottom when there is none (R1-1); this beats the memory below. And the bar
// closing (hold true → false) is the explicit "back to live" gesture
// (alpha.463: 關掉搜尋列就恢復自動捲到底): the next growth follows the bottom
// wherever the reader is, unless they scroll up first.
//
// **Releasing (A F4, R4 T3.2).** A jump to a match in the last screen leaves
// `scrollTop` clamped within NEAR_BOTTOM of the end, which reads as "at the
// bottom", and the next line would push the match off screen. The search bar
// — and the dock's inspect jump, with the bar closed — calls `release()`
// after every jump: the reader counts as away from the bottom, and the
// position the jump left is remembered so its own (late) scroll event does
// not count. Only the reader moving off it, back to the bottom, resumes
// following.
//
// **Memory (spec §6).** Given `memory`, every observed position is written to
// the pane's memo (`lib/nex/transcript-scroll-memory`): scrollTop, the flag,
// the view and the first turn still on screen. The first call after a mount
// reads it back: at the bottom → the jump to the bottom as ever; elsewhere,
// the same view restores scrollTop (the browser clamps it), and the other
// view — a different height — brings the remembered first turn to the top.
import { useCallback, useImperativeHandle, useLayoutEffect, useMemo, useRef, type Ref, type UIEvent } from 'react'
import { readScrollMemo, writeScrollMemo, type ScrollMemo } from '../lib/nex/transcript-scroll-memory'

/** Within this many pixels of the end counts as the bottom (sub-pixel rounding, a last line's margin). */
const NEAR_BOTTOM = 24

export interface TranscriptScroll {
  /** The container's callback ref: set on the scrolling div (forwards to the caller's `scrollRef`). */
  attach: (node: HTMLDivElement | null) => void
  /** The container's onScroll. */
  onScroll: (e: UIEvent<HTMLDivElement>) => void
  /** Place the reader (first call), then scroll to the bottom only if they are there. */
  follow: () => void
  /** Stop following where the box is now, until the reader is back at the bottom (A F4). */
  release: () => void
}

/** What a transcript hands its `scrollControl` ref (the search bar's handle). */
export interface TranscriptScrollControl {
  release: () => void
}

/** Which pane's memo a transcript keeps, and which view it is. */
export interface TranscriptScrollMemory {
  paneId: string
  view: ScrollMemo['view']
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

/** The index of the first `[data-turn-index]` whose bottom is below the box's top. */
function firstVisibleTurn(el: HTMLElement): number | null {
  const top = el.getBoundingClientRect().top
  for (const turn of el.querySelectorAll<HTMLElement>('[data-turn-index]')) {
    if (turn.getBoundingClientRect().bottom > top) {
      const n = Number(turn.dataset.turnIndex)
      return Number.isFinite(n) ? n : null
    }
  }
  return null
}

export function useTranscriptScroll(
  external: Ref<HTMLDivElement> | undefined,
  hold: boolean,
  memory?: TranscriptScrollMemory,
): TranscriptScroll {
  const box = useRef<HTMLDivElement | null>(null)
  const atBottom = useRef(true)
  const lastTop = useRef(0)
  const scrolled = useRef(false)
  // The scrollTop a `release()` left, until the reader moves off it.
  const released = useRef<number | null>(null)
  // Set while a smooth scroll `follow` started is on its way down.
  const smoothTarget = useRef<number | null>(null)
  // The bar just closed: the next growth follows wherever the reader is.
  const resume = useRef(false)
  const holding = useRef(hold)
  const mem = useRef(memory)
  // Before any passive effect of the same commit reads them.
  useLayoutEffect(() => {
    mem.current = memory
  })
  useLayoutEffect(() => {
    if (holding.current && !hold) {
      released.current = null
      resume.current = true
    }
    holding.current = hold
  }, [hold])

  const attach = useCallback((node: HTMLDivElement | null) => {
    box.current = node
    assignRef(external, node)
  }, [external])

  const remember = useCallback((el: HTMLDivElement) => {
    const m = mem.current
    if (!m) return
    writeScrollMemo(m.paneId, {
      scrollTop: el.scrollTop, atBottom: atBottom.current, view: m.view, firstTurn: firstVisibleTurn(el),
    })
  }, [])

  const observe = useCallback(() => {
    const el = box.current
    if (!el) return
    const top = el.scrollTop
    if (released.current !== null && top === released.current) {
      // Still where the jump left it (its own scroll event, or growth).
      remember(el)
      return
    }
    released.current = null
    if (el.scrollHeight - top - el.clientHeight <= NEAR_BOTTOM) {
      atBottom.current = true
      smoothTarget.current = null
    } else if (top !== lastTop.current) {
      if (top < lastTop.current) {
        smoothTarget.current = null
        resume.current = false
      }
      if (smoothTarget.current === null) atBottom.current = false
    }
    lastTop.current = top
    remember(el)
  }, [remember])

  /** The first call's restore from memory; false when there is none to do (no memo, or at the bottom). */
  const restore = useCallback((el: HTMLDivElement): boolean => {
    const m = mem.current
    const memo = m && readScrollMemo(m.paneId)
    if (!m || !memo || memo.atBottom) return false
    let top = memo.scrollTop
    if (memo.view !== m.view && memo.firstTurn !== null) {
      const turn = el.querySelector<HTMLElement>(`[data-turn-index="${memo.firstTurn}"]`)
      // The turn's offset inside the box, like scrollIntoView({ block: 'start' })
      // but without scrolling any ancestor.
      if (turn) top = turn.getBoundingClientRect().top - el.getBoundingClientRect().top + el.scrollTop
    }
    el.scrollTo({ top, behavior: 'auto' })
    atBottom.current = false
    lastTop.current = top
    return true
  }, [])

  const follow = useCallback(() => {
    const el = box.current
    if (!el?.scrollTo) return
    const first = !scrolled.current
    if (first) {
      scrolled.current = true
      // Mounted under an open search bar (a view switch): the bar places
      // the reader — at the current match, or the bottom — not this (R1-1).
      if (holding.current) return
      if (restore(el)) return
    } else {
      observe()
      if (!atBottom.current && !resume.current) return
    }
    el.scrollTo({ top: el.scrollHeight, behavior: first ? 'auto' : 'smooth' })
    smoothTarget.current = first ? null : el.scrollHeight
    atBottom.current = true
    released.current = null
    resume.current = false
  }, [observe, restore])

  const release = useCallback(() => {
    const el = box.current
    if (!el) return
    atBottom.current = false
    smoothTarget.current = null
    resume.current = false
    released.current = el.scrollTop
    lastTop.current = el.scrollTop
    remember(el)
  }, [remember])

  return useMemo(() => ({ attach, onScroll: observe, follow, release }), [attach, observe, follow, release])
}
