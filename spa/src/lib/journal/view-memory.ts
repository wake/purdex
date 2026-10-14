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
  /** Card widths in px; null = not set yet (the UI initialises it from the last used width). */
  widths: { journal: number | null; roadmap: number | null; tasks: number | null }
  filter: JournalFilter
  /** scrollTop of the stream. */
  scroll: number
  /** The stream was at its bottom when the reader left: it opens there again and keeps following new entries. */
  pinnedToBottom: boolean
  /** The 待確認 entry whose detail is open (an entry id), if any. */
  detailId: string | null
}

export const MAX_VIEW_MEMOS = 200

/** Insertion order is recency order (a use re-inserts), so the first key is the least recently used. */
const memos = new Map<string, ViewMemo>()
const SEP = '\u0000'
const keyOf = (hostId: string, unitKey: string): string => `${hostId}${SEP}${unitKey}`

export const initialViewMemo = (): ViewMemo => ({
  journalOpen: false, roadmapOpen: false, tasksOpen: false, widths: { journal: null, roadmap: null, tasks: null },
  filter: 'all', scroll: 0, pinnedToBottom: true, detailId: null,
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
}

export function forgetViewMemo(hostId: string, unitKey: string): void {
  memos.delete(keyOf(hostId, unitKey))
}

/** The host was forgotten (removed / re-pointed): its units are not coming back under these keys. */
export function forgetViewMemosOfHost(hostId: string): void {
  for (const k of [...memos.keys()]) if (k.startsWith(`${hostId}${SEP}`)) memos.delete(k)
}

export function viewMemoCount(): number {
  return memos.size
}

export function clearViewMemos(): void {
  memos.clear()
}
