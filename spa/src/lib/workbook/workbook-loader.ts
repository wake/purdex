// spa/src/lib/workbook/workbook-loader.ts — "a seat's first appearance in the team view" (plan WA-1.3 (a)): every seat the
// team rosters show gets `loadSeat` (limit 1) — the store makes that once per seat per connection generation (b) and
// only on a `workbook.v1` host. The signal is the roster itself, not a component mount: a roster change, a support answer
// (a new generation, or the first one) and a host-list change (the daemon-id map) each re-offer every seat, and the
// store's own bookkeeping turns the repeats into nothing. One module-level subscription for the app's lifetime,
// started from main.tsx. Events are not a trigger.
import { useHostStore } from '../../stores/useHostStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import type { TeamRoster } from '../team/roster'
import { daemonIdMap } from '../team/team-views'

export interface SeatTarget { hostId: string; sessionId: string }

/** Every seat (lead and active members) of every roster, on the SPA host its session lives on. A remote member lives on
 *  the host its daemon id maps to (none: this Mac lacks that host, nothing to ask); an untrusted `host_id` names no host. */
export function seatTargets(rosterByHost: Record<string, TeamRoster[]>, hosts: Record<string, { daemonId?: string }>): SeatTarget[] {
  const byDaemon = daemonIdMap(hosts)
  const out = new Map<string, SeatTarget>()
  for (const [leadHost, teams] of Object.entries(rosterByHost)) {
    for (const team of teams) {
      for (const s of [team.lead, ...team.members]) {
        if (!s.session_id || s.host_untrusted) continue
        const remote = !!s.host_id && s.host_id !== team.host_id
        const hostId = remote ? byDaemon[s.host_id!] : leadHost
        if (hostId && Object.hasOwn(hosts, hostId)) out.set(`${hostId}\u0000${s.session_id}`, { hostId, sessionId: s.session_id })
      }
    }
  }
  return [...out.values()]
}

export function startWorkbookLoader(): () => void {
  const sync = () => {
    for (const t of seatTargets(useTeamRosterStore.getState().byHost, useHostStore.getState().hosts)) {
      void useWorkbookStore.getState().loadSeat(t.hostId, t.sessionId)
    }
  }
  const stops = [
    useTeamRosterStore.subscribe((n, p) => { if (n.byHost !== p.byHost) sync() }),
    useWorkbookStore.subscribe((n, p) => { if (n.support !== p.support) sync() }),
    useHostStore.subscribe((n, p) => { if (n.hosts !== p.hosts) sync() }),
  ]
  sync()
  return () => stops.forEach((f) => f())
}
