// spa/src/lib/pool/use-host-observations.ts — the 行程 tab's hook onto `host-observations.ts` (monitoring tabs plan MON-8c). Reads the undeclared heavy commands once when the tab shows and again every 60 s while it stays, but only when
// the source daemon announces `pool.observations.v1` and the host is ok: the records live in that host's wwd, so a host that is down only gets a read that times out. The fleet is not read.
import { useEffect, useState } from 'react'
import { usePoolStore } from './store'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { holdObservations, loadUndeclared, observationKey, readUndeclared, readUndeclaredLoad, subscribeObservations, type ObservationLoad } from './host-observations'
import type { UndeclaredPage } from './observations-api'

const REFRESH_MS = 60_000
const CAPABILITY = 'pool.observations.v1'

export type ObservationsAvailability = 'ok' | 'no_route' | 'host_down'

export function useHostObservations(hostId: string, alias: string, hostOk: boolean): {
  availability: ObservationsAvailability; page: UndeclaredPage | null; load: ObservationLoad
} {
  const source = usePoolStore((s) => s.sourceHostId)
  // Subscribed: the daemon's capabilities may arrive after the page opened, and that must start the read.
  const announced = useNexHostStore((s) => !!source && s.byHost[source]?.daemonCapabilities?.includes(CAPABILITY) === true)
  const [, setRev] = useState(0)
  useEffect(() => subscribeObservations(hostId, () => setRev((n) => n + 1)), [hostId])

  const on = !!source && announced && hostOk
  useEffect(() => {
    if (!on || !source) return
    const release = holdObservations(hostId)
    void loadUndeclared(source, alias, hostId, Date.now())
    const timer = setInterval(() => { void loadUndeclared(source, alias, hostId, Date.now()) }, REFRESH_MS)
    return () => { clearInterval(timer); release() }
  }, [on, source, alias, hostId])

  const availability: ObservationsAvailability = !announced ? 'no_route' : !hostOk ? 'host_down' : 'ok'
  return { availability, page: source ? (readUndeclared(hostId, observationKey(source, alias))?.page ?? null) : null, load: readUndeclaredLoad(hostId) }
}
