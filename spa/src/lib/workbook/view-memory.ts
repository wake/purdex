// spa/src/lib/workbook/view-memory.ts — what the workbook view had on screen, per conversation (WA-2b-2). A tab-hosted view
// unmounts when its tab is switched away (`keepAliveCount: 0`), so its own `useState` would reset: the 紀錄 / 待辦 switch,
// the scroll of each, and which finished groups were opened live here instead (pattern: lib/nex/transcript-scroll-memory.ts).
// Memory only: a reload starts every workbook at 紀錄, top. Bounded: the MAX_VIEW_MEMOS most recently used conversations are
// kept, and a host the store forgets takes its memos with it (`useWorkbookStore.forgetHost`).

export type WorkbookTab = 'log' | 'todos'

export interface ViewMemo {
  tab: WorkbookTab
  scroll: Record<WorkbookTab, number>
  /** Keys (`EntryGroup.key`) of the finished groups the reader opened. */
  openGroups: string[]
}

export const MAX_VIEW_MEMOS = 200

/** Insertion order is recency order (a use re-inserts), so the first key is the least recently used. */
const memos = new Map<string, ViewMemo>()
const SEP = '\u0000'
const keyOf = (hostId: string, convKey: string): string => `${hostId}${SEP}${convKey}`

export const initialViewMemo = (): ViewMemo => ({ tab: 'log', scroll: { log: 0, todos: 0 }, openGroups: [] })

export function readViewMemo(hostId: string, convKey: string): ViewMemo {
  const k = keyOf(hostId, convKey)
  const m = memos.get(k)
  if (!m) return initialViewMemo()
  memos.delete(k)
  memos.set(k, m) // a read is a use
  return m
}

export function patchViewMemo(hostId: string, convKey: string, patch: Partial<ViewMemo>): void {
  const k = keyOf(hostId, convKey)
  const next = { ...(memos.get(k) ?? initialViewMemo()), ...patch }
  memos.delete(k)
  memos.set(k, next)
  while (memos.size > MAX_VIEW_MEMOS) memos.delete(memos.keys().next().value as string)
}

export function forgetViewMemo(hostId: string, convKey: string): void {
  memos.delete(keyOf(hostId, convKey))
}

/** The host was forgotten (removed / re-pointed): its conversations are not coming back under these keys. */
export function forgetViewMemosOfHost(hostId: string): void {
  for (const k of [...memos.keys()]) if (k.startsWith(`${hostId}${SEP}`)) memos.delete(k)
}

export function viewMemoCount(): number {
  return memos.size
}

export function clearViewMemos(): void {
  memos.clear()
}
