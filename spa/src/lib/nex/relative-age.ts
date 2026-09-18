// spa/src/lib/nex/relative-age.ts — bucket a past `updated_at` for the
// sidebar Executions view (spec §4.3). Returns an i18n key suffix and the
// count; the caller renders `executions.age.<key>` with `{ n }`.

export type RelativeAgeKey = 'just_now' | 'minutes' | 'hours' | 'days'

export interface RelativeAge {
  key: RelativeAgeKey
  n: number
}

const MINUTE_MS = 60_000
const HOUR_MS = 60 * MINUTE_MS
const DAY_MS = 24 * HOUR_MS

export function relativeAge(updatedAt: number, now: number): RelativeAge {
  const diff = Math.max(0, now - updatedAt)
  if (diff < MINUTE_MS) return { key: 'just_now', n: Math.floor(diff / 1_000) }
  if (diff < HOUR_MS) return { key: 'minutes', n: Math.floor(diff / MINUTE_MS) }
  if (diff < DAY_MS) return { key: 'hours', n: Math.floor(diff / HOUR_MS) }
  return { key: 'days', n: Math.floor(diff / DAY_MS) }
}
