// spa/src/lib/team/roster-forget.ts — a host that is removed, or re-pointed at another endpoint or token, forgets its
// team roster (plan PL-2b′): what the old daemon said about its teams is not the new one's. The new connection's
// snapshot fills it again. One module-level subscription for the app's lifetime, started from main.tsx; the triggers
// are unattended-support.ts's, and the WS branch's connection binding covers the frames still queued in between.
import { hostEndpoint, useHostStore, type HostConfig } from '../../stores/useHostStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'

const identity = (h: HostConfig): string => `${hostEndpoint(h)}:${h.token ?? ''}`

export function startRosterForget(): () => void {
  return useHostStore.subscribe((next, prev) => {
    if (next.hosts === prev.hosts) return
    const store = useTeamRosterStore.getState()
    for (const hostId of Object.keys(store.byHost)) {
      const now = next.hosts[hostId]
      const before = prev.hosts[hostId]
      if (!now || (before !== undefined && identity(before) !== identity(now))) store.forgetHost(hostId)
    }
  })
}
