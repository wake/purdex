// spa/src/lib/workbook/view-model.ts — pure shaping of a conversation's entries for the workbook view (WA-2b-2; spec §10):
// the 「紀錄」 groups by `thing`, and the counts a refresh entry reads out. No store, no React.
import type { WorkbookEntry } from './types'

export interface EntryGroup {
  /** Stable across renders: `t:<thing>` for a named thing, `e:<id>` for an entry with no thing yet (pending / failed). */
  key: string
  /** '' for a loose entry (nothing to name the group by). */
  thing: string
  /** The thing ended: its newest settled entry says `thing_done`. */
  done: boolean
  /** Newest first. */
  entries: WorkbookEntry[]
}

/** `entries` is newest first (the store's order). Groups come out by their newest entry, so the most recent thing is first;
 *  `skipped` entries are never shown. An entry that names no thing is its own group (it cannot be filed under a name). */
export function groupEntries(entries: readonly WorkbookEntry[]): EntryGroup[] {
  const groups: EntryGroup[] = []
  const byKey = new Map<string, EntryGroup>()
  for (const e of entries) {
    if (e.state === 'skipped') continue
    const key = e.thing === '' ? `e:${e.id}` : `t:${e.thing}`
    let g = byKey.get(key)
    if (!g) {
      g = { key, thing: e.thing, done: false, entries: [] }
      byKey.set(key, g)
      groups.push(g)
    }
    g.entries.push(e)
  }
  for (const g of groups) g.done = g.entries.find((e) => e.state === 'ok')?.thingDone ?? false
  return groups
}

/** What a refresh entry did to the list, by count (the line reads 「重整：完成 a、移除 b、新增 c」). */
export function refreshCounts(e: Pick<WorkbookEntry, 'todoChanges'>): { done: number; dropped: number; added: number } {
  return { done: e.todoChanges.done.length, dropped: e.todoChanges.dropped.length, added: e.todoChanges.added.length }
}

/** The time an entry is about: its turn's, else (a refresh has no turn) when it was written. */
export const entryTime = (e: Pick<WorkbookEntry, 'turnAt' | 'createdAt'>): number => e.turnAt > 0 ? e.turnAt : e.createdAt
