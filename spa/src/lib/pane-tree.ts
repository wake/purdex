import type { Pane, PaneContent, PaneLayout } from '../types/tab'

export function getPrimaryPane(layout: PaneLayout): Pane {
  if (layout.type === 'leaf') return layout.pane
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
