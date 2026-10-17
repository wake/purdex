// spa/src/lib/watch/events-store.ts — each watcher host's events, and the count of those still waiting (plan W5e, spec §5).
//
// A host's events are read when its status first answers `ok` (its daemon carries the watch module, `watch.v1`) and again on
// each `watch.events` push (`rereadWatchEvents`, called from the host event socket). There is no timer. A reply is written
// only under the generation it began in: a disconnect, a removal, or a newer read moves it, so an answer of an old
// connection, or an older answer arriving after a newer one, is dropped. Nothing here is persisted.
import { create } from 'zustand'
import { useHostStore } from '../../stores/useHostStore'
import { connectionKey } from '../host-connection-key'
import { fetchWatchEvents, fetchWatchUnhandled } from './api'
import { isCount, type WatchEvent } from './events-types'
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
  /**
   * App host id → the number of events its daemon has not handled: `GET /api/watch/unhandled` on connect, and each
   * `watch.events` payload. The daemon's own count, because a list holds at most 500 rows and counting them would
   * undercount a longer backlog. A host that has not said counts none.
   */
  unhandled: Record<string, number>
  setUnhandled: (hostId: string, n: number) => void
  set: (hostId: string, entry: EventsEntry) => void
  forget: (hostId: string) => void
  reset: () => void
}

export const useWatchEventsStore = create<WatchEventsState>((set) => ({
  byHost: {},
  known: [],
  unhandled: {},
  know: (hostId) => set((s) => (s.known.includes(hostId) ? s : { known: [...s.known, hostId] })),
  setUnhandled: (hostId, n) => set((s) => (s.unhandled[hostId] === n ? s : { unhandled: { ...s.unhandled, [hostId]: n } })),
  set: (hostId, entry) => set((s) => ({ byHost: { ...s.byHost, [hostId]: entry } })),
  forget: (hostId) => set((s) => {
    if (!(hostId in s.byHost) && !(hostId in s.unhandled)) return s
    const { [hostId]: _gone, ...byHost } = s.byHost
    const { [hostId]: _count, ...unhandled } = s.unhandled
    return { byHost, unhandled }
  }),
  reset: () => set({ byHost: {}, known: [], unhandled: {} }),
}))

export const isPending = (e: WatchEvent): boolean => e.status === 'pending'

/** How many events wait for the watcher, over all hosts: the sum of what each daemon says. */
export function pendingCount(unhandled: Record<string, number>): number {
  let n = 0
  for (const c of Object.values(unhandled)) n += c
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

/** The running reader's way to set a host's count from a push (null while `startWatchEvents` is not running). */
let applier: ((hostId: string, n: number) => void) | null = null

/**
 * A `watch.events` payload's `unhandled`: that host's count, at once. Taken only for a connected host that carries the watch
 * module, and only a whole number >= 0. A count read still in flight is older than the push and is dropped.
 */
export function applyWatchUnhandled(hostId: string, n: number): void {
  applier?.(hostId, n)
}

/** Installed once from main.tsx. Returns how to stop it. */
export function startWatchEvents(): () => void {
  const gens = new Map<string, number>()
  // The count has its own generation: a push (newer than any read already asked) and a disconnect both move it.
  const cgens = new Map<string, number>()
  const cbump = (hostId: string): number => { const g = (cgens.get(hostId) ?? 0) + 1; cgens.set(hostId, g); return g }
  const bump = (hostId: string): number => { const g = (gens.get(hostId) ?? 0) + 1; gens.set(hostId, g); return g }
  const isConnected = (hostId: string): boolean => {
    const s = useHostStore.getState()
    return !!s.hosts[hostId] && s.runtime[hostId]?.status === 'connected'
  }
  // The connection a read was sent on: a re-point leaves the runtime `connected`, so write-back also compares the identity.
  const identityOf = (hostId: string): string | null => {
    const h = useHostStore.getState().hosts[hostId]
    return h ? connectionKey(h) : null
  }
  const carriesWatch = (hostId: string): boolean => useWatchStatusStore.getState().byHost[hostId]?.kind === 'ok'

  const read = (hostId: string): void => {
    const gen = bump(hostId)
    const identity = identityOf(hostId)
    void fetchWatchEvents(hostId).then((r) => {
      if (gens.get(hostId) !== gen || !isConnected(hostId) || identityOf(hostId) !== identity) return
      if (r.kind === 'unsupported') useWatchEventsStore.getState().forget(hostId)
      else useWatchEventsStore.getState().set(hostId, r.kind === 'ok' ? { kind: 'ok', events: r.events, at: Date.now() } : { kind: 'error', at: Date.now() })
    })
  }
  const readCount = (hostId: string): void => {
    const gen = cbump(hostId)
    const identity = identityOf(hostId)
    void fetchWatchUnhandled(hostId).then((r) => {
      if (cgens.get(hostId) !== gen || !isConnected(hostId) || identityOf(hostId) !== identity) return
      if (r.kind === 'ok') useWatchEventsStore.getState().setUnhandled(hostId, r.count)
    })
  }
  rereader = (hostId) => { if (isConnected(hostId) && carriesWatch(hostId)) read(hostId) }
  applier = (hostId, n) => {
    if (!isCount(n) || !isConnected(hostId) || !carriesWatch(hostId)) return
    cbump(hostId)
    useWatchEventsStore.getState().setUnhandled(hostId, n)
  }
  const dropCount = (hostId: string): void => { cbump(hostId) }

  // A host whose first read is under way: the status store may change again before it lands.
  const reading = new Set<string>()
  const first = (): void => {
    for (const [id, e] of Object.entries(useWatchStatusStore.getState().byHost)) {
      if (e.kind === 'ok' && isConnected(id) && !(id in useWatchEventsStore.getState().byHost) && !reading.has(id)) { reading.add(id); useWatchEventsStore.getState().know(id); read(id); readCount(id) }
    }
  }
  const unsubStatus = useWatchStatusStore.subscribe((next, prev) => {
    for (const id of Object.keys(prev.byHost)) {
      if (!(id in next.byHost) || next.byHost[id].kind !== 'ok') { bump(id); dropCount(id); reading.delete(id); useWatchEventsStore.getState().forget(id) }
    }
    first()
  })
  const unsubStore = useWatchEventsStore.subscribe((next) => {
    for (const id of [...reading]) if (id in next.byHost) reading.delete(id)
  })
  const unsubHosts = useHostStore.subscribe((next, prev) => {
    for (const id of Object.keys(prev.hosts)) {
      if (!next.hosts[id]) { bump(id); dropCount(id); reading.delete(id); useWatchEventsStore.getState().forget(id) }
    }
    for (const id of Object.keys(next.hosts)) {
      const gone = prev.runtime[id]?.status === 'connected' && next.runtime[id]?.status !== 'connected'
      const repointed = !gone && !!prev.hosts[id] && connectionKey(prev.hosts[id]) !== connectionKey(next.hosts[id])
      if (gone || repointed) {
        bump(id); dropCount(id); reading.delete(id); useWatchEventsStore.getState().forget(id)
      }
    }
  })
  first()

  return () => {
    unsubStatus(); unsubStore(); unsubHosts()
    rereader = null
    applier = null
  }
}
