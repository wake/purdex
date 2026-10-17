// spa/src/stores/useRelayFreshStore.ts — per host, the sessions whose handoff file is written (relay v2 contract 5,
// `relay.half.v1`). Fed by the `relay.fresh` WS branch (lib/relay/fresh-ws.ts) and by `GET /api/relay/fresh`
// (lib/relay/fresh-sync.ts). A snapshot, a changed and a GET answer all REPLACE the host's list: the daemon always sends the
// whole list, so merging would keep a session that was cleared. Never persisted, never synced (the same shape as
// useTeamRosterStore).
//
// Which is newer, an event or a GET answer: every host has a generation. `begin` hands one out for a GET; an event
// (`apply`) and `forgetHost` move it, so an answer that arrives after either is dropped — the event, which the daemon
// sent after the state the GET read, wins. The generation is kept after a host is forgotten, so a re-added host never
// restarts at a number an old answer holds.
import { create } from 'zustand'
import type { RelayFresh } from '../lib/relay/fresh'

interface RelayFreshState {
  byHost: Record<string, RelayFresh[]>
  gens: Record<string, number>
  /** A GET is about to be sent for the host: its generation, to hand back to `applyFetched`. */
  begin: (hostId: string) => number
  /** An event's list: replaces the host's, and outdates every GET begun before. */
  apply: (hostId: string, list: RelayFresh[]) => void
  /** A GET answer: replaces the host's list only while `gen` is still the host's generation. */
  applyFetched: (hostId: string, gen: number, list: RelayFresh[]) => boolean
  forgetHost: (hostId: string) => void
  reset: () => void
}

const bump = (gens: Record<string, number>, hostId: string): Record<string, number> => ({ ...gens, [hostId]: (gens[hostId] ?? 0) + 1 })

export const useRelayFreshStore = create<RelayFreshState>()((set, get) => ({
  byHost: {},
  gens: {},
  begin: (hostId) => {
    const gen = (get().gens[hostId] ?? 0) + 1
    set((s) => ({ gens: { ...s.gens, [hostId]: gen } }))
    return gen
  },
  apply: (hostId, list) => set((s) => ({ byHost: { ...s.byHost, [hostId]: list }, gens: bump(s.gens, hostId) })),
  applyFetched: (hostId, gen, list) => {
    if (get().gens[hostId] !== gen) return false
    set((s) => ({ byHost: { ...s.byHost, [hostId]: list } }))
    return true
  },
  forgetHost: (hostId) => set((s) => {
    const gens = bump(s.gens, hostId)
    if (!Object.hasOwn(s.byHost, hostId)) return { gens }
    const { [hostId]: _gone, ...rest } = s.byHost
    return { byHost: rest, gens }
  }),
  reset: () => set({ byHost: {}, gens: {} }),
}))
