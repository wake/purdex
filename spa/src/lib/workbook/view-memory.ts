// spa/src/lib/workbook/view-memory.ts — what the workbook view had on screen, per conversation (WA-2b-2). A tab-hosted view
// unmounts when its tab is switched away (`keepAliveCount: 0`), so its own `useState` would reset: the 紀錄 / 待辦 switch,
// the scroll of each, and which finished groups were opened live here instead (pattern: lib/nex/transcript-scroll-memory.ts).
// Memory only: a reload starts every workbook at 紀錄, top.

export type WorkbookTab = 'log' | 'todos'

export interface ViewMemo {
  tab: WorkbookTab
  scroll: Record<WorkbookTab, number>
  /** Keys (`EntryGroup.key`) of the finished groups the reader opened. */
  openGroups: string[]
}

const memos = new Map<string, ViewMemo>()
const keyOf = (hostId: string, convKey: string): string => `${hostId}\u0000${convKey}`

export const initialViewMemo = (): ViewMemo => ({ tab: 'log', scroll: { log: 0, todos: 0 }, openGroups: [] })

export function readViewMemo(hostId: string, convKey: string): ViewMemo {
  return memos.get(keyOf(hostId, convKey)) ?? initialViewMemo()
}

export function patchViewMemo(hostId: string, convKey: string, patch: Partial<ViewMemo>): void {
  memos.set(keyOf(hostId, convKey), { ...readViewMemo(hostId, convKey), ...patch })
}

export function forgetViewMemo(hostId: string, convKey: string): void {
  memos.delete(keyOf(hostId, convKey))
}

export function clearViewMemos(): void {
  memos.clear()
}
