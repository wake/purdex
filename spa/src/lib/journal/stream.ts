// spa/src/lib/journal/stream.ts — the stream's time labels and day dividers (spec §11). Days are the viewer's LOCAL calendar days
// (`YYYY-MM-DD`), times `HH:mm` local. Pure.
import type { JournalEntry } from './types'

const p2 = (n: number): string => String(n).padStart(2, '0')

export const dayKey = (at: number): string => {
  const d = new Date(at)
  return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())}`
}

export const timeOf = (at: number): string => {
  const d = new Date(at)
  return `${p2(d.getHours())}:${p2(d.getMinutes())}`
}

export type StreamItem = { type: 'day'; day: string } | { type: 'entry'; entry: JournalEntry }

/** `entries` (oldest first) with one divider before the first entry of each local day. */
export function withDays(entries: readonly JournalEntry[]): StreamItem[] {
  const out: StreamItem[] = []
  let day = ''
  for (const entry of entries) {
    const d = dayKey(entry.at)
    if (d !== day) { day = d; out.push({ type: 'day', day }) }
    out.push({ type: 'entry', entry })
  }
  return out
}
