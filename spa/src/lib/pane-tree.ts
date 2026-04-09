import type { Pane, PaneContent, PaneLayout, LayoutPattern } from '../types/tab'
import { generateId } from './id'

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

export function scanPaneTree(layout: PaneLayout, fn: (pane: Pane) => void): void {
  if (layout.type === 'leaf') {
    fn(layout.pane)
  } else {
    layout.children.forEach((child) => scanPaneTree(child, fn))
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

export function splitAtPane(layout: PaneLayout, paneId: string, direction: 'h' | 'v', newContent: PaneContent): PaneLayout {
  if (layout.type === 'leaf') {
    if (layout.pane.id === paneId) {
      return { type: 'split', id: generateId(), direction, children: [layout, { type: 'leaf', pane: { id: generateId(), content: newContent } }], sizes: [50, 50] }
    }
    return layout
  }
  const newChildren = layout.children.map((child) => splitAtPane(child, paneId, direction, newContent))
  return newChildren.some((c, i) => c !== layout.children[i]) ? { ...layout, children: newChildren } : layout
}

export function removePane(layout: PaneLayout, paneId: string): PaneLayout | null {
  if (layout.type === 'leaf') return layout.pane.id === paneId ? null : layout

  const mapped = layout.children.map((child) => removePane(child, paneId))
  const newChildren = mapped.filter((c): c is PaneLayout => c !== null)

  if (newChildren.length === layout.children.length) {
    // No child was removed (null), but a child might have been modified internally
    const anyChanged = newChildren.some((c, i) => c !== layout.children[i])
    if (!anyChanged) return layout
    // Children were modified but not removed — return updated layout with same sizes
    return { ...layout, children: newChildren }
  }
  if (newChildren.length === 0) return null
  if (newChildren.length === 1) return newChildren[0]

  const keptSizes = layout.sizes.filter((_, i) => mapped[i] !== null)
  const total = keptSizes.reduce((a, b) => a + b, 0)
  const normalizedSizes = keptSizes.map((s) => (s / total) * 100)
  return { ...layout, children: newChildren, sizes: normalizedSizes }
}

export function countLeaves(layout: PaneLayout): number {
  if (layout.type === 'leaf') return 1
  return layout.children.reduce((sum, child) => sum + countLeaves(child), 0)
}

export function collectLeaves(layout: PaneLayout): Pane[] {
  if (layout.type === 'leaf') return [layout.pane]
  return layout.children.flatMap((child) => collectLeaves(child))
}

function newTabPane(): Pane {
  return { id: generateId(), content: { kind: 'new-tab' } }
}

export function applyLayoutPattern(layout: PaneLayout, pattern: LayoutPattern): PaneLayout {
  const leaves = collectLeaves(layout)
  const p = (i: number): Pane => leaves[i] ?? newTabPane()
  switch (pattern) {
    case 'single': return { type: 'leaf', pane: p(0) }
    case 'split-h': return { type: 'split', id: generateId(), direction: 'h', children: [{ type: 'leaf', pane: p(0) }, { type: 'leaf', pane: p(1) }], sizes: [50, 50] }
    case 'split-v': return { type: 'split', id: generateId(), direction: 'v', children: [{ type: 'leaf', pane: p(0) }, { type: 'leaf', pane: p(1) }], sizes: [50, 50] }
    case 'grid-4': return { type: 'split', id: generateId(), direction: 'v', children: [
      { type: 'split', id: generateId(), direction: 'h', children: [{ type: 'leaf', pane: p(0) }, { type: 'leaf', pane: p(1) }], sizes: [50, 50] },
      { type: 'split', id: generateId(), direction: 'h', children: [{ type: 'leaf', pane: p(2) }, { type: 'leaf', pane: p(3) }], sizes: [50, 50] },
    ], sizes: [50, 50] }
  }
}
