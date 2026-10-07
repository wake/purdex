import { useTabStore } from '../stores/useTabStore'
import { collectLeaves, paneAgentKey, paneShowsAgent } from './pane-tree'
import { compositeKey } from './composite-key'

/** Every agent key shown by a pane of the active tab — the primary one and every other leaf of a split, at any depth
 *  (#1853), by `paneAgentKey`'s rules: a worker (execution) pane yields `exec-<id>` on its resolved host
 *  (worker-pane theme spec §8.2), an ended tmux pane nothing. In layout order, each composite key once. What unread
 *  marking and auto mark-read treat as on screen — the same rule as `isAgentVisibleInActiveTab`.
 *  `[]` when no tab is active or the active id names no tab. */
export function getActiveTabAgents(): Array<{ hostId: string; sessionCode: string }> {
  const { activeTabId, tabs } = useTabStore.getState()
  if (!activeTabId) return []
  const tab = tabs[activeTabId]
  if (!tab) return []
  const seen = new Set<string>()
  const out: Array<{ hostId: string; sessionCode: string }> = []
  for (const pane of collectLeaves(tab.layout)) {
    const key = paneAgentKey(pane.content)
    if (!key) continue
    const ck = compositeKey(key.hostId, key.sessionCode)
    if (seen.has(ck)) continue
    seen.add(ck)
    out.push(key)
  }
  return out
}

/** Is the agent key `hostId`/`sessionCode` shown by ANY pane of the active tab — the primary one or any other leaf of
 *  a split, at any depth (#1840)? The notification dispatcher's "the user is looking at it" check, and unread
 *  marking's (#1853): a split tab shows every one of its panes at once.
 *  False when no tab is active or the active id names no tab. */
export function isAgentVisibleInActiveTab(hostId: string, sessionCode: string): boolean {
  const { activeTabId, tabs } = useTabStore.getState()
  if (!activeTabId) return false
  const tab = tabs[activeTabId]
  if (!tab) return false
  return collectLeaves(tab.layout).some((p) => paneShowsAgent(p.content, hostId, sessionCode))
}
