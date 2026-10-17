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
  apply: (hostId: string, teams: TeamRoster[]) => void
  /** The host's connection closed or is being replaced: its roster stays, but is no longer from a live connection. */
  dropConnection: (hostId: string) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

export const useTeamRosterStore = create<TeamRosterState>()((set) => ({
  byHost: {},
  liveHosts: {},
  apply: (hostId, teams) => set((s) => ({ byHost: { ...s.byHost, [hostId]: teams }, liveHosts: { ...s.liveHosts, [hostId]: true } })),
  dropConnection: (hostId) => set((s) => {
    if (!Object.hasOwn(s.liveHosts, hostId)) return s
    const { [hostId]: _gone, ...rest } = s.liveHosts
    return { liveHosts: rest }
  }),
  forgetHost: (hostId) => set((s) => {
    if (!Object.hasOwn(s.byHost, hostId) && !Object.hasOwn(s.liveHosts, hostId)) return s
    const { [hostId]: _gone, ...rest } = s.byHost
    const { [hostId]: _live, ...liveRest } = s.liveHosts
    return { byHost: rest, liveHosts: liveRest }
  }),
  reset: () => set({ byHost: {}, liveHosts: {} }),
}))
