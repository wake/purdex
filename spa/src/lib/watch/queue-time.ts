// spa/src/lib/watch/queue-time.ts — the host tab's queue times, counted locally from a command's `since` (ms; null when the daemon
// sent none or a zero/pre-2000 stamp).

/** A wait turns warning past 2 minutes. */
export const WAIT_LONG_S = 120

/** Seconds since `since`, never negative; null when `since` is unknown. */
export const secondsSince = (since: number | null, now: number): number | null =>
  since === null ? null : Math.max(0, (now - since) / 1000)

/** 8:22 — minutes can pass 59 (1:05:00 reads 65:00); an unknown time reads 「—」, never NaN. */
export function mss(since: number | null, now: number): string {
  const s = secondsSince(since, now)
  if (s === null) return '—'
  return `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, '0')}`
}
