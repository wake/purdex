// spa/src/lib/fleet/store.ts — the App's picture of each daemon host's fleet (fleet spec §6; plan T-2a.2). Never
// persisted: the daemon pushes `fleet.changed` and the App never polls.
//
// Fleet generation: every host has a counter that moves when its connection goes away, when it is re-pointed and when it
// is removed (`fence` / `forgetHost`), and when a new connection begins (`begin`). Everything that arrives later and
// carries the old counter — the initial GET's reply, a probe-host list reply — is dropped, and a pushed event is
// written only while this connection's capability answer has confirmed `fleet.v1`. So a reply from the daemon the App
// no longer talks to can never put numbers on screen.
import { create } from 'zustand'
import { fetchProbeHosts } from './api'
import { forgetProbeDraft } from './probe-draft-memory'
import type { FleetProbe, FleetSelf, FleetPayload, VersionedProbeHosts } from './types'

export type FleetSupport = 'unknown' | 'yes' | 'no'

export interface FleetEntry {
  support: FleetSupport
  self: FleetSelf | null
  probes: FleetProbe[]
  /** When the numbers last landed (ms), null = none held. */
  updatedAt: number | null
  /** When the connection went away (ms); null while connected or never lost. Its numbers are gone, not "last known". */
  offlineSince: number | null
  /** A pushed event has been written under this generation: an older GET reply must not overwrite it. */
  pushed: boolean
}

export interface FocusProbe { proberHostId: string; probeId: string }

const EMPTY_ENTRY: FleetEntry = Object.freeze({
  support: 'unknown', self: null, probes: [], updatedAt: null, offlineSince: null, pushed: false,
}) as FleetEntry

interface FleetState {
  byHost: Record<string, FleetEntry>
  /** Per host: the probe-only host definitions of its hostconfig (`null` = not loaded / the daemon has none). */
  defs: Record<string, VersionedProbeHosts | null>
  gens: Record<string, number>
  /** Not persisted: T-2b's row click names the probe row the Hosts page should scroll to. */
  focusProbe: FocusProbe | null

  genOf: (hostId: string) => number
  /** A new connection to the host: moves the generation, starts from nothing; returns the new generation. */
  begin: (hostId: string) => number
  /** The connection is gone: moves the generation and drops the numbers (they are not live any more). */
  fence: (hostId: string) => void
  /** Re-pointed or removed: moves the generation and forgets everything of the old daemon. */
  forgetHost: (hostId: string) => void
  setSupport: (hostId: string, gen: number, support: FleetSupport) => void
  /** The initial GET's reply. Dropped under a stale generation, or when a pushed event already landed. */
  applyFetched: (hostId: string, gen: number, payload: FleetPayload) => void
  /** A `fleet.changed` frame. Written only while this generation has `fleet.v1` confirmed. */
  applyEvent: (hostId: string, payload: FleetPayload) => void
  applyDefs: (hostId: string, gen: number, defs: VersionedProbeHosts | null) => void
  loadDefs: (hostId: string) => Promise<void>
  setFocusProbe: (focus: FocusProbe | null) => void
  reset: () => void
}

const bump = (gens: Record<string, number>, hostId: string): Record<string, number> => ({ ...gens, [hostId]: (gens[hostId] ?? 0) + 1 })

function withoutKey<T>(rec: Record<string, T>, key: string): Record<string, T> {
  const { [key]: _gone, ...rest } = rec
  return rest
}

export const useFleetStore = create<FleetState>()((set, get) => ({
  byHost: {},
  defs: {},
  gens: {},
  focusProbe: null,

  genOf: (hostId) => get().gens[hostId] ?? 0,

  begin: (hostId) => {
    set((s) => ({ gens: bump(s.gens, hostId), byHost: { ...s.byHost, [hostId]: { ...EMPTY_ENTRY } }, defs: withoutKey(s.defs, hostId) }))
    return get().gens[hostId]
  },

  fence: (hostId) => set((s) => ({
    gens: bump(s.gens, hostId),
    byHost: { ...s.byHost, [hostId]: { ...EMPTY_ENTRY, offlineSince: Date.now() } },
    defs: withoutKey(s.defs, hostId),
  })),

  forgetHost: (hostId) => {
    forgetProbeDraft(hostId) // its SSH host was picked from the old daemon's list
    set((s) => ({
      gens: bump(s.gens, hostId),
      byHost: withoutKey(s.byHost, hostId),
      defs: withoutKey(s.defs, hostId),
      focusProbe: s.focusProbe?.proberHostId === hostId ? null : s.focusProbe,
    }))
  },

  setSupport: (hostId, gen, support) => set((s) => {
    if ((s.gens[hostId] ?? 0) !== gen) return s
    return { byHost: { ...s.byHost, [hostId]: { ...(s.byHost[hostId] ?? EMPTY_ENTRY), support } } }
  }),

  applyFetched: (hostId, gen, payload) => set((s) => {
    const cur = s.byHost[hostId]
    if ((s.gens[hostId] ?? 0) !== gen || !cur || cur.support !== 'yes' || cur.pushed) return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, self: payload.self, probes: payload.probes, updatedAt: Date.now() } } }
  }),

  applyEvent: (hostId, payload) => set((s) => {
    const cur = s.byHost[hostId]
    if (!cur || cur.support !== 'yes') return s
    return { byHost: { ...s.byHost, [hostId]: { ...cur, self: payload.self, probes: payload.probes, updatedAt: Date.now(), pushed: true } } }
  }),

  applyDefs: (hostId, gen, defs) => set((s) => ((s.gens[hostId] ?? 0) !== gen ? s : { defs: { ...s.defs, [hostId]: defs } })),

  loadDefs: async (hostId) => {
    const gen = get().genOf(hostId)
    try {
      get().applyDefs(hostId, gen, await fetchProbeHosts(hostId))
    } catch {
      /* the section shows what it has; a failed reload leaves the list as it was */
    }
  },

  setFocusProbe: (focusProbe) => set({ focusProbe }),

  reset: () => set({ byHost: {}, defs: {}, gens: {}, focusProbe: null }),
}))

export function useFleetEntry(hostId: string): FleetEntry {
  return useFleetStore((s) => s.byHost[hostId]) ?? EMPTY_ENTRY
}
