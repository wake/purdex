// spa/src/lib/pool/use-series-history.ts — the host tab's hook onto the daemon's series read: 1 h / 6 h / 24 h are read once when the tab opens on a live host
// (the daemon must have announced `pool.series.v1`); the 5 minute range is `use-facts-history.ts`'s.
import { useEffect, useState } from 'react'
import { usePoolStore } from './store'
import { invalidateFactsHistory, loadSeries, seriesAnnounced } from './history'
import type { HistoryRange } from '../fleet/history'

/** The count of finished loads: the charts' memo depends on it, since a finished load changes the history without any other render cause. */
export function useSeriesHistory(fleetHostId: string, alias: string | null, range: HistoryRange, live: boolean, memTotalMb: number | null): number {
  const source = usePoolStore((s) => s.sourceHostId)
  const [rev, setRev] = useState(0)
  const hasTotal = memTotalMb !== null && memTotalMb > 0
  const enabled = live && range !== '5m' && alias !== null && source !== null && seriesAnnounced(source)

  useEffect(() => {
    if (!enabled || !source || !alias) return
    let cancelled = false
    void loadSeries({ sourceHostId: source, alias, fleetHostId, range, memTotalMb, now: Date.now() }).then(() => { if (!cancelled) setRev((n) => n + 1) })
    return () => { cancelled = true; invalidateFactsHistory(fleetHostId) }
    // memTotalMb itself is the host's constant total: only whether it is usable (> 0) matters, since without it the memory chart is left out of the series.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, source, alias, fleetHostId, range, hasTotal])

  return rev
}
