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
//   catching up, not the reader leaving. Moving up, arriving, or the
//   reader's own scrolling input (wheel, touch, a press on the scrollbar, a
//   scrolling key — A3) ends it: after that the position alone decides, so
//   a reader who drags down and stops short of the end is not pulled on;
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
// **Memory (spec §6).** Given `memory`, every observed position — and
// `follow`'s own jump, mount's or a later catch-up alike, which never gets a
// scroll event of its own to observe — is written to the pane's memo
// (`lib/nex/transcript-scroll-memory`): scrollTop, the flag, the view and the
// first turn still on screen. The first call after a mount reads it back: at
// the bottom → the jump to the bottom as ever; elsewhere, the same view
// restores scrollTop (the browser clamps it; a clamp that lands at the
// bottom counts as the bottom, A4), and the other view — a
// different height — brings the remembered first turn to the top.
// **Keying.** The caller's `memory.paneId` is whatever key it composes — a
// worker pane's transcript keys it by pane *and* execution
// (ExecutionView), since a handoff / take-back can swap a pane's content to
// a different execution while keeping its paneId.
import { useCallback, useImperativeHandle, useLayoutEffect, useMemo, useRef, type Ref, type UIEvent } from 'react'
import { readScrollMemo, writeScrollMemo, type ScrollMemo } from '../lib/nex/transcript-scroll-memory'

/** Within this many pixels of the end counts as the bottom (sub-pixel rounding, a last line's margin). */
const NEAR_BOTTOM = 24

/** Keys that scroll the box (A3). */
const SCROLL_KEYS = new Set(['PageDown', 'PageUp', 'ArrowDown', 'ArrowUp', ' ', 'Spacebar', 'End', 'Home'])
/** Input that is always the reader scrolling (A3). */
const SCROLL_INPUTS = ['wheel', 'touchstart', 'touchmove'] as const

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

  // A3: the reader's own scrolling input ends follow()'s smooth scroll.
  // Native listeners on the box, so neither transcript has to spread more
  // handlers. A press counts only on the box itself (its scrollbar), not a
  // click on the content; a key only when it scrolls.
  const takeOver = useCallback((e: Event) => {
    if (smoothTarget.current === null) return
    if (e.type === 'pointerdown' && e.target !== e.currentTarget) return
    if (e.type === 'keydown' && !SCROLL_KEYS.has((e as KeyboardEvent).key)) return
    smoothTarget.current = null
  }, [])

  const attach = useCallback((node: HTMLDivElement | null) => {
    const prev = box.current
    if (prev && prev !== node) {
      for (const type of SCROLL_INPUTS) prev.removeEventListener(type, takeOver)
      prev.removeEventListener('pointerdown', takeOver)
      prev.removeEventListener('keydown', takeOver)
    }
    if (node && node !== prev) {
      for (const type of SCROLL_INPUTS) node.addEventListener(type, takeOver, { passive: true })
      node.addEventListener('pointerdown', takeOver)
      node.addEventListener('keydown', takeOver)
    }
    box.current = node
    assignRef(external, node)
  }, [external, takeOver])

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
    // Read back where it landed: an instant scroll lands synchronously, and
    // the browser clamps a memo past the max (less content now) — possibly
    // to the bottom, and possibly with no scroll event at all (it all fits).
    // A clamped restore is judged from where it landed (A4); an unclamped
    // one keeps the memo's "not at the bottom" (a released jump into the
    // last screen stays released).
    const landed = el.scrollTop
    atBottom.current = landed !== top && el.scrollHeight - landed - el.clientHeight <= NEAR_BOTTOM
    lastTop.current = landed
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
    // The jump itself must be remembered too (fix round 1, finding 3):
    // jsdom (and, for a smooth scroll, the real browser mid-flight) never
    // fires a scroll event for a programmatic scrollTo, so without this an
    // immediate unmount right after a follow()-driven jump would leave the
    // memo at whatever stale, off-bottom position the reader had before.
    remember(el)
  }, [observe, restore, remember])

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
