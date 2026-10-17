// spa/src/lib/watch/events-store.ts — each watcher host's events, and the count of those still waiting (plan W5e, spec §5).
//
// A host's events are read when its status first answers `ok` (its daemon carries the watch module, `watch.v1`) and again on
// each `watch.events` push (`rereadWatchEvents`, called from the host event socket). There is no timer. A reply is written
// only under the generation it began in: a disconnect, a removal, or a newer read moves it, so an answer of an old
// connection, or an older answer arriving after a newer one, is dropped. Nothing here is persisted.
import { create } from 'zustand'
import { useHostStore } from '../../stores/useHostStore'
import { fetchWatchEvents } from './api'
import type { WatchEvent } from './events-types'
import { useWatchStatusStore } from './status-store'

export type EventsEntry =
  | { kind: 'ok'; events: WatchEvent[]; at: number }
  | { kind: 'error'; at: number }

interface WatchEventsState {
  /** App host id → what that host's daemon said about its events. */
  byHost: Record<string, EventsEntry>
  /** App hosts that carried the watch module at some point since the App started (kept when they go offline: the tab says so). */
  known: string[]
  know: (hostId: string) => void
  set: (hostId: string, entry: EventsEntry) => void
  forget: (hostId: string) => void
  reset: () => void
}

export const useWatchEventsStore = create<WatchEventsState>((set) => ({
  byHost: {},
  known: [],
  know: (hostId) => set((s) => (s.known.includes(hostId) ? s : { known: [...s.known, hostId] })),
  set: (hostId, entry) => set((s) => ({ byHost: { ...s.byHost, [hostId]: entry } })),
  forget: (hostId) => set((s) => {
    if (!(hostId in s.byHost)) return s
    const { [hostId]: _gone, ...byHost } = s.byHost
    return { byHost }
  }),
  reset: () => set({ byHost: {}, known: [] }),
}))

export const isPending = (e: WatchEvent): boolean => e.status === 'pending'

/** How many events wait for the watcher on the hosts that answered. A host that did not answer counts none. */
export function pendingCount(byHost: Record<string, EventsEntry>): number {
  let n = 0
  for (const e of Object.values(byHost)) if (e.kind === 'ok') n += e.events.filter(isPending).length
  return n
}

/** Whether any connected host carries the watch module: the status answered `ok` (the events tab exists from then on). */
export function anyHostDeclaresWatch(statusByHost: Record<string, { kind: string }>): boolean {
  return Object.values(statusByHost).some((e) => e.kind === 'ok')
}

/** The running reader's re-read of one host (null while `startWatchEvents` is not running). */
let rereader: ((hostId: string) => void) | null = null

/** A `watch.events` push: read that host's events again, if it is connected and carries the watch module. */
export function rereadWatchEvents(hostId: string): void {
  rereader?.(hostId)
}

/** Installed once from main.tsx. Returns how to stop it. */
export function startWatchEvents(): () => void {
  const gens = new Map<string, number>()
  const bump = (hostId: string): number => { const g = (gens.get(hostId) ?? 0) + 1; gens.set(hostId, g); return g }
  const isConnected = (hostId: string): boolean => {
    const s = useHostStore.getState()
    return !!s.hosts[hostId] && s.runtime[hostId]?.status === 'connected'
  }
  const carriesWatch = (hostId: string): boolean => useWatchStatusStore.getState().byHost[hostId]?.kind === 'ok'

  const read = (hostId: string): void => {
    const gen = bump(hostId)
    void fetchWatchEvents(hostId).then((r) => {
      if (gens.get(hostId) !== gen || !isConnected(hostId)) return
      if (r.kind === 'unsupported') useWatchEventsStore.getState().forget(hostId)
      else useWatchEventsStore.getState().set(hostId, r.kind === 'ok' ? { kind: 'ok', events: r.events, at: Date.now() } : { kind: 'error', at: Date.now() })
    })
  }
  rereader = (hostId) => { if (isConnected(hostId) && carriesWatch(hostId)) read(hostId) }

  // A host whose first read is under way: the status store may change again before it lands.
  const reading = new Set<string>()
  const first = (): void => {
    for (const [id, e] of Object.entries(useWatchStatusStore.getState().byHost)) {
      if (e.kind === 'ok' && isConnected(id) && !(id in useWatchEventsStore.getState().byHost) && !reading.has(id)) { reading.add(id); useWatchEventsStore.getState().know(id); read(id) }
    }
  }
  const unsubStatus = useWatchStatusStore.subscribe((next, prev) => {
    for (const id of Object.keys(prev.byHost)) {
      if (!(id in next.byHost) || next.byHost[id].kind !== 'ok') { bump(id); reading.delete(id); useWatchEventsStore.getState().forget(id) }
    }
    first()
  })
  const unsubStore = useWatchEventsStore.subscribe((next) => {
    for (const id of [...reading]) if (id in next.byHost) reading.delete(id)
  })
  const unsubHosts = useHostStore.subscribe((next, prev) => {
    for (const id of Object.keys(prev.hosts)) {
      if (!next.hosts[id]) { bump(id); reading.delete(id); useWatchEventsStore.getState().forget(id) }
    }
    for (const id of Object.keys(next.hosts)) {
      if (prev.runtime[id]?.status === 'connected' && next.runtime[id]?.status !== 'connected') {
        bump(id); reading.delete(id); useWatchEventsStore.getState().forget(id)
      }
    }
  })
  first()

  return () => {
    unsubStatus(); unsubStore(); unsubHosts()
    rereader = null
  }
}
