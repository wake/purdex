// spa/src/lib/journal/filters.ts — the journal card's five stream filters (spec Q-C). Pure. The chosen filter is `ViewMemo.filter`
// (view-memory.ts), never component state.
import type { EntryKind, JournalEntry } from './types'
import type { JournalFilter } from './view-memory'

export const FILTER_IDS: readonly JournalFilter[] = ['all', 'exec', 'task', 'comm', 'confirm']

const KINDS: Record<Exclude<JournalFilter, 'all'>, readonly EntryKind[]> = {
  exec: ['exec', 'exec_done'],
  task: ['task_new', 'task_done'],
  comm: ['request', 'comm'],
  confirm: ['confirm'],
}

export const matchesFilter = (e: JournalEntry, f: JournalFilter): boolean => f === 'all' || KINDS[f].includes(e.kind)

/** What a click on `clicked` makes of the filter: the active one clicked again clears it (back to all). */
export const nextFilter = (current: JournalFilter, clicked: JournalFilter): JournalFilter => (clicked === current ? 'all' : clicked)

/** A 待確認's progress: ticked items of all items. */
export const answered = (e: JournalEntry): { done: number; total: number } => ({ done: e.items.filter((i) => i.done).length, total: e.items.length })
