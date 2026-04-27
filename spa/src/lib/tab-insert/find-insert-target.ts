import type { Tab, PaneContent } from '../../types/tab'
import { getPrimaryPane } from '../pane-tree'

/**
 * Find the insertion target for a new tab, aggregating same-kind tabs.
 * Scans right from activeTabId for the nearest tab whose primary pane
 * content matches `isSameKind`. Returns that tab's ID (insert after it).
 * Falls back to activeTabId if no match found or if activeTabId is not
 * in `orderedTabIds`.
 */
export function findInsertTarget(
  orderedTabIds: string[],
  activeTabId: string,
  tabs: Record<string, Tab>,
  isSameKind: (content: PaneContent) => boolean,
): string {
  const activeIdx = orderedTabIds.indexOf(activeTabId)
  if (activeIdx === -1) return activeTabId

  for (let i = activeIdx + 1; i < orderedTabIds.length; i++) {
    const tab = tabs[orderedTabIds[i]]
    if (tab && isSameKind(getPrimaryPane(tab.layout).content)) {
      return orderedTabIds[i]
    }
  }
  return activeTabId
}
