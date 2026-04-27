import type { PaneLayout, Tab, Workspace } from '../types/tab'

/**
 * Recursively walks a PaneLayout tree, collecting hostIds from every
 * pane whose content is `kind: 'tmux-session'`. Order matches
 * pre-order traversal of the layout tree (left-to-right children).
 */
export function collectTmuxSessionHostIds(layout: PaneLayout): string[] {
  if (layout.type === 'leaf') {
    return layout.pane.content.kind === 'tmux-session'
      ? [layout.pane.content.hostId]
      : []
  }
  return layout.children.flatMap(collectTmuxSessionHostIds)
}

/**
 * Infer the "primary" hostId for a Workspace based on its tabs (spec §3.2.1).
 *
 *  1. Collect hostIds from every tmux-session pane across all tabs.
 *  2. Majority vote (highest count wins).
 *  3. Tie-break A: if `workspace.activeTabId` resolves to a tmux-session whose
 *     hostId is among the winners, prefer it.
 *  4. Tie-break B: otherwise, scan `workspace.tabs` in order and return the
 *     first hostId that appears in the winners set.
 *  5. Returns `null` when no tmux-session pane is found anywhere — caller
 *     MUST treat this as "host unknown" and surface the host picker (see
 *     spec §3.2.2 / §4.4 HostPickerPopover).
 *
 * Critically: this MUST NOT silently fall back to `useHostStore.activeHostId`
 * for the null case — that would risk sending keys to the wrong host.
 */
export function inferWorkspaceHostId(
  workspace: Workspace,
  tabs: Record<string, Tab>,
): string | null {
  const candidates = workspace.tabs
    .map((tabId) => tabs[tabId])
    .filter((t): t is Tab => !!t)
    .flatMap((t) => collectTmuxSessionHostIds(t.layout))

  if (candidates.length === 0) return null

  const counts = new Map<string, number>()
  for (const h of candidates) counts.set(h, (counts.get(h) ?? 0) + 1)
  const max = Math.max(...counts.values())
  const winners = [...counts.entries()].filter(([, c]) => c === max).map(([h]) => h)
  if (winners.length === 1) return winners[0]

  // Tie-break A: active tab's hostId if it is a tmux-session and is among winners.
  if (workspace.activeTabId) {
    const activeTab = tabs[workspace.activeTabId]
    if (activeTab) {
      const activeHosts = collectTmuxSessionHostIds(activeTab.layout)
      const winner = activeHosts.find((h) => winners.includes(h))
      if (winner) return winner
    }
  }

  // Tie-break B: first winner in tabs order.
  for (const tabId of workspace.tabs) {
    const t = tabs[tabId]
    if (!t) continue
    const hosts = collectTmuxSessionHostIds(t.layout)
    const first = hosts.find((h) => winners.includes(h))
    if (first) return first
  }
  return winners[0] // theoretically unreachable
}
