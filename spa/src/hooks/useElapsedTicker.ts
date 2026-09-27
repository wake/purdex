// spa/src/hooks/useElapsedTicker.ts
// 1 s clock for running-tool elapsed badges (spec §4.2, last paragraph).
//
// Returns `Date.now()` sampled at mount. When `active` becomes true it
// re-samples at once (so a freshly running tool never shows a stale clock)
// and then every `intervalMs` (1000 by default; the R4 dock passes 30 s) so consumers can compute `max(0, now - startedAt)`;
// the interval is cleared when `active` flips false and on unmount. Flipping
// inactive keeps the last value (no reset) — a finished tool's badge is
// computed from `endedAt`, not from this clock, so nothing depends on it
// moving.
import { useEffect, useState } from 'react'

export const ELAPSED_TICK_MS = 1_000

export function useElapsedTicker(active: boolean, intervalMs: number = ELAPSED_TICK_MS): number {
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!active) return
    // eslint-disable-next-line react-hooks/set-state-in-effect -- fresh sample on activation; the interval alone would lag up to 1 s
    setNow(Date.now())
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [active, intervalMs])

  return now
}
