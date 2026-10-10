// spa/src/lib/team/roster-forget.ts — a host that is removed, or re-pointed at another endpoint or token, forgets its
// team roster (plan PL-2b′): what the old daemon said about its teams is not the new one's. The new connection's
// snapshot fills it again. "Re-pointed" is the WS connection's own notion (`connectionKey`, a JSON tuple — a joined
// string can collide for IPv6 hosts and tokens with colons). One module-level subscription for the app's lifetime,
// started from main.tsx; the triggers are unattended-support.ts's, and the WS branch's connection binding covers the
// frames still queued in between.
import { useHostStore } from '../../stores/useHostStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { connectionKey } from '../host-connection-key'

export function startRosterForget(): () => void {
  return useHostStore.subscribe((next, prev) => {
    if (next.hosts === prev.hosts) return
    const store = useTeamRosterStore.getState()
    for (const hostId of Object.keys(store.byHost)) {
      const now = next.hosts[hostId]
      const before = prev.hosts[hostId]
      if (!now || (before !== undefined && connectionKey(before) !== connectionKey(now))) store.forgetHost(hostId)
    }
    // The workbook entries, statuses and support the old daemon gave are not the new one's either (WA-1.3 (d)).
    for (const hostId of Object.keys(prev.hosts)) {
      const now = next.hosts[hostId]
      if (!now || connectionKey(prev.hosts[hostId]) !== connectionKey(now)) useWorkbookStore.getState().forgetHost(hostId)
    }
  })
}
