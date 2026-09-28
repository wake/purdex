// spa/src/lib/nex/transcript-scroll-memory.ts — where each worker pane's
// transcript was scrolled (worker pane theme spec §6). A remount (a room ⇄
// chat switch, the pane re-rendering from scratch) reads it back. Memory
// only: a reload starts every transcript at the bottom again.

export interface ScrollMemo {
  scrollTop: number
  atBottom: boolean
  /** The view that wrote it: only that view can reuse `scrollTop`. */
  view: 'room' | 'chat'
  /** The first turn (`data-turn-index`) still on screen — both views share the index. */
  firstTurn: number | null
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
