import type { Pane, PaneContent, PaneLayout } from '../types/tab'

export function getPrimaryPane(layout: PaneLayout): Pane {
  if (layout.type === 'leaf') return layout.pane
  if (!layout.children.length) {
    // Corrupted layout — return a placeholder to prevent crash
    return { id: 'corrupted', content: { kind: 'new-tab' } }
  }
  return getPrimaryPane(layout.children[0])
}

export function findPane(layout: PaneLayout, paneId: string): Pane | undefined {
  if (layout.type === 'leaf') {
    return layout.pane.id === paneId ? layout.pane : undefined
  }
  for (const child of layout.children) {
    const found = findPane(child, paneId)
    if (found) return found
  }
  return undefined
}

export function updatePaneInLayout(
  layout: PaneLayout,
  paneId: string,
  content: PaneContent,
): PaneLayout {
  if (layout.type === 'leaf') {
    if (layout.pane.id === paneId) {
      return { type: 'leaf', pane: { ...layout.pane, content } }
    }
    return layout
  }
  return {
    ...layout,
    children: layout.children.map((child) => updatePaneInLayout(child, paneId, content)),
  }
}

export function getLayoutKey(layout: PaneLayout): string {
  return layout.type === 'leaf' ? layout.pane.id : layout.id
}

/**
 * Find the tab ID containing a session pane matching the given session code.
 */
export function findTabBySessionCode(
  tabs: Record<string, { layout: PaneLayout }>,
  sessionCode: string,
): string | undefined {
  for (const [tabId, tab] of Object.entries(tabs)) {
    const primary = getPrimaryPane(tab.layout)
    if (primary.content.kind === 'tmux-session' && primary.content.sessionCode === sessionCode) {
      return tabId
    }
  }
  return undefined
}
