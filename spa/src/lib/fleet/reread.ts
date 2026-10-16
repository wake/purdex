// spa/src/lib/fleet/reread.ts — read `GET /api/fleet` from every daemon that carries `fleet.v1` (spec §6): after a push of a
// higher term from another daemon, and every 5 s while no primary is heard. Each reply is written under the generation it
// was asked in (store.ts), so an answer of a connection that is gone lands nowhere. One request per host at a time.
import { fetchFleet } from './api'
import { useFleetStore } from './store'

const inflight = new Map<string, number>()

export function readFleetOf(hostId: string): void {
  const store = useFleetStore.getState()
  const gen = store.genOf(hostId)
  if (inflight.get(hostId) === gen) return
  inflight.set(hostId, gen)
  const done = () => { if (inflight.get(hostId) === gen) inflight.delete(hostId) }
  fetchFleet(hostId).then(
    (answer) => { done(); useFleetStore.getState().applyFetched(hostId, gen, answer) },
    () => done(), // the next 5 s round (or push) asks again
  )
}

export function readAllFleets(): void {
  const { byHost } = useFleetStore.getState()
  for (const [hostId, e] of Object.entries(byHost)) if (e.support === 'yes') readFleetOf(hostId)
}
