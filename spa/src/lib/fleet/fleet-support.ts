// spa/src/lib/fleet/fleet-support.ts — does each host's daemon carry `fleet.v1`, and its first picture of the fleet (plan
// T-2a1.4). One module-level subscription for the app's lifetime, started from main.tsx next to the other capability probes;
// it is its own module so unattended-support.ts (another line's) is not touched, but it follows the same triggers: per host
// and only while connected, the transition to `connected` or a change of its endpoint or token. Each trigger is one
// `/api/info`; a `fleet.v1` answer starts the one GET `/api/fleet`, whose reply is written under the generation it began in.
// A disconnect fences the host, a re-point or a removal forgets it, so a late answer of the old connection is dropped.
//
// Its one timer ticks every second: it checks the 10 s staleness (store.ts) and, while the cells wait for a primary (stale,
// or only a standby/nothing heard), reads every `fleet.v1` daemon's `GET /api/fleet` every 5 s until a primary answers.
import { fetchHostInfo } from '../host-api'
import { hostEndpoint, useHostStore, type HostConfig } from '../../stores/useHostStore'
import { readAllFleets, readFleetOf } from './reread'
import { useFleetStore } from './store'
import { FLEET_CAPABILITY } from './types'

export const REREAD_MS = 5_000
const identity = (h: HostConfig): string => `${hostEndpoint(h)}:${h.token ?? ''}`

export function startFleetSupport(): () => void {
  const probe = (hostId: string) => {
    const gen = useFleetStore.getState().begin(hostId)
    fetchHostInfo(hostId).then(
      (info) => {
        if (useFleetStore.getState().genOf(hostId) !== gen) return
        const caps: unknown = info?.capabilities
        const yes = Array.isArray(caps) && caps.includes(FLEET_CAPABILITY)
        useFleetStore.getState().setSupport(hostId, gen, yes ? 'yes' : 'no')
        if (yes) readFleetOf(hostId)
      },
      () => { /* support stays unknown until the next trigger; nothing here polls it */ },
    )
  }

  const initial = useHostStore.getState()
  for (const [hostId, rt] of Object.entries(initial.runtime)) {
    if (rt?.status === 'connected' && initial.hosts[hostId]) probe(hostId)
  }

  const unsub = useHostStore.subscribe((next, prev) => {
    const fleet = useFleetStore.getState()
    for (const hostId of Object.keys(prev.hosts)) {
      if (!next.hosts[hostId]) fleet.forgetHost(hostId)
    }
    for (const [hostId, host] of Object.entries(next.hosts)) {
      const before = prev.hosts[hostId]
      const repointed = before !== undefined && identity(before) !== identity(host)
      const connected = next.runtime[hostId]?.status === 'connected'
      const wasConnected = prev.runtime[hostId]?.status === 'connected'
      if (repointed) fleet.forgetHost(hostId) // what the old daemon said is not the new one's
      if (!connected) {
        if (wasConnected && !repointed) fleet.fence(hostId)
        continue
      }
      if (repointed || !before || !wasConnected) probe(hostId)
    }
  })

  let lastRound = Date.now()
  const timer = setInterval(() => {
    const now = Date.now()
    useFleetStore.getState().checkStale(now)
    const s = useFleetStore.getState()
    const waiting = s.waitingPrimary || s.primaryHostId === null
    if (waiting && now - lastRound >= REREAD_MS) { lastRound = now; readAllFleets() }
    if (!waiting) lastRound = now
  }, 1000)

  return () => { unsub(); clearInterval(timer) }
}
