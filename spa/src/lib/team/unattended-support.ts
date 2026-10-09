// spa/src/lib/team/unattended-support.ts — does each host's daemon support 無人值守模式 (unattended spec D-U23-5:
// "a daemon without the capability … counts as unsupported"; plan PU-2a). One module-level subscription for the
// app's lifetime, started from main.tsx.
//
// Triggers, per host and only while connected: the transition to `connected`, or a change of its endpoint or token.
// Each trigger is one `/api/info`: `capabilities` an array holding `relay.unattended.v1` → 'yes', any other answer →
// 'no'. A failed request leaves the support as it was ('unknown' for a new host) until the next trigger; nothing here
// polls. Only the newest request per host may apply (host-daemon-id.ts's generation guard), and a removed host's
// answer never does.
//
// A re-point (endpoint or token) or a removal forgets the host's entry: what the old daemon said — its support and
// its switch — is not the new one's. The new connection's snapshot and this probe fill it again.
import { fetchHostInfo } from '../host-api'
import { hostEndpoint, useHostStore, type HostConfig } from '../../stores/useHostStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useRelayQuotaStore } from './relay-quota'
import { RELAY_QUOTA_CAPABILITY, UNATTENDED_CAPABILITY } from './types'

const identity = (h: HostConfig): string => `${hostEndpoint(h)}:${h.token ?? ''}`

export function startUnattendedSupport(): () => void {
  let counter = 0
  // Generation of the newest request per host; absent = none may apply.
  const current = new Map<string, number>()

  const probe = (hostId: string) => {
    const generation = ++counter
    current.set(hostId, generation)
    fetchHostInfo(hostId).then(
      (info) => {
        if (current.get(hostId) !== generation) return
        const caps: unknown = info?.capabilities
        const listed = Array.isArray(caps) ? caps : []
        useUnattendedStore.getState().setSupport(hostId, listed.includes(UNATTENDED_CAPABILITY) ? 'yes' : 'no')
        useUnattendedStore.getState().setQuotaSupport(hostId, listed.includes(RELAY_QUOTA_CAPABILITY) ? 'yes' : 'no')
      },
      () => { /* not retried until the next trigger */ },
    )
  }

  const initial = useHostStore.getState()
  for (const [hostId, rt] of Object.entries(initial.runtime)) {
    if (rt?.status === 'connected' && initial.hosts[hostId]) probe(hostId)
  }

  return useHostStore.subscribe((next, prev) => {
    const store = useUnattendedStore.getState()
    for (const hostId of [...current.keys()]) {
      if (!next.hosts[hostId]) current.delete(hostId)
    }
    for (const hostId of Object.keys(store.byHost)) {
      if (!next.hosts[hostId]) store.forgetHost(hostId)
    }
    for (const hostId of Object.keys(useRelayQuotaStore.getState().confirmed).map((k) => k.split('\u0000')[0])) {
      if (!next.hosts[hostId]) useRelayQuotaStore.getState().forgetHost(hostId) // a removed host's numbers go with it
    }
    for (const [hostId, host] of Object.entries(next.hosts)) {
      const before = prev.hosts[hostId]
      const repointed = before !== undefined && identity(before) !== identity(host)
      if (repointed) {
        current.delete(hostId) // an answer still on its way is the old daemon's
        store.forgetHost(hostId)
        useRelayQuotaStore.getState().forgetHost(hostId) // and so are the quota numbers it confirmed
      }
      if (next.runtime[hostId]?.status !== 'connected') continue
      if (repointed || !before || prev.runtime[hostId]?.status !== 'connected') probe(hostId)
    }
  })
}
