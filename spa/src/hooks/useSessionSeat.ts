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
      // A member on another host lives in that host's tmux; one whose host_id the parser rejected (`host_untrusted`) may be anywhere — it is never matched here.
      if ((s.host_id ?? '') !== '' || s.host_untrusted === true) continue
      if (s.tmux_session === tmuxName && sameGeneration(instance, s.tmux_instance ?? '')) return s.address
    }
  }
  return null
}

/**
 * `paneInstance` is the tmux generation the pane itself was bound to (`Pane.content.tmuxInstance`). A code is tmux's `$N` re-encoded and is handed out again after a restart, so a pane of an older
 * generation than the session now carrying its code is a stale pane (reconciliation has not marked it terminated yet): it is not that session's face. Only a KNOWN difference rules it out —
 * a pane that cannot say (`''`) follows the session, as usePeerInfo's callers do.
 */
// A pane's host id is data from a stored tab: a name that is a member of Object.prototype (`toString`) is no host, and its lookup must not reach the member.
export function useSessionSeat(hostId: string, sessionCode: string | undefined, paneInstance = ''): Seat | null {
  const session = useSessionStore((s) => (hostId && sessionCode ? (Object.hasOwn(s.sessions, hostId) ? s.sessions[hostId] : undefined)?.find((x) => x.code === sessionCode) : undefined))
  const name = session?.name ?? ''
  const instance = session?.tmux_instance ?? ''
  const stale = paneInstance !== '' && instance !== '' && paneInstance !== instance
  const fromRoster = useTeamRosterStore((s) => (stale ? null : rosterAddress(Object.hasOwn(s.byHost, hostId) ? s.byHost[hostId] : undefined, name, instance)))
  const fromPeers = usePeerStore((s) => {
    const row = hostId && sessionCode && !stale ? (Object.hasOwn(s.byHost, hostId) ? s.byHost[hostId] : undefined)?.rows[sessionCode] : undefined
    return row && sameGeneration(instance, row.tmuxInstance) ? row.address : null
  })
  return useSeat(fromRoster ?? fromPeers ?? '')
}
