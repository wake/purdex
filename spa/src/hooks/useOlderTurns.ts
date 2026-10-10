// spa/src/hooks/useOlderTurns.ts — reading a conversation's earlier turns as the reader goes up, shared by the deck and the chat.
//
// Three jobs, one rule each:
// - ASK: a page is asked for when the reader is within TOP_REACH of the top (scroll event), AND again whenever a page has just
//   landed or failed and the reader is still there (`firstTurn` is the dependency: a reader who flung the box to scrollTop 0
//   while the request was out gets no further scroll event), AND while the content is too short to scroll at all. Not before the
//   first placement (`placed`): a box still waiting for its remembered anchor sits at scrollTop 0 without being at the top.
// - BACK OFF: a page that fails (paging ends and the first turn did not move) is not asked for again at once, from the scroll
//   path or the automatic one: 1 s, 2 s, 4 s … up to 8 s. The automatic path gives up after AUTO_PAGE_TRIES until the reader's
//   own scroll asks again.
// - KEEP THE PLACE: whenever older turns land above the reader — whoever asked for them (another pane of the same session, a
//   request that outlived an unmount) — the first turn on screen and its offset from the box's top are read in the render that
//   brings them (the DOM is still the old one there; `useMemo` keyed by the first turn, from the box element held in state) and
//   put back right after the DOM change (`shiftBy`). At scrollTop 0 Chrome does not anchor by itself and the box opts out of
//   anchoring anyway. A new `generation` (a jump, a replacing snapshot) is another window, not older turns: nothing to keep.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef } from 'react'

/** Within this many pixels of the top the next older page is read. */
export const TOP_REACH = 120
const AUTO_PAGE_TRIES = 3
const RETRY_MS = 1000
const RETRY_MAX_MS = 8000

export interface OlderTurnsOptions {
  /** The scrolling box (held in state by the caller, so that it can be read while rendering); null before it attaches. */
  box: HTMLElement | null
  hasMore: boolean
  paging: boolean
  /** Index of the first turn held now (null: none). */
  firstTurn: number | null
  /** Changes when the turns held are another window (jump, replacing snapshot), not older turns added. */
  generation: number
  /** The first placement of the reader has happened (`useTranscriptScroll().placed`). */
  placed: boolean
  load: () => void
  shiftBy: (delta: number) => void
}

interface Attempt { first: number | null; tries: number; fails: number; notBefore: number }
interface Snap { turn: string; offset: number }

function firstVisible(box: HTMLElement): Snap | null {
  const top = box.getBoundingClientRect().top
  for (const el of box.querySelectorAll<HTMLElement>('[data-turn-index]')) {
    const r = el.getBoundingClientRect()
    if (r.bottom > top) return { turn: el.dataset.turnIndex ?? '', offset: r.top - top }
  }
  return null
}

export function useOlderTurns({ box, hasMore, paging, firstTurn, generation, placed, load, shiftBy }: OlderTurnsOptions): { onScroll: () => void } {
  const loadRef = useRef(load)
  const shiftRef = useRef(shiftBy)
  useLayoutEffect(() => { loadRef.current = load; shiftRef.current = shiftBy })
  const attempt = useRef<Attempt>({ first: null, tries: 0, fails: 0, notBefore: 0 })
  const sawPaging = useRef(false)
  const askedAt = useRef<number | null>(null)
  const committed = useRef<{ first: number | null; generation: number }>({ first: firstTurn, generation })

  // While rendering, the DOM is still the one the reader sees: read their place when the first turn moves.
  const snap = useMemo(() => (box && firstTurn !== null ? firstVisible(box) : null), [box, firstTurn])

  useLayoutEffect(() => {
    const prev = committed.current
    if (box && snap && firstTurn !== null && prev.first !== null && firstTurn < prev.first && generation === prev.generation) {
      const anchor = box.querySelector<HTMLElement>(`[data-turn-index="${snap.turn}"]`)
      if (anchor) {
        const delta = anchor.getBoundingClientRect().top - box.getBoundingClientRect().top - snap.offset
        if (delta !== 0) shiftRef.current(delta)
      }
    }
    committed.current = { first: firstTurn, generation }
  })

  const state = useCallback((): Attempt => {
    if (attempt.current.first !== firstTurn) attempt.current = { first: firstTurn, tries: 0, fails: 0, notBefore: 0 }
    return attempt.current
  }, [firstTurn])

  const request = useCallback(() => {
    sawPaging.current = false
    askedAt.current = firstTurn
    loadRef.current()
  }, [firstTurn])

  // A page that ended without moving the first turn failed (or came empty, or was aborted): back off.
  useEffect(() => {
    if (paging) { sawPaging.current = true; return }
    if (!sawPaging.current) return
    sawPaging.current = false
    if (askedAt.current !== firstTurn) return // it landed
    const a = state()
    a.fails += 1
    a.notBefore = Date.now() + Math.min(RETRY_MAX_MS, RETRY_MS * 2 ** (a.fails - 1))
  }, [paging, firstTurn, state])

  // Automatic: the content too short to scroll, or the reader at the top when the last page landed / failed.
  useEffect(() => {
    if (!box || !hasMore || paging || !placed) return
    if (box.scrollHeight > box.clientHeight + TOP_REACH && box.scrollTop >= TOP_REACH) return
    const a = state()
    if (a.tries >= AUTO_PAGE_TRIES) return
    const run = () => {
      // The live end may have grown the content, or the reader moved away, while the timer waited.
      if (box.scrollHeight > box.clientHeight + TOP_REACH && box.scrollTop >= TOP_REACH) return
      a.tries += 1
      request()
    }
    // After a failure, the same back-off as the scroll path (the failure effect above set `notBefore`).
    const wait = a.fails > 0 ? a.notBefore - Date.now() : 0
    if (wait <= 0) { run(); return }
    const timer = setTimeout(run, wait)
    return () => clearTimeout(timer)
    // Keyed by the FIRST turn, not the turns: a live update elsewhere must not restart a pending retry.
  }, [firstTurn, hasMore, paging, placed, box, state, request])

  const onScroll = useCallback(() => {
    if (!box || !hasMore || paging || box.scrollTop >= TOP_REACH) return
    if (Date.now() < state().notBefore) return
    request()
  }, [box, hasMore, paging, state, request])

  return { onScroll }
}
