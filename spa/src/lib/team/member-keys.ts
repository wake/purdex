// spa/src/lib/team/member-keys.ts — which sessions are active team members, as the SPA's `hostId:code` keys (member
// spec §3.2). A member's unread is hidden and never counted, and its waiting / idle never notify (R1-R3).
//
// A roster seat names its session by tmux session NAME; the key is the session-list row of the seat's host (`seatHostOf`,
// the same host rule as the team views) whose `name` is that. Whatever cannot be mapped is NOT a member (R4): the seat
// has no `tmux_session`, its host is not configured, or that host's session list lacks the name (not arrived, or gone).
// The lead is not in `members`, so it is never in the set. A roster counts only while its host's connection is the one
// it arrived on (`liveRosterHosts`, R4): across a reconnect a stale seat must not claim a new session that reuses the name.
import { useHostStore } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { transitionOf } from '../../components/team/seat-flags'
import { compositeKey } from '../composite-key'
import type { Session } from '../host-api'
import type { TeamRoster } from './roster'
import { teamHostMap } from './team-state'
import { seatHostOf } from './team-views'

export interface MemberKeysInput {
  rosterByHost: Record<string, TeamRoster[]>
  /** Hosts whose roster snapshot arrived on THEIR CURRENT connection (R4); a roster of any other host is stale and ignored. */
  liveRosterHosts: ReadonlySet<string>
  sessionsByHost: Record<string, ReadonlyArray<Pick<Session, 'code' | 'name'>>>
  /** Wire (daemon) id → SPA host id, for members of a cross-host team (see `teamHostMap`). Absent = none. */
  hostIdByDaemonId?: Record<string, string>
}

export function activeMemberKeys(input: MemberKeysInput): Set<string> {
  const keys = new Set<string>()
  for (const [leadHostId, teams] of Object.entries(input.rosterByHost)) {
    if (!input.liveRosterHosts.has(leadHostId)) continue
    for (const team of teams) {
      for (const m of team.members) {
        if (transitionOf(m) !== null || !m.tmux_session) continue // not joining / releasing / killing; an unknown state is active (roster.ts)
        const { hostId } = seatHostOf(team, m, leadHostId, input.hostIdByDaemonId)
        if (hostId === null) continue
        const hit = input.sessionsByHost[hostId]?.find((s) => s.name === m.tmux_session)
        if (hit) keys.add(compositeKey(hostId, hit.code))
      }
    }
  }
  return keys
}

/** The member set over the stores' CURRENT state, for code that runs at an event rather than in a render. */
export function currentMemberKeys(): Set<string> {
  return activeMemberKeys({
    rosterByHost: useTeamRosterStore.getState().byHost,
    liveRosterHosts: new Set(Object.keys(useTeamRosterStore.getState().liveHosts)),
    sessionsByHost: useSessionStore.getState().sessions,
    hostIdByDaemonId: teamHostMap(useHostStore.getState()),
  })
}
