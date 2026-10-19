// spa/src/lib/pool/use-facts-history.ts — the host tab's hook onto `history.ts`: reads the daemon's last 5 minutes once when the tab opens on a live host
// (5 minute range only), and adds each new facts sample's zone to the band as it arrives.
import { useEffect, useState } from 'react'
import { usePoolStore } from './store'
import { loadFactsHistory, readHistoryLoad, readZones, recordZone, type HistoryLoad, type ZonePoint } from './history'
import type { PoolFacts } from './types'

export function useFactsHistory(fleetHostId: string, alias: string | null, enabled: boolean, live: PoolFacts | null): { load: HistoryLoad; zones: ZonePoint[] } {
  const source = usePoolStore((s) => s.sourceHostId)
  // The outcome outlives this component (the pane unmounts on a tab switch): the memory is the source, the state only re-renders.
  const [load, setLoad] = useState<HistoryLoad>(() => readHistoryLoad(fleetHostId))

  useEffect(() => {
    if (!enabled || !source || !alias) return
    let cancelled = false
    void loadFactsHistory(source, alias, fleetHostId, Date.now()).then((r) => { if (!cancelled) setLoad(r) })
    return () => { cancelled = true }
  }, [enabled, source, alias, fleetHostId])

  const at = live?.at ?? null
  const zone = live?.derived.zone ?? null
  // The sample is written to the memory after this render, so this render adds it itself: the band shows it at once, with no state to re-render on.
  useEffect(() => {
    if (!enabled || at === null || zone === null) return
    recordZone(fleetHostId, at, zone)
  }, [enabled, fleetHostId, at, zone])
  const stored = readZones(fleetHostId)
  const zones = enabled && at !== null && zone !== null && !stored.some((p) => p.t === at) ? [...stored, { t: at, zone }] : stored

  return { load, zones }
}
