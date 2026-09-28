import { useTabStore } from '../stores/useTabStore'
import { getPrimaryPane } from './pane-tree'
import { execAgentCode } from './nex/worker-agent-status'
import { resolveExecutionHostId } from './nex/resolve-host'

/** Derive the active session code from the current active tab.
 *  Returns null if no tab is active or the active tab is not a session. */
export function getActiveSessionCode(): string | null {
  const { activeTabId, tabs } = useTabStore.getState()
  if (!activeTabId) return null
  const tab = tabs[activeTabId]
  if (!tab) return null
  const primary = getPrimaryPane(tab.layout)
  return primary.content.kind === 'tmux-session' ? primary.content.sessionCode : null
}

/** Derive both hostId and sessionCode (the `useAgentStore` key) from the current active tab.
 *  A worker (execution) tab yields `exec:<id>` on its resolved host (worker-pane theme spec §8.2),
 *  so unread and notification suppression treat it like a terminal tab — a caller that needs a
 *  tmux code must skip it with `isExecAgentCode`.
 *  Returns null if no tab is active or the active tab is neither. */
export function getActiveSessionInfo(): { hostId: string; sessionCode: string } | null {
  const { activeTabId, tabs } = useTabStore.getState()
  if (!activeTabId) return null
  const tab = tabs[activeTabId]
  if (!tab) return null
  const c = getPrimaryPane(tab.layout).content
  if (c.kind === 'tmux-session') return { hostId: c.hostId, sessionCode: c.sessionCode }
  if (c.kind === 'execution') return { hostId: resolveExecutionHostId(c.host), sessionCode: execAgentCode(c.executionId) }
  return null
}
