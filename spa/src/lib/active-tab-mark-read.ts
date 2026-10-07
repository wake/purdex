// spa/src/lib/active-tab-mark-read.ts — auto-markRead when an agent comes on screen in the active tab.
// Lives outside active-session.ts to avoid a circular dependency between it and useAgentStore.
import { useTabStore } from '../stores/useTabStore'
import { useAgentStore } from '../stores/useAgentStore'
import { getActiveTabAgents } from './active-session'
import { compositeKey } from './composite-key'

/**
 * Subscribe to tab-store changes; whenever an agent key comes on screen — shown
 * by any pane of the active tab, primary or not (#1853; composite
 * `hostId:sessionCode`, so cross-host correct — a worker pane's key is
 * `exec-<id>`, worker-pane theme spec §8.2) — mark that key read. Only keys
 * that were not on screen before the change are marked: switching tabs, or
 * opening an agent in a pane of the active tab. Returns the unsubscribe.
 * Started once from main.tsx (app lifetime).
 */
export function startActiveTabMarkRead(): () => void {
  let prevKeys = new Set(getActiveTabAgents().map((a) => compositeKey(a.hostId, a.sessionCode)))
  return useTabStore.subscribe(() => {
    const agents = getActiveTabAgents()
    const keys = new Set(agents.map((a) => compositeKey(a.hostId, a.sessionCode)))
    const fresh = agents.filter((a) => !prevKeys.has(compositeKey(a.hostId, a.sessionCode)))
    prevKeys = keys
    for (const a of fresh) useAgentStore.getState().markRead(a.hostId, a.sessionCode)
  })
}
