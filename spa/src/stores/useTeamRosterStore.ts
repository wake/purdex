// spa/src/stores/useTeamRosterStore.ts — per host, the live teams its daemon reported (plan PL-2b′). Fed only by the
// `team.roster` WS branch (lib/team/roster-ws.ts); a snapshot and a changed both REPLACE the host's list, because the
// daemon always sends the whole roster. Never persisted, never synced: each renderer has its own sockets and every new
// connection replays a snapshot, so every window follows (the same shape as useUnattendedStore).
import { create } from 'zustand'
import type { TeamRoster } from '../lib/team/roster'

interface TeamRosterState {
  byHost: Record<string, TeamRoster[]>
  /**
   * Hosts whose `team.roster` snapshot arrived on the CURRENT connection (member spec R4): set by `apply` (only the WS
   * branch calls it), cleared by `dropConnection` when the socket closes or is replaced. `byHost` itself is kept across a
   * disconnect (beads / quotas read it); only the member set trusts nothing but `liveHosts`. It lives here, with the
   * data it qualifies, so one `set` changes both atomically and every reader subscribes to one store.
   */
  liveHosts: Record<string, true>
  /**
   * Hosts whose session list arrived on the CURRENT connection (member spec R4): a seat is mapped to a session only through
   * such a list, so a cross-host seat never claims a code from a list fetched before its host reconnected. Set by
   * `markSessionsLive` (the session store calls it when a list lands), cleared by `dropConnection` with `liveHosts`.
   */
  liveSessionHosts: Record<string, true>
  markSessionsLive: (hostId: string) => void
  apply: (hostId: string, teams: TeamRoster[]) => void
  /** The host's connection closed or is being replaced: its roster stays, but is no longer from a live connection. */
  dropConnection: (hostId: string) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

// Per host, how many times its connection has been dropped. Not state: nothing renders from it. An async session fetch
// remembers the value it started under and is discarded if it moved (member spec R4: a reply from a retired connection
// must not be marked live).
const connectionGenerations = new Map<string, number>()
export function connectionGeneration(hostId: string): number { return connectionGenerations.get(hostId) ?? 0 }

export const useTeamRosterStore = create<TeamRosterState>()((set) => ({
  byHost: {},
  liveHosts: {},
  liveSessionHosts: {},
  markSessionsLive: (hostId) => set((s) => (Object.hasOwn(s.liveSessionHosts, hostId) ? s : { liveSessionHosts: { ...s.liveSessionHosts, [hostId]: true } })),
  apply: (hostId, teams) => set((s) => ({ byHost: { ...s.byHost, [hostId]: teams }, liveHosts: { ...s.liveHosts, [hostId]: true } })),
  dropConnection: (hostId) => set((s) => {
    connectionGenerations.set(hostId, connectionGeneration(hostId) + 1)
    const rosterLive = Object.hasOwn(s.liveHosts, hostId)
    const sessionsLive = Object.hasOwn(s.liveSessionHosts, hostId)
    if (!rosterLive && !sessionsLive) return s
    const { [hostId]: _gone, ...rest } = s.liveHosts
    const { [hostId]: _sess, ...sessRest } = s.liveSessionHosts
    return { liveHosts: rosterLive ? rest : s.liveHosts, liveSessionHosts: sessionsLive ? sessRest : s.liveSessionHosts }
  }),
  forgetHost: (hostId) => set((s) => {
    if (!Object.hasOwn(s.byHost, hostId) && !Object.hasOwn(s.liveHosts, hostId) && !Object.hasOwn(s.liveSessionHosts, hostId)) return s
    const { [hostId]: _gone, ...rest } = s.byHost
    const { [hostId]: _live, ...liveRest } = s.liveHosts
    const { [hostId]: _sess, ...sessRest } = s.liveSessionHosts
    return { byHost: rest, liveHosts: liveRest, liveSessionHosts: sessRest }
  }),
  reset: () => set({ byHost: {}, liveHosts: {}, liveSessionHosts: {} }),
}))
