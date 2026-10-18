// spa/src/lib/names/persona-loader.ts — keeps `usePersonaStore` current (name pool v2 plan P4): read when a host's
// capabilities say it serves personas, and again on its `hostconfig.personas` event. A daemon that does not declare
// `personas.v1` is never asked, so nothing changes against an older daemon.
//
// The load is driven by the Nex store's capability entry, not by the connection: that store invalidates and refetches
// its entry on its own subscription, so ordering two host-store subscribers would race (a reconnect to a downgraded
// daemon would still see the old entry; an endpoint move would see the entry cleared). Reacting to the entry itself
// makes the order irrelevant.
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore, selectPersonasV1 } from '../../stores/useNexHostStore'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { hostEndpointKey } from '../../stores/useHostConfigStore'

export const PERSONAS_EVENT_TYPE = 'hostconfig.personas'

function supported(hostId: string): boolean {
  return selectPersonasV1(hostId)(useNexHostStore.getState())
}

/** Loads now when the host's current capabilities declare `personas.v1`; otherwise does nothing. */
export async function loadPersonasIfSupported(hostId: string): Promise<void> {
  if (supported(hostId)) await usePersonaStore.getState().load(hostId)
}

/** The event carries only `{revision}`; the collection is re-read whole. */
export function handlePersonasEvent(hostId: string): void {
  void loadPersonasIfSupported(hostId)
}

/** Asks for the capability entry; the Nex subscription below does the loading once it lands. */
function ensureCapabilities(hostId: string): void {
  void useNexHostStore.getState().ensure(hostId)
}

export function startPersonaLoader(): () => void {
  for (const [hostId, rt] of Object.entries(useHostStore.getState().runtime)) {
    if (rt?.status === 'connected') {
      ensureCapabilities(hostId)
      void loadPersonasIfSupported(hostId)
    }
  }

  const stopNex = useNexHostStore.subscribe((next, prev) => {
    for (const hostId of Object.keys(next.byHost)) {
      const now = selectPersonasV1(hostId)(next)
      const before = selectPersonasV1(hostId)(prev)
      if (now && (!before || next.byHost[hostId] !== prev.byHost[hostId])) void usePersonaStore.getState().load(hostId)
      else if (!now && before) usePersonaStore.getState().forget(hostId) // the daemon stopped declaring it (downgrade)
    }
  })

  const stopHosts = useHostStore.subscribe((next, prev) => {
    for (const hostId of Object.keys(prev.hosts)) {
      if (!next.hosts[hostId]) usePersonaStore.getState().forget(hostId)
    }
    for (const hostId of Object.keys(next.hosts)) {
      const connected = next.runtime[hostId]?.status === 'connected'
      // The cached copy belongs to the old daemon when the address, port or token moved under the same id.
      const moved = !!prev.hosts[hostId] && hostEndpointKey(prev.hosts[hostId]) !== hostEndpointKey(next.hosts[hostId])
      if (moved) usePersonaStore.getState().forget(hostId)
      if (connected && (moved || prev.runtime[hostId]?.status !== 'connected')) ensureCapabilities(hostId)
    }
  })

  return () => { stopNex(); stopHosts() }
}
