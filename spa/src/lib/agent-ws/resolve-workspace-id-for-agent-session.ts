import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { scanPaneTree } from '../pane-tree'

/**
 * Find the workspace that owns a tab matching (hostId, sessionCode) for an
 * agent session event (e.g. PathHint dispatch).
 *
 * Returns:
 *   - workspaceId  — when exactly one workspace owns a matching tab
 *   - null         — when no tab matches OR multiple workspaces match OR the
 *                    matching tab is standalone
 *
 * "Multiple match → null" is intentional (attacker review #6): it avoids racy
 * writes to a workspace the user just switched to during tear-off / merge
 * transitions. PathHint with no clear owner is dropped.
 *
 * Scans every leaf in the pane tree (not just the primary pane) so a session
 * hosted in a non-primary split pane still resolves.
 */
export function resolveWorkspaceIdForAgentSession(hostId: string, sessionCode: string): string | null {
  const tabs = useTabStore.getState().tabs
  const matchingTabIds = new Set<string>()
  for (const [tabId, tab] of Object.entries(tabs)) {
    if (!tab) continue
    let matched = false
    scanPaneTree(tab.layout, (pane) => {
      if (matched) return
      const c = pane.content
      if (c.kind === 'tmux-session' && c.hostId === hostId && c.sessionCode === sessionCode) {
        matched = true
      }
    })
    if (matched) matchingTabIds.add(tabId)
  }
  if (matchingTabIds.size === 0) return null

  const wsState = useWorkspaceStore.getState()
  const owners = wsState.workspaces.filter((w) =>
    w.tabs.some((tid) => matchingTabIds.has(tid)),
  )
  if (owners.length !== 1) return null
  return owners[0].id
}
