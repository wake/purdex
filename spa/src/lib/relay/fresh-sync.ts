// spa/src/lib/relay/fresh-sync.ts — keeps useRelayFreshStore current (relay v2 contract 5, `relay.half.v1`). One module-level
// subscription set for the app's lifetime, started from main.tsx. Per host:
//   - the capability is read through the Nexen readiness cache (`ensure`, the host's `/api/info`), once a host is connected;
//   - while the host lists `relay.half.v1`: `GET /api/relay/fresh` once when it connects and again on every reconnect, and
//     once when the flag turns true (the first `relay.fresh` snapshot may have arrived before the flag was known and been
//     ignored, fresh-ws.ts). The `relay.fresh` events replace the list as they come; which of an event and an answer is
//     newer is the store's generation (useRelayFreshStore), so an old answer never overwrites a newer event;
//   - a host without the flag (or one whose flag is lost) has its list dropped, a removed or re-pointed host forgets it
//     (what the old daemon said is not the new one's, as roster-forget.ts).
// A failed GET is silent: the list held stays, the next reconnect or event brings the truth.
import { useHostStore } from '../../stores/useHostStore'
import { selectRelayHalfV1, useNexHostStore } from '../../stores/useNexHostStore'
import { useRelayFreshStore } from '../../stores/useRelayFreshStore'
import { connectionKey } from '../host-connection-key'
import { hostIdentityNow } from '../team/quota-host'
import { fetchRelayFresh } from './fresh'

const flagOf = (hostId: string): boolean => selectRelayHalfV1(hostId)(useNexHostStore.getState())

/** Read the host's list now (a no-op for a host without `relay.half.v1`). An answer is applied only if the host is still the
 *  same daemon (endpoint and token) and still lists the capability, and no event has arrived since it was asked. */
export function readRelayFresh(hostId: string): void {
  const identity = hostIdentityNow(hostId)
  if (identity === null || !flagOf(hostId)) return
  const gen = useRelayFreshStore.getState().begin(hostId)
  fetchRelayFresh(hostId).then(
    (list) => {
      if (hostIdentityNow(hostId) !== identity || !flagOf(hostId)) return
      useRelayFreshStore.getState().applyFetched(hostId, gen, list)
    },
    () => { /* keep the list shown; the next reconnect or event brings the truth */ },
  )
}

export function startRelayFresh(): () => void {
  /** A host just became connected (or was connected when this started): read its list, or find out whether it has one. */
  const onConnected = (hostId: string) => {
    if (flagOf(hostId)) readRelayFresh(hostId)
    else void useNexHostStore.getState().ensure(hostId) // the flag turning true (below) reads the list
  }

  const unsubHosts = useHostStore.subscribe((next, prev) => {
    const store = useRelayFreshStore.getState()
    for (const hostId of Object.keys(prev.hosts)) {
      if (!next.hosts[hostId]) store.forgetHost(hostId)
    }
    for (const [hostId, host] of Object.entries(next.hosts)) {
      const before = prev.hosts[hostId]
      const repointed = before !== undefined && connectionKey(before) !== connectionKey(host)
      if (repointed) store.forgetHost(hostId)
      const connected = next.runtime[hostId]?.status === 'connected'
      const wasConnected = prev.runtime[hostId]?.status === 'connected'
      if (connected && (!wasConnected || repointed)) onConnected(hostId)
    }
  })

  const unsubNex = useNexHostStore.subscribe((next, prev) => {
    if (next.byHost === prev.byHost) return
    for (const hostId of new Set([...Object.keys(next.byHost), ...Object.keys(prev.byHost)])) {
      const was = selectRelayHalfV1(hostId)(prev)
      const now = selectRelayHalfV1(hostId)(next)
      if (was === now) continue
      if (!now) useRelayFreshStore.getState().forgetHost(hostId)
      else if (useHostStore.getState().runtime[hostId]?.status === 'connected') readRelayFresh(hostId)
    }
  })

  // Hosts already connected when this starts.
  const initial = useHostStore.getState()
  for (const [hostId, rt] of Object.entries(initial.runtime)) {
    if (rt?.status === 'connected' && initial.hosts[hostId]) onConnected(hostId)
  }

  return () => {
    unsubHosts()
    unsubNex()
  }
}
