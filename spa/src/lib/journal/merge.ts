// spa/src/lib/journal/merge.ts — ordering and merging of journal entries (J-2a T-2a.4). The daemon sorts by (at, id) too, but a
// file's block order can differ from turn order (daemon plan §8), so the App never trusts the order it was given.
import type { JournalEntry } from './types'

/** Oldest first: by `at`, equal times by `id` (plain string order, the same total order everywhere). */
export const compareEntries = (a: JournalEntry, b: JournalEntry): number => (a.at - b.at) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)

/** Union by id (an incoming row replaces a held one), oldest first. */
export function mergeEntries(held: readonly JournalEntry[], incoming: readonly JournalEntry[]): JournalEntry[] {
  const byId = new Map<string, JournalEntry>()
  for (const e of held) byId.set(e.id, e)
  for (const e of incoming) byId.set(e.id, e)
  return [...byId.values()].sort(compareEntries)
}

/** The pinned band: newest first by `at`, equal times by `id` descending (the exact reverse of the stream order). */
export function sortPinned(pinned: readonly JournalEntry[]): JournalEntry[] {
  const byId = new Map<string, JournalEntry>()
  for (const e of pinned) byId.set(e.id, e)
  return [...byId.values()].sort((a, b) => compareEntries(b, a))
}

/** The `before=` cursor for the page older than `oldest`: `<YYYY-MM>:<id>`. ASSUMED (daemon plan §8 names the shape, not the
 *  month's time zone): the month is the viewer's local month of the entry's time. */
export function cursorOf(oldest: JournalEntry): string {
  const d = new Date(oldest.at)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}:${oldest.id}`
}
