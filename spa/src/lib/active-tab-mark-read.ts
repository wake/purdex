// spa/src/lib/active-tab-mark-read.ts — auto-markRead when the active tab changes to a session or a worker.
// Lives outside active-session.ts to avoid a circular dependency between it and useAgentStore.
import { useTabStore } from '../stores/useTabStore'
import { useAgentStore } from '../stores/useAgentStore'
import { getActiveSessionInfo } from './active-session'
import { compositeKey } from './composite-key'

/**
 * Subscribe to tab-store changes; whenever the active agent key changes
 * (composite `hostId:sessionCode`, so cross-host correct — a worker tab's key
 * is `exec-<id>`, worker-pane theme spec §8.2), mark that key read. Returns
 * the unsubscribe. Started once from main.tsx (app lifetime).
 */
export function startActiveTabMarkRead(): () => void {
  const keyOf = (info: { hostId: string; sessionCode: string } | null) =>
    info ? compositeKey(info.hostId, info.sessionCode) : null
  let prevKey = keyOf(getActiveSessionInfo())
  return useTabStore.subscribe(() => {
    const info = getActiveSessionInfo()
    const key = keyOf(info)
    if (key === prevKey) return
    prevKey = key
    if (info) useAgentStore.getState().markRead(info.hostId, info.sessionCode)
  })
}
