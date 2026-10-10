// spa/src/hooks/useCoarseNow.ts — "now", to the minute, shared by every component that asks (#2410).
//
// For judgments that are about minutes (is a reading older than half an hour), where a timer per component would be waste:
// one interval serves all subscribers and runs only while there is one. The value changes once a minute, so a subscriber
// re-renders once a minute, and not otherwise. Written as an external store so that reading the clock is not a call made
// while rendering.
import { useSyncExternalStore } from 'react'

export const COARSE_NOW_STEP_MS = 60_000

const listeners = new Set<() => void>()
let timer: ReturnType<typeof setInterval> | null = null

/** The current minute, as ms since the epoch. Stable within a minute, which is what `useSyncExternalStore` needs of a snapshot. */
export function coarseNow(): number {
  return Math.floor(Date.now() / COARSE_NOW_STEP_MS) * COARSE_NOW_STEP_MS
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  if (timer === null) timer = setInterval(() => { for (const l of [...listeners]) l() }, COARSE_NOW_STEP_MS)
  return () => {
    listeners.delete(listener)
    if (listeners.size === 0 && timer !== null) {
      clearInterval(timer)
      timer = null
    }
  }
}

export function useCoarseNow(): number {
  return useSyncExternalStore(subscribe, coarseNow, coarseNow)
}
