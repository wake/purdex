// spa/src/hooks/useSessionSeat.ts — who a sidebar session row is (name pool v2 spec §9.6, NA-5b): the persona behind a (host, session code), or null (the row then keeps its agent icon).
//
// It reads only what the App already holds and never asks for anything — no /api/peers (that call costs ~2 s; usePeerInfo decides when it is made), no roster fetch:
//   1. the team roster's seat of that host (the lead and the members that live on the host themselves), matched by tmux session name AND tmux generation;
//   2. the host's cached /api/peers row for the session code, only when its generation is the session's own (usePeerInfo's `sameGeneration`: an unknown on either side is never a match —
//      a wrong address would put another conversation's face on the row).
// The roster wins when both know. The address then goes through `useSeat` (the code it ends in), so a store update — a peers answer that lands, a roster frame, a persona import — changes the row.
import { useSessionStore } from '../stores/useSessionStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { usePeerStore } from '../stores/usePeerStore'
import { useSeat, type Seat } from '../lib/names/seat'
import type { TeamRoster } from '../lib/team/roster'
import { sameGeneration } from './usePeerInfo'

/** The address of the roster seat that is this tmux session of this host, or null. A member on another host (`host_id` set) lives in another host's tmux: not matched by name here. */
function rosterAddress(teams: readonly TeamRoster[] | undefined, tmuxName: string, instance: string): string | null {
  if (!teams || tmuxName === '') return null
  for (const team of teams) {
    for (const s of [team.lead, ...team.members]) {
      if ((s.host_id ?? '') !== '') continue
      if (s.tmux_session === tmuxName && sameGeneration(instance, s.tmux_instance ?? '')) return s.address
    }
  }
  return null
}

export function useSessionSeat(hostId: string, sessionCode: string | undefined): Seat | null {
  const session = useSessionStore((s) => (hostId && sessionCode ? s.sessions[hostId]?.find((x) => x.code === sessionCode) : undefined))
  const name = session?.name ?? ''
  const instance = session?.tmux_instance ?? ''
  const fromRoster = useTeamRosterStore((s) => rosterAddress(s.byHost[hostId], name, instance))
  const fromPeers = usePeerStore((s) => {
    const row = hostId && sessionCode ? s.byHost[hostId]?.rows[sessionCode] : undefined
    return row && sameGeneration(instance, row.tmuxInstance) ? row.address : null
  })
  return useSeat(fromRoster ?? fromPeers ?? '')
}
