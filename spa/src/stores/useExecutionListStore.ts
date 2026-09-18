// spa/src/stores/useExecutionListStore.ts — per-host execution list shared
// by the Host → Nex table and the sidebar Executions view (P-C spec §4.3).
// Keeps the rows fresh through ONE site-wide Nex SSE per host, refcounted by
// subscribers; the stream is a refresh signal only (nexen/api/sse.go:118,195)
// — frame contents are never applied, only its durable cursor is kept so a
// reconnect does not replay history from seq 0. This file is the zustand
// shell and the two watchers; the connection lifecycle and commit guards
// live in `lib/nex/execution-list-effects.ts`.
import { create } from 'zustand'
import { createExecutionListEffects, type HostListCaches } from '../lib/nex/execution-list-effects'
import { hostFingerprint } from '../lib/nex/nex-host-reducer'
import { isNexReady } from '../components/hosts/nex/nex-ready'
import { useHostStore } from './useHostStore'
import { useNexHostStore } from './useNexHostStore'

export { LIST_REFRESH_DEBOUNCE_MS, type HostListCache, type HostListPhase } from '../lib/nex/execution-list-effects'

interface ExecutionListState {
  byHost: HostListCaches
  /** Refcounted; the returned unsubscribe is idempotent for its own token. */
  subscribe: (hostId: string) => () => void
  /** Re-query the list now; reopens the stream if it was terminally closed. No-op without subscribers. */
  refetch: (hostId: string) => void
  /** Host removed: close, drop rows and cursor, forget the entry. Subscriber tokens survive for an undo. */
  clearHost: (hostId: string) => void
}

const effects = createExecutionListEffects({
  get: () => useExecutionListStore.getState().byHost,
  set: (update) =>
    useExecutionListStore.setState((s) => {
      const next = update(s.byHost)
      return next === s.byHost ? s : { byHost: next }
    }),
})

export const useExecutionListStore = create<ExecutionListState>()(() => ({
  byHost: {},
  subscribe: (hostId) => effects.subscribe(hostId),
  refetch: (hostId) => effects.refetch(hostId),
  clearHost: (hostId) => effects.clearHost(hostId),
}))

/**
 * Keep the streams honest against the two stores they depend on. One
 * module-level subscription pair for the app's lifetime, started from
 * main.tsx next to `startNexHostInvalidation`; returns its unsubscribe.
 *
 * - Nex info readiness for a subscribed host flips true → open (the first
 *   subscribe may have found the host not ready yet); flips false → close
 *   but keep the rows, so a daemon blip does not blank the list.
 * - A host's identity (`ip`, `port` or `token`) changes → close and drop
 *   the rows and cursor: they belonged to the old daemon. Readiness of the
 *   new daemon reopens for the surviving subscribers — and since the
 *   nex-host watcher (registered before this one in main.tsx) has just
 *   dropped that host's entry, nobody else would ask the new daemon, so
 *   `ensure` is called here for any host that still has a subscriber.
 */
export function startExecutionListInvalidation(): () => void {
  const stopNex = useNexHostStore.subscribe((next, prev) => {
    if (next.byHost === prev.byHost) return
    for (const hostId of effects.subscribedHosts()) {
      const was = isNexReady(prev.byHost[hostId]?.info)
      const now = isNexReady(next.byHost[hostId]?.info)
      if (now && !was) effects.open(hostId)
      else if (was && !now) effects.close(hostId, { dropCache: false })
    }
  })
  const stopHosts = useHostStore.subscribe((next, prev) => {
    if (next.hosts === prev.hosts) return
    for (const hostId of Object.keys(useExecutionListStore.getState().byHost)) {
      const before = prev.hosts[hostId]
      const after = next.hosts[hostId]
      if (before && after && hostFingerprint(before) !== hostFingerprint(after)) {
        effects.close(hostId, { dropCache: true })
        if (effects.subscribedHosts().includes(hostId)) void useNexHostStore.getState().ensure(hostId)
      }
    }
  })
  return () => {
    stopNex()
    stopHosts()
  }
}

/** Test seam: forget every runtime (handles and reservations are NOT released — pair with `subscriptionSlots.resetForTests`). */
export function resetExecutionListForTests(): void {
  effects.resetForTests()
}
