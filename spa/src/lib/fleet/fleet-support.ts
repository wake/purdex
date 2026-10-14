// spa/src/lib/fleet/fleet-support.ts — does each host's daemon carry `fleet.v1`, and its first picture of the fleet
// (plan T-2a.2). One module-level subscription for the app's lifetime, started from main.tsx; it is its own module so
// the unattended-support probe (another line's) is not touched.
//
// Per host and only while connected: the transition to `connected`, or a change of its endpoint or token, begins a new
// fleet generation (store.ts) and asks `/api/info` once. A `fleet.v1` answer starts the one GET `/api/fleet` — also when
// the answer arrives long after the connection came up — and its reply is written only under the generation it began in.
// A disconnect, a re-point and a removal each move the generation, so a late reply from the old connection is dropped.
// A failed request leaves the support unknown until the next trigger; nothing here polls.
import { fetchHostInfo } from '../host-api'
import { hostEndpoint, useHostStore, type HostConfig } from '../../stores/useHostStore'
import { fetchFleet } from './api'
import { useFleetStore } from './store'
import { FLEET_CAPABILITY } from './types'

const identity = (h: HostConfig): string => `${hostEndpoint(h)}:${h.token ?? ''}`

export function startFleetSupport(): () => void {
  const probe = (hostId: string) => {
    const fleet = useFleetStore.getState()
    const gen = fleet.begin(hostId)
    const live = () => useFleetStore.getState().genOf(hostId) === gen
    fetchHostInfo(hostId).then(
      (info) => {
        if (!live()) return
        const caps: unknown = info?.capabilities
        const yes = Array.isArray(caps) && caps.includes(FLEET_CAPABILITY)
        useFleetStore.getState().setSupport(hostId, gen, yes ? 'yes' : 'no')
        if (!yes) return
        fetchFleet(hostId).then(
          (payload) => useFleetStore.getState().applyFetched(hostId, gen, payload),
          () => { /* not retried: the next `fleet.changed` (or reconnect) fills it */ },
        )
      },
      () => { /* support stays unknown until the next trigger */ },
    )
  }

  const initial = useHostStore.getState()
  for (const [hostId, rt] of Object.entries(initial.runtime)) {
    if (rt?.status === 'connected' && initial.hosts[hostId]) probe(hostId)
  }

  return useHostStore.subscribe((next, prev) => {
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
}
