import type { Tab } from '../types/tab'
import { getPrimaryPane } from './pane-tree'

/**
 * Find the insertion target for a new browser tab.
 * Scans right from activeTabId to find the nearest browser tab.
 * Returns that browser tab's ID (insert after it), or activeTabId if none found.
 */
export function findBrowserInsertTarget(
  orderedTabIds: string[],
  activeTabId: string,
  tabs: Record<string, Tab>,
): string {
  const activeIdx = orderedTabIds.indexOf(activeTabId)
  if (activeIdx === -1) return activeTabId

  for (let i = activeIdx + 1; i < orderedTabIds.length; i++) {
    const tab = tabs[orderedTabIds[i]]
    if (tab && getPrimaryPane(tab.layout).content.kind === 'browser') {
      return orderedTabIds[i]
    }
  }

  return activeTabId
}
