// spa/src/lib/relay/fresh-sync.ts — keeps useRelayFreshStore current (relay v2 contract 5, `relay.half.v1`). One module-level
// subscription set for the app's lifetime, started from main.tsx. Per host:
//   - the capability is read through the Nexen readiness cache (`ensure`, the host's `/api/info`), once a host is connected;
//   - while the host lists `relay.half.v1`: `GET /api/relay/fresh` once when it connects and again on every reconnect, and
//     once when the flag turns true (the first `relay.fresh` snapshot may have arrived before the flag was known and been
//     ignored, fresh-ws.ts). The `relay.fresh` events replace the list as they come; which of an event and an answer is
//     newer is the store's generation (useRelayFreshStore), so an old answer never overwrites a newer event;
//   - a host without the flag (or one whose flag is lost) has its list dropped, a removed or re-pointed host forgets it
//     (what the old daemon said is not the new one's, as roster-forget.ts);
//   - a re-point does not wait on which of this and the Nexen cache's own host-store watcher runs first: the cache entry
//     vanishing (`clearHost`) on a connected host is itself the cue to ask `/api/info` again, so the new daemon's flag is
//     always read, and a flag the old daemon had is never trusted for the new one.
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
      if (!connected || (wasConnected && !repointed)) continue
      // A re-pointed host's cached flag may still be the old daemon's (this watcher can run before the cache's own): do not
      // read on it, ask the new daemon's `/api/info` (the flag turning true reads the list). The cache's `clearHost` in the
      // same change is answered the same way below, whichever watcher ran first.
      if (repointed) void useNexHostStore.getState().ensure(hostId)
      else onConnected(hostId)
    }
  })

  const unsubNex = useNexHostStore.subscribe((next, prev) => {
    if (next.byHost === prev.byHost) return
    for (const hostId of new Set([...Object.keys(next.byHost), ...Object.keys(prev.byHost)])) {
      // The entry dropped on a connected host (`clearHost` on a re-point): ask again for the new daemon. Only a drop is a cue
      // (`ensure` itself never drops one, so this cannot loop) and only a configured, connected host has anything to ask.
      const { hosts, runtime } = useHostStore.getState()
      if (prev.byHost[hostId] && !next.byHost[hostId] && hosts[hostId] && runtime[hostId]?.status === 'connected') {
        void next.ensure(hostId)
      }
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
