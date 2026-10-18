// spa/src/lib/names/persona-loader.ts — keeps `usePersonaStore` current (name pool v2 plan P4): read when a host
// connects and when its `hostconfig.personas` event arrives. A daemon that does not declare `personas.v1` is never
// asked, so nothing changes against an older daemon.
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore, selectPersonasV1 } from '../../stores/useNexHostStore'
import { usePersonaStore } from '../../stores/usePersonaStore'

export const PERSONAS_EVENT_TYPE = 'hostconfig.personas'

export async function loadPersonasIfSupported(hostId: string): Promise<void> {
  await useNexHostStore.getState().ensure(hostId)
  if (!selectPersonasV1(hostId)(useNexHostStore.getState())) return
  await usePersonaStore.getState().load(hostId)
}

/** The event carries only `{revision}`; the collection is re-read whole. */
export function handlePersonasEvent(hostId: string): void {
  void loadPersonasIfSupported(hostId)
}

export function startPersonaLoader(): () => void {
  for (const [hostId, rt] of Object.entries(useHostStore.getState().runtime)) {
    if (rt?.status === 'connected') void loadPersonasIfSupported(hostId)
  }
  return useHostStore.subscribe((next, prev) => {
    for (const hostId of Object.keys(prev.hosts)) {
      if (!next.hosts[hostId]) usePersonaStore.getState().forget(hostId)
    }
    for (const hostId of Object.keys(next.hosts)) {
      const connected = next.runtime[hostId]?.status === 'connected'
      if (connected && prev.runtime[hostId]?.status !== 'connected') void loadPersonasIfSupported(hostId)
    }
  })
}
