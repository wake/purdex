import { useTabStore } from '../stores/useTabStore'
import { useAgentStore } from '../stores/useAgentStore'
import { getPrimaryPane } from './pane-tree'

/** Derive the active session code from the current active tab.
 *  Returns null if no tab is active or the active tab is not a session. */
export function getActiveSessionCode(): string | null {
  const { activeTabId, tabs } = useTabStore.getState()
  if (!activeTabId) return null
  const tab = tabs[activeTabId]
  if (!tab) return null
  const primary = getPrimaryPane(tab.layout)
  return primary.content.kind === 'session' ? primary.content.sessionCode : null
}

/** Cross-store subscription: auto-markRead when the active tab changes to a session.
 *  Call once at app init. Returns unsubscribe function. */
export function subscribeActiveTabMarkRead(): () => void {
  let prev = getActiveSessionCode()
  return useTabStore.subscribe(() => {
    const current = getActiveSessionCode()
    if (current !== prev) {
      prev = current
      if (current !== null) {
        useAgentStore.getState().markRead(current)
      }
    }
  })
}
