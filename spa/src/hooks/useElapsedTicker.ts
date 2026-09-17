// spa/src/hooks/useElapsedTicker.ts
// 1 s clock for running-tool elapsed badges (spec §4.2, last paragraph).
//
// Returns `Date.now()` sampled at mount. While `active`, re-samples every
// 1000 ms so consumers can compute `max(0, now - startedAt)`; the interval
// is cleared when `active` flips false and on unmount. Flipping inactive
// keeps the last value (no reset) — a finished tool's badge is computed
// from `endedAt`, not from this clock, so nothing depends on it moving.
import { useEffect, useState } from 'react'

export const ELAPSED_TICK_MS = 1_000

export function useElapsedTicker(active: boolean): number {
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!active) return
    const id = setInterval(() => setNow(Date.now()), ELAPSED_TICK_MS)
    return () => clearInterval(id)
  }, [active])

  return now
}
