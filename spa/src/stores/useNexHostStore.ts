// spa/src/stores/useNexHostStore.ts — per-host cache of "is Nexen ready
// here?" (P-C spec §4.1): `/api/info.nex` plus `GET /v1/capabilities`, one
// truth shared by the Host → Nex page, the Headless NewTab section and the
// hand-off entry points. Not persisted, not synced. This file is the zustand
// shell, the selectors and the host-store watcher; the entry shape and
// transitions live in `lib/nex/nex-host-reducer.ts`, the fetching in
// `lib/nex/nex-host-effects.ts`.
import { create } from 'zustand'
import { createNexHostEffects, type NexHostEntries } from '../lib/nex/nex-host-effects'
import { hostFingerprint } from '../lib/nex/nex-host-reducer'
import { useHostStore } from './useHostStore'

export { NEX_HOST_TTL_MS, type NexHostEntry, type NexHostPhase } from '../lib/nex/nex-host-reducer'

interface NexHostState {
  byHost: NexHostEntries
  ensure: (hostId: string) => Promise<void>
  invalidate: (hostId: string) => Promise<void>
  clearHost: (hostId: string) => void
}

export const useNexHostStore = create<NexHostState>()((set, get) => ({
  byHost: {},
  ...createNexHostEffects({
    get: () => get().byHost,
    set: (update) =>
      set((s) => {
        const next = update(s.byHost)
        return next === s.byHost ? s : { byHost: next }
      }),
  }),
}))

export function selectReady(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => s.byHost[hostId]?.phase === 'ready'
}

export function selectHandoffReady(hostId: string): (s: Pick<NexHostState, 'byHost'>) => boolean {
  return (s) => {
    const entry = s.byHost[hostId]
    if (entry?.phase !== 'ready' || !entry.capabilities) return false
    return entry.capabilities.delegate?.resume_session_id === true
      && entry.capabilities.sandbox_profiles.includes('handoff')
  }
}

/**
 * Keep the cache honest against the host store. Same shape as
 * `startPeerCacheInvalidation`: one module-level subscription for the app's
 * lifetime, started from main.tsx; returns its unsubscribe. Two triggers:
 *
 * - a host's daemon comes (back) online → refetch its readiness. Only hosts
 *   someone has already asked about are refetched — a reconnect is not a
 *   reason to poll every daemon's Nexen state;
 * - a host's identity (`ip`, `port` or `token`) changes → drop its entry.
 *   The capabilities belonged to the old daemon (or the old credentials),
 *   so they are cleared rather than refetched; whoever needs the host next
 *   asks again with `ensure`. An in-flight request for the old identity is
 *   discarded at commit time by the fingerprint check.
 */
export function startNexHostInvalidation(): () => void {
  return useHostStore.subscribe((next, prev) => {
    if (next.hosts !== prev.hosts) {
      for (const hostId of Object.keys(useNexHostStore.getState().byHost)) {
        const before = prev.hosts[hostId]
        const after = next.hosts[hostId]
        if (before && after && hostFingerprint(before) !== hostFingerprint(after)) {
          useNexHostStore.getState().clearHost(hostId)
        }
      }
    }
    if (next.runtime === prev.runtime) return
    for (const hostId of Object.keys(next.runtime)) {
      const connected = next.runtime[hostId]?.status === 'connected'
      const wasConnected = prev.runtime[hostId]?.status === 'connected'
      if (connected && !wasConnected && useNexHostStore.getState().byHost[hostId]) {
        void useNexHostStore.getState().invalidate(hostId)
      }
    }
  })
}
