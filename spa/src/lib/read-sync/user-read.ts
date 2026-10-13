// spa/src/lib/read-sync/user-read.ts — a USER read: the person made the session visible or clicked its notification.
//
// Only this kind of read is told to the daemon (app-badge spec §7.3). Marking read is the store's `markRead`; what this
// adds is the report, with the cursor `markRead` captured at the unread -> read transition. A key that was already
// read is not a transition, so it sends nothing. NOT a user read, and so not reported: `running`, `clear`, clearing by
// host prefix, and a read received from the daemon (`applyReads`).
import { useAgentStore } from '../../stores/useAgentStore'
import { reportRead } from './reporter'

export function markReadByUser(hostId: string, sessionCode: string): void {
  const t = useAgentStore.getState().markRead(hostId, sessionCode)
  if (t?.wasUnread) reportRead(hostId, sessionCode, t.cursor)
}
