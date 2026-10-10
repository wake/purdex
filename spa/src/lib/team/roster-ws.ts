// spa/src/lib/team/roster-ws.ts — the daemon's `team.roster` host event (plan PL-2b′; wire in roster.ts): a snapshot to
// each new subscriber, a changed after every change, each carrying the whole roster. Called from useMultiHostEventWs
// with the per-host closure's hostId. A malformed frame is dropped whole with one warning — the store keeps what it
// had rather than read a broken frame as "every team ended".
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useHostStore } from '../../stores/useHostStore'
import { parseRosterEvent, type TeamRoster } from './roster'
import { teamHostMap } from './team-state'
import { labelOf, seatHostOf, teamKeyOf } from './team-views'

interface SeatMark { hostId: string; title: string }

/** The seats of `team` this Mac can name a host for, by session id (a seat on a host it cannot name has no workbook to open). */
function seatsOf(team: TeamRoster, frameHostId: string, hostMap: Record<string, string>): Map<string, SeatMark> {
  const out = new Map<string, SeatMark>()
  for (const session of [team.lead, ...team.members]) {
    const { hostId } = seatHostOf(team, session, frameHostId, hostMap)
    if (hostId !== null) out.set(session.session_id, { hostId, title: labelOf(session) })
  }
  return out
}

/** A seat that was on the host's previous roster for a team the new frame still lists, and is not on it now, ended: record it once.
 *  Only a frame from the connected host gets here, so a lost connection (which sends no frame) never ends anyone; a team that
 *  ended whole is pruned (`forgetTeams`), not recorded. A seat that is on the roster again leaves the ended list. */
function recordEnded(hostId: string, before: readonly TeamRoster[] | undefined, after: readonly TeamRoster[]): void {
  if (!before) return
  const hostMap = teamHostMap(useHostStore.getState())
  const ui = useTeamUiStore.getState()
  const now = Date.now()
  for (const team of after) {
    const key = teamKeyOf(hostId, team.id)
    const live = seatsOf(team, hostId, hostMap)
    for (const [sessionId, mark] of live) ui.clearEndedSeat(key, mark.hostId, sessionId)
    const prev = before.find((t) => t.id === team.id)
    if (!prev) continue
    const gone = [...seatsOf(prev, hostId, hostMap)].filter(([sessionId]) => !live.has(sessionId))
    if (gone.length > 0) ui.recordEndedSeats(key, gone.map(([sessionId, m]) => ({ hostId: m.hostId, sessionId, title: m.title, endedAt: now })))
  }
}

export function handleRosterEvent(hostId: string, value: unknown): void {
  const ev = parseRosterEvent(value)
  if (typeof ev === 'string') {
    console.warn(`[roster-ws] ignoring frame: ${ev}`)
    return
  }
  const before = useTeamRosterStore.getState().byHost[hostId]
  useTeamRosterStore.getState().apply(hostId, ev.teams)
  recordEnded(hostId, before, ev.teams)
  // The frame is the whole roster: the arrangement of a team it does not list (it ended meanwhile) goes with it.
  useTeamUiStore.getState().forgetTeams(hostId, ev.teams.map((t) => teamKeyOf(hostId, t.id)))
}
