// spa/src/lib/nex/transcript-scroll-memory.ts — where each worker pane's
// transcript was scrolled (worker pane theme spec §6). A remount (a room ⇄
// chat switch, the pane re-rendering from scratch) reads it back. Memory
// only: a reload starts every transcript at the bottom again.

/**
 * What was on screen (worker prelude spec §5.4, #1534): the first prelude
 * element (`data-prelude-pos`) or turn (`data-turn-index`) still showing,
 * and its top's distance from the box's top (negative once partly scrolled
 * past). Unlike `scrollTop`, it survives content landing above it.
 */
export type ScrollAnchor =
  | { kind: 'prelude'; pos: string; offset: number }
  | { kind: 'turn'; index: number; offset: number }

export interface ScrollMemo {
  scrollTop: number
  atBottom: boolean
  /** The view that wrote it: only that view can reuse `scrollTop` and the anchor's offset. */
  view: 'room' | 'chat'
  /** The first turn (`data-turn-index`) still on screen — both views share the index. */
  firstTurn: number | null
  /** Absent when nothing anchorable was on screen. */
  anchor?: ScrollAnchor
}

const memos = new Map<string, ScrollMemo>()

export function readScrollMemo(paneId: string): ScrollMemo | undefined {
  return memos.get(paneId)
}

export function writeScrollMemo(paneId: string, memo: ScrollMemo): void {
  memos.set(paneId, memo)
}

export function forgetScrollMemo(paneId: string): void {
  memos.delete(paneId)
}
