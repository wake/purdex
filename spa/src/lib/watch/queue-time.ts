// spa/src/lib/watch/queue-time.ts — the host tab's queue times, counted locally from a command's `since` (ms; null when the daemon
// sent none or a zero/pre-2000 stamp), and the keys that pair a card with its table row on hover.
import type { FleetHolder, FleetWaiter } from '../fleet/types'

export type QueueClass = 'heavy' | 'light'
export const holderKey = (h: FleetHolder) => `h${h.class}${h.slot}-${h.pid}`
// Not the place: a waiter moves up as others leave, and the highlight must stay on the same command across frames.
export const waiterKey = (w: FleetWaiter) => `w${w.class}|${w.command}|${w.project}|${w.session}|${w.since ?? ''}`

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
