// spa/src/hooks/useActiveMemberKeys.ts — the active team members' `hostId:code` keys (lib/team/member-keys.ts) for React.
// Subscribes to the roster, the session lists and the host map, so a mounted tab / row / count follows a team change.
import { useMemo } from 'react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { activeMemberKeys } from '../lib/team/member-keys'
import { teamHostMap } from '../lib/team/team-state'

const SEP = '\n'

/**
 * The set keeps its identity while the membership is unchanged: it is rebuilt from a primitive (the sorted keys joined),
 * so a session-list refresh or a roster frame that changes no membership does not re-run what depends on it.
 */
export function useActiveMemberKeys(): ReadonlySet<string> {
  const rosterByHost = useTeamRosterStore((s) => s.byHost)
  const sessionsByHost = useSessionStore((s) => s.sessions)
  const hostMapKey = useHostStore((s) => JSON.stringify(teamHostMap(s)))
  const joined = useMemo(
    () => [...activeMemberKeys({ rosterByHost, sessionsByHost, hostIdByDaemonId: JSON.parse(hostMapKey) })].sort().join(SEP),
    [rosterByHost, sessionsByHost, hostMapKey],
  )
  return useMemo(() => new Set(joined ? joined.split(SEP) : []), [joined])
}
