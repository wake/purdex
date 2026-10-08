// spa/src/stores/useTeamRosterStore.ts — per host, the live teams its daemon reported (plan PL-2b′). Fed only by the
// `team.roster` WS branch (lib/team/roster-ws.ts); a snapshot and a changed both REPLACE the host's list, because the
// daemon always sends the whole roster. Never persisted, never synced: each renderer has its own sockets and every new
// connection replays a snapshot, so every window follows (the same shape as useUnattendedStore).
import { create } from 'zustand'
import type { TeamRoster } from '../lib/team/roster'

interface TeamRosterState {
  byHost: Record<string, TeamRoster[]>
  apply: (hostId: string, teams: TeamRoster[]) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

export const useTeamRosterStore = create<TeamRosterState>()((set) => ({
  byHost: {},
  apply: (hostId, teams) => set((s) => ({ byHost: { ...s.byHost, [hostId]: teams } })),
  forgetHost: (hostId) => set((s) => {
    if (!Object.hasOwn(s.byHost, hostId)) return s
    const { [hostId]: _gone, ...rest } = s.byHost
    return { byHost: rest }
  }),
  reset: () => set({ byHost: {} }),
}))
