import type { PaneLayout, SplitLayout } from '../types/tab'

function isSplit(layout: PaneLayout): layout is SplitLayout {
  return layout.type === 'split'
}

export function isGrid4(layout: PaneLayout): boolean {
  if (!isSplit(layout) || layout.direction !== 'v' || layout.children.length !== 2) return false
  return layout.children.every(
    (child) => child.type === 'split' && child.direction === 'h' && child.children.length === 2,
  )
}
