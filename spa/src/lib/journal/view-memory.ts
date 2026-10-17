// spa/src/lib/journal/view-memory.ts — what the journal view had on screen, per unit (J-2a T-2a.5). A tab-hosted view
// unmounts when its tab is switched away (`keepAliveCount: 0`), and the card stack itself can be hidden and shown, so none
// of this may live in component state (CLAUDE.md, Tab-hosted checklist). Pattern: lib/workbook/view-memory.ts.
// Memory only: a reload starts every unit closed, at the bottom. Bounded: the MAX_VIEW_MEMOS most recently used units are
// kept, and a host the journal store forgets takes its memos with it (`useJournalStore.forgetHost`).

export type JournalFilter = 'all' | 'exec' | 'task' | 'comm' | 'confirm'

export interface ViewMemo {
  journalOpen: boolean
  roadmapOpen: boolean
  tasksOpen: boolean
  /** 固定: a fixed card is not closed by a click outside it (U4); per card, default off. */
  fixed: { journal: boolean; roadmap: boolean; tasks: boolean }
  /** Card widths in px; null = not set yet (the UI initialises it from the last used width). */
  widths: { journal: number | null; roadmap: number | null; tasks: number | null }
  filter: JournalFilter
  /** scrollTop of the stream. */
  scroll: number
  /** The stream was at its bottom when the reader left: it opens there again and keeps following new entries. */
  pinnedToBottom: boolean
  /** The 待確認 entry whose detail is open (an entry id), if any. */
  detailId: string | null
  /** Session pages (journal.sessions.v1): the leader session shown; null = follow the current one. */
  page: string | null
  /** Where the stream was on each session page (by leader session id): one value per PAGE, not per unit. */
  pageScroll: Record<string, ScrollMemo>
}

/** Where a stream was left: `scroll` is scrollTop, `pinnedToBottom` that it was at its end (it opens there and follows new entries). */
export interface ScrollMemo { scroll: number; pinnedToBottom: boolean }

export const MAX_VIEW_MEMOS = 200
/** Pages with a remembered scroll position per unit: the least recently saved past this are forgotten (they open at the bottom). */
export const MAX_PAGE_SCROLLS = 50

/** A change counter + listeners, so a component can follow a unit's memo (`useViewMemo`) while the memo itself stays outside React. */
let rev = 0
const listeners = new Set<() => void>()
const bump = (): void => { rev++; for (const l of [...listeners]) l() }
export function subscribeViewMemos(l: () => void): () => void { listeners.add(l); return () => { listeners.delete(l) } }
export const viewMemoRev = (): number => rev

/** Insertion order is recency order (a use re-inserts), so the first key is the least recently used. */
const memos = new Map<string, ViewMemo>()
const SEP = '\u0000'
const keyOf = (hostId: string, unitKey: string): string => `${hostId}${SEP}${unitKey}`

export const initialViewMemo = (): ViewMemo => ({
  journalOpen: false, roadmapOpen: false, tasksOpen: false, fixed: { journal: false, roadmap: false, tasks: false }, widths: { journal: null, roadmap: null, tasks: null },
  filter: 'all', scroll: 0, pinnedToBottom: true, detailId: null, page: null, pageScroll: {},
})

export function readViewMemo(hostId: string, unitKey: string): ViewMemo {
  const k = keyOf(hostId, unitKey)
  const m = memos.get(k)
  if (!m) return initialViewMemo()
  memos.delete(k)
  memos.set(k, m) // a read is a use
  return m
}

export function patchViewMemo(hostId: string, unitKey: string, patch: Partial<ViewMemo>): void {
  const k = keyOf(hostId, unitKey)
  const next = { ...(memos.get(k) ?? initialViewMemo()), ...patch }
  memos.delete(k)
  memos.set(k, next)
  while (memos.size > MAX_VIEW_MEMOS) memos.delete(memos.keys().next().value as string)
  bump()
}

/** The stream's place on session page `page` of a unit ('' = no session pages: the unit's own `scroll` / `pinnedToBottom`). A page never seen opens at the bottom. */
export function readScrollMemo(hostId: string, unitKey: string, page: string): ScrollMemo {
  const m = readViewMemo(hostId, unitKey)
  if (page === '') return { scroll: m.scroll, pinnedToBottom: m.pinnedToBottom }
  return m.pageScroll[page] ?? { scroll: 0, pinnedToBottom: true }
}

export function patchScrollMemo(hostId: string, unitKey: string, page: string, v: ScrollMemo): void {
  if (page === '') { patchViewMemo(hostId, unitKey, v); return }
  const { [page]: _old, ...rest } = readViewMemo(hostId, unitKey).pageScroll
  const pageScroll = { ...rest, [page]: v }
  for (const old of Object.keys(pageScroll).slice(0, Math.max(0, Object.keys(pageScroll).length - MAX_PAGE_SCROLLS))) delete pageScroll[old]
  patchViewMemo(hostId, unitKey, { pageScroll })
}

/** Move a memo to another key in one step (the unreachable stand-in -> the resolved unit). Nothing to move, or the target already
 *  holds state: no-op (the source is then left alone). Returns whether it moved. */
export function migrateViewMemo(fromHost: string, fromKey: string, toHost: string, toKey: string): boolean {
  const from = keyOf(fromHost, fromKey)
  const to = keyOf(toHost, toKey)
  const m = memos.get(from)
  if (from === to || !m || memos.has(to)) return false
  memos.delete(from)
  memos.set(to, m)
  bump()
  return true
}

export function forgetViewMemo(hostId: string, unitKey: string): void {
  memos.delete(keyOf(hostId, unitKey))
  bump()
}

/** The host was forgotten (removed / re-pointed): its units are not coming back under these keys. */
export function forgetViewMemosOfHost(hostId: string): void {
  for (const k of [...memos.keys()]) if (k.startsWith(`${hostId}${SEP}`)) memos.delete(k)
  bump()
}

export function viewMemoCount(): number {
  return memos.size
}

export function clearViewMemos(): void {
  memos.clear()
  bump()
}
