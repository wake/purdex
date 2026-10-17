// spa/src/lib/team/member-set.ts — the App's ONE member set (member spec §3.2, MQ-2 review A-2). A small derived store
// subscribes once to the roster, session and host stores, joins them with `activeMemberKeys`, and keeps the same Set
// instance while the membership is unchanged. Every consumer (rows, beads, tab aggregates, workspace numbers, the Dock
// reporter, the notification path) reads this snapshot; none re-runs the join.
import { create } from 'zustand'
import { useHostStore } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { activeMemberKeys } from './member-keys'
import { teamHostMap } from './team-state'

interface MemberSetState { keys: ReadonlySet<string> }

export const useMemberSetStore = create<MemberSetState>()(() => ({ keys: new Set<string>() }))

let lastKey = ''
let last: { roster: object; live: object; sessions: object; hostMap: string } | null = null

function recompute(): void {
  const roster = useTeamRosterStore.getState()
  const sessions = useSessionStore.getState().sessions
  const hostMap = JSON.stringify(teamHostMap(useHostStore.getState()))
  if (last && last.roster === roster.byHost && last.live === roster.liveHosts && last.sessions === sessions && last.hostMap === hostMap) return
  last = { roster: roster.byHost, live: roster.liveHosts, sessions, hostMap }
  const next = activeMemberKeys({
    rosterByHost: roster.byHost,
    liveRosterHosts: new Set(Object.keys(roster.liveHosts)),
    sessionsByHost: sessions,
    hostIdByDaemonId: JSON.parse(hostMap),
  })
  const key = [...next].sort().join('\n')
  if (key === lastKey) return // same membership: keep the instance
  lastKey = key
  useMemberSetStore.setState({ keys: next })
}

useTeamRosterStore.subscribe(recompute)
useSessionStore.subscribe(recompute)
useHostStore.subscribe(recompute)
recompute()

/** The member set over the CURRENT state, for code that runs at an event rather than in a render. */
export function currentMemberKeys(): ReadonlySet<string> {
  recompute() // the subscriptions keep it fresh; this also covers a store replaced wholesale
  return useMemberSetStore.getState().keys
}
