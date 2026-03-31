import { useTabStore } from '../stores/useTabStore'
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

/** Derive both hostId and sessionCode from the current active tab.
 *  Returns null if no tab is active or the active tab is not a session. */
export function getActiveSessionInfo(): { hostId: string; sessionCode: string } | null {
  const { activeTabId, tabs } = useTabStore.getState()
  if (!activeTabId) return null
  const tab = tabs[activeTabId]
  if (!tab) return null
  const primary = getPrimaryPane(tab.layout)
  if (primary.content.kind !== 'session') return null
  return { hostId: primary.content.hostId, sessionCode: primary.content.sessionCode }
}
