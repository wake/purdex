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
import { backfillHosts } from './persona-backfill'
import { startSourcePin } from '../../stores/usePersonaSourceStore'

export const PERSONAS_EVENT_TYPE = 'hostconfig.personas'

type NexState = ReturnType<typeof useNexHostStore.getState>

/**
 * The entry declares `personas.v1` AND was fetched from the daemon now answering. A stale entry (`fetchedAt` 0:
 * invalidated by a reconnect and not yet refetched) keeps the old capabilities, which a downgraded daemon no longer
 * has, so it never counts: the refetch landing is what triggers the load.
 */
function freshSupported(state: Pick<NexState, 'byHost'>, hostId: string): boolean {
  return (state.byHost[hostId]?.fetchedAt ?? 0) > 0 && selectPersonasV1(hostId)(state)
}

/** Whether the host's fresh capabilities declare `personas.v1` (the same test the load uses). */
export function personasSupported(hostId: string): boolean {
  return freshSupported(useNexHostStore.getState(), hostId)
}

/** Loads now when the host's fresh capabilities declare `personas.v1`; otherwise does nothing. */
export async function loadPersonasIfSupported(hostId: string): Promise<void> {
  if (freshSupported(useNexHostStore.getState(), hostId)) await loadThenBackfill(hostId)
}

/** A read can be the one that completes the picture (the source or a host just arrived), so hosts are compared after it. */
async function loadThenBackfill(hostId: string): Promise<void> {
  await usePersonaStore.getState().load(hostId)
  try { await backfillHosts() } catch { /* the next load tries again; a failed copy must not break loading */ }
}

/** The event carries only `{revision}`; the collection is re-read whole. */
export function handlePersonasEvent(hostId: string): void {
  void loadPersonasIfSupported(hostId)
}

/**
 * Asks for the capability entry; the Nex subscription below does the loading once it lands. Deferred to a microtask so
 * the Nex store's own host-store subscriber (clear on an endpoint move, invalidate on reconnect) has already run,
 * whichever order the two were started in: asking first would start a fetch against the entry about to be cleared.
 */
function ensureCapabilities(hostId: string): void {
  queueMicrotask(() => { void useNexHostStore.getState().ensure(hostId) })
}

export function startPersonaLoader(): () => void {
  const stopPin = startSourcePin()
  for (const [hostId, rt] of Object.entries(useHostStore.getState().runtime)) {
    if (rt?.status === 'connected') {
      ensureCapabilities(hostId)
      void loadPersonasIfSupported(hostId)
    }
  }

  const stopNex = useNexHostStore.subscribe((next, prev) => {
    for (const hostId of Object.keys(next.byHost)) {
      if (next.byHost[hostId] === prev.byHost[hostId]) continue
      if (freshSupported(next, hostId)) void loadThenBackfill(hostId)
      // A refetch that no longer declares it: the daemon was downgraded.
      else if ((next.byHost[hostId]?.fetchedAt ?? 0) > 0 && selectPersonasV1(hostId)(prev)) usePersonaStore.getState().forget(hostId)
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
      else if (prev.runtime[hostId]?.status === 'connected' && !connected) usePersonaStore.getState().markOffline(hostId)
      // A dropped connection leaves the cached copy "ready", which would keep an unreachable source editable. The
      // reconnect refetches the capabilities and loads again, which makes the entry ready.
      if (connected && (moved || prev.runtime[hostId]?.status !== 'connected')) ensureCapabilities(hostId)
    }
  })

  return () => { stopPin(); stopNex(); stopHosts() }
}
