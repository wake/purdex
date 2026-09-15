import type { PaneLayout } from '../types/tab'

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
