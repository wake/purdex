// spa/src/lib/team/roster-ws.ts — the daemon's `team.roster` host event (plan PL-2b′; wire in roster.ts): a snapshot to
// each new subscriber, a changed after every change, each carrying the whole roster. Called from useMultiHostEventWs
// with the per-host closure's hostId. A malformed frame is dropped whole with one warning — the store keeps what it
// had rather than read a broken frame as "every team ended".
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { parseRosterEvent } from './roster'

export function handleRosterEvent(hostId: string, value: unknown): void {
  const ev = parseRosterEvent(value)
  if (typeof ev === 'string') {
    console.warn(`[roster-ws] ignoring frame: ${ev}`)
    return
  }
  useTeamRosterStore.getState().apply(hostId, ev.teams)
}
