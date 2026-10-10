// spa/src/lib/workbook/workbook-loader.ts — "a seat's first appearance in the team view" (plan WA-1.3 (a)): every seat the
// team rosters show gets `loadSeat` (limit 1) — the store makes that once per seat per connection generation (b) and
// only on a `workbook.v1` host. The signal is the roster itself, not a component mount: a roster change, a support answer
// (a new generation, or the first one) and a host-list change (the daemon-id map) each re-offer every seat, and the
// store's own bookkeeping turns the repeats into nothing. One module-level subscription for the app's lifetime,
// started from main.tsx. Events are not a trigger.
import { useHostStore } from '../../stores/useHostStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { WORKBOOK_MAX_RETRIES, workbookRetryDelay } from './retry'
import type { TeamRoster } from '../team/roster'
import { teamHostMap } from '../team/team-state'

export interface SeatTarget { hostId: string; sessionId: string }

/** Every seat (lead and active members) of every roster, on the SPA host its session lives on. A remote member lives on
 *  the host its daemon id maps to (none: this Mac lacks that host, nothing to ask); an untrusted `host_id` names no host. */
export function seatTargets(rosterByHost: Record<string, TeamRoster[]>, hostState: Pick<ReturnType<typeof useHostStore.getState>, 'hosts' | 'runtime'>): SeatTarget[] {
  const { hosts } = hostState
  const byDaemon = teamHostMap(hostState) // only a daemon id verified at its host's endpoint maps: a stale or mismatched one would send a credential to the wrong daemon
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
  /** A seat whose ask got no answer (network / 5xx): its retry count and the timer of the one pending retry (#2435). Bounded by
   *  the roster: an entry is dropped when the seat leaves it, the connection generation moves on, or the loader stops. */
  const retries = new Map<string, { gen: number; tries: number; seq: number; exhausted?: boolean; timer?: ReturnType<typeof setTimeout> }>()
  const drop = (key: string) => { const e = retries.get(key); if (e?.timer !== undefined) clearTimeout(e.timer); retries.delete(key) }
  const keyOf = (t: SeatTarget) => `${t.hostId}\u0000${t.sessionId}`
  const ask = (t: SeatTarget) => {
    const key = keyOf(t)
    const gen = useWorkbookStore.getState().gens[t.hostId] ?? 0
    let e = retries.get(key)
    if (!e || e.gen !== gen) { drop(key); e = { gen, tries: 0, seq: 0 }; retries.set(key, e) }
    if (e.exhausted) return // the 3 retries are spent: no ask until the seat leaves the roster or the generation moves on (both drop this entry)
    if (e.timer !== undefined) return // the backoff is running: a roster frame does not cut it short (the store itself dedupes asks in one generation)
    const mine = e
    const n = ++mine.seq
    void useWorkbookStore.getState().loadSeat(t.hostId, t.sessionId).then((res) => {
      // Only the newest ask speaks: an older one (an 'ok' of a host whose support was not known yet) must not erase a newer failure.
      if (retries.get(key) !== mine || n !== mine.seq) return // the seat left, the generation moved on, or the loader stopped
      if (res !== 'failed') { retries.delete(key); return }
      if (mine.timer !== undefined) return
      if (mine.tries >= WORKBOOK_MAX_RETRIES) { mine.exhausted = true; return }
      mine.timer = setTimeout(() => { mine.timer = undefined; if (retries.get(key) === mine) ask(t) }, workbookRetryDelay(mine.tries++))
    })
  }
  const sync = () => {
    const targets = seatTargets(useTeamRosterStore.getState().byHost, useHostStore.getState())
    useWorkbookStore.getState().syncSeats(targets) // a seat that left the rosters stops holding its conversation
    const now = new Map(targets.map((t) => [keyOf(t), t]))
    for (const [key, e] of retries) {
      const t = now.get(key)
      if (!t || e.gen !== (useWorkbookStore.getState().gens[t.hostId] ?? 0)) drop(key)
    }
    for (const t of targets) ask(t)
  }
  const stops = [
    useTeamRosterStore.subscribe((n, p) => { if (n.byHost !== p.byHost) sync() }),
    useWorkbookStore.subscribe((n, p) => { if (n.support !== p.support) sync() }),
    useHostStore.subscribe((n, p) => { if (n.hosts !== p.hosts || n.runtime !== p.runtime) sync() }), // the daemon-id verification lands in runtime
  ]
  sync()
  return () => { stops.forEach((f) => f()); for (const key of [...retries.keys()]) drop(key) }
}
