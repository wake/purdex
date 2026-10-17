// spa/src/lib/team/member-keys.ts — which sessions are active team members, as the SPA's `hostId:code` keys (member
// spec §3.2). A member's unread is hidden and never counted, and its waiting / idle never notify (R1-R3).
//
// A roster seat names its session by tmux session NAME; the key is the session-list row of the seat's host (`seatHostOf`,
// the same host rule as the team views) whose `name` is that. Whatever cannot be mapped is NOT a member (R4): the seat
// has no `tmux_session`, its host is not configured, or that host's session list lacks the name (not arrived, or gone).
// The lead is not in `members`, so it is never in the set. A roster counts only while its host's connection is the one
// it arrived on (`liveRosterHosts`, R4), and a seat is mapped only through a session list that arrived on its host's current
// connection (`liveSessionHosts`, R4): across a reconnect a stale seat must not claim a new session that reuses the name.
import { transitionOf } from '../../components/team/seat-flags'
import { compositeKey } from '../composite-key'
import type { Session } from '../host-api'
import type { TeamRoster } from './roster'
import { seatHostOf } from './team-views'

export interface MemberKeysInput {
  rosterByHost: Record<string, TeamRoster[]>
  /** Hosts whose roster snapshot arrived on THEIR CURRENT connection (R4); a roster of any other host is stale and ignored. */
  liveRosterHosts: ReadonlySet<string>
  /** Hosts whose session list arrived on THEIR CURRENT connection (R4): a seat is mapped only through such a list. */
  liveSessionHosts: ReadonlySet<string>
  sessionsByHost: Record<string, ReadonlyArray<Pick<Session, 'code' | 'name'>>>
  /** Wire (daemon) id → SPA host id, for members of a cross-host team (see `teamHostMap`). Absent = none. */
  hostIdByDaemonId?: Record<string, string>
}

export function activeMemberKeys(input: MemberKeysInput): Set<string> {
  const keys = new Set<string>()
  // One name → code index per host (the first row of a name wins, as a linear find would), built on first use.
  const index = new Map<string, Map<string, string>>()
  const codeByName = (hostId: string): Map<string, string> => {
    let m = index.get(hostId)
    if (!m) {
      m = new Map()
      for (const s of input.sessionsByHost[hostId] ?? []) if (!m.has(s.name)) m.set(s.name, s.code)
      index.set(hostId, m)
    }
    return m
  }
  for (const [leadHostId, teams] of Object.entries(input.rosterByHost)) {
    if (!input.liveRosterHosts.has(leadHostId)) continue
    for (const team of teams) {
      for (const m of team.members) {
        if (transitionOf(m) !== null || !m.tmux_session) continue // not joining / releasing / killing; an unknown state is active (roster.ts)
        const { hostId } = seatHostOf(team, m, leadHostId, input.hostIdByDaemonId)
        if (hostId === null || !input.liveSessionHosts.has(hostId)) continue // the seat host's list must be this connection's too (R4)
        const code = codeByName(hostId).get(m.tmux_session)
        if (code !== undefined) keys.add(compositeKey(hostId, code))
      }
    }
  }
  return keys
}
