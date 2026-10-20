// spa/src/lib/pool/use-cell-models.ts — the fleet cells' hook onto the resource pool. It runs the pool store's poll while a strip of
// cells is mounted, and turns each fleet host into the cell's model. While no daemon announces `pool.v1` (or none has answered yet),
// `active` is false and every model is the fleet host unchanged: the screens are then exactly what they were.
import { useEffect, useMemo, useState } from 'react'
import { useFleetStore, type FleetHostView } from '../fleet/store'
import { poolCellModel, matchPoolHost, type PoolCellModel } from './cell-model'
import { poolHostView } from './host-view'
import { usePoolStore } from './store'
import { NEX_HOST_TTL_MS } from '../../stores/useNexHostStore'
import { refreshPoolIdentities, usePoolHostDaemons } from './daemons'

/** How often the freshness of the facts (30 s) is looked at again when nothing new arrives. */
const CLOCK_MS = 5000

// Cells can be mounted more than once (wide and narrow bars): the poll runs while any is.
let mounted = 0
export function useRunPool() {
  useEffect(() => {
    if (mounted++ === 0) usePoolStore.getState().start()
    // A daemon that learned its wwd after the App first read /api/info is picked up when the pool's hosts change.
    refreshPoolIdentities()
    const unsubscribe = usePoolStore.subscribe((s, prev) => { if (s.hosts !== prev.hosts) refreshPoolIdentities() })
    // An event-driven pool stops polling, so nothing else would ask again once the 60 s cache TTL has passed.
    const timer = setInterval(refreshPoolIdentities, NEX_HOST_TTL_MS)
    return () => { unsubscribe(); clearInterval(timer); if (--mounted === 0) usePoolStore.getState().stop() }
  }, [])
}

function useClock(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), CLOCK_MS)
    return () => clearInterval(id)
  }, [])
  return now
}

export function usePoolCellModels(views: FleetHostView[]): { models: PoolCellModel[]; active: boolean } {
  useRunPool()
  const now = useClock()
  const hosts = usePoolStore((s) => s.hosts)
  const oldDaemon = usePoolStore((s) => s.oldDaemon)
  const answered = usePoolStore((s) => s.sourceHostId !== null)
  const failed = usePoolStore((s) => s.error !== null)
  const lastLiveAt = usePoolStore((s) => s.lastLiveAt)
  const daemons = usePoolHostDaemons()
  const fleetStale = useFleetStore((s) => s.stale)
  // Active once the pool has said something: a list (an empty one is an answer too), or that every daemon failed. Before the first read the cells stay the fleet's.
  const active = !oldDaemon && (answered || failed)
  const models = useMemo(() => views.map((v) => {
    if (!active) return poolCellModel(v, null, null)
    const pool = matchPoolHost(hosts, v, daemons)
    const alias = pool?.alias ?? v.alias
    return poolCellModel(v, poolHostView({ alias, pool, fleet: v, fleetStale, poolLastOk: lastLiveAt[alias] ?? null }, now), pool)
  }), [views, active, hosts, daemons, fleetStale, lastLiveAt, now])
  return { models, active }
}
