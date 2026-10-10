// spa/src/lib/workspace-settings-panes.ts — a deleted workspace's settings page can also sit in a SECONDARY pane of a split tab
// (#1955). The delete paths (WorkspaceSettingsPage's interactive delete, applyWorkspacesSection's sync removal) close a tab
// whose primary pane is such a page; for a split tab whose primary pane is something else, only that pane goes and the tab
// with its primary pane stays (interface lead's call). A page about a workspace that no longer exists would only say
// "Workspace not found".
import type { PaneContent, PaneLayout } from '../types/tab'
import { collectLeaves, getPrimaryPane, removePane } from './pane-tree'

/** Whether `content` is the settings page of one of `workspaceIds`. */
export function isWorkspaceSettingsOf(content: PaneContent, workspaceIds: readonly string[]): boolean {
  return content.kind === 'settings' && content.scope !== 'global' && workspaceIds.includes(content.scope.workspaceId)
}

/** The ids of the non-primary panes of `layout` that show a settings page of one of `workspaceIds`. */
export function secondarySettingsPaneIds(layout: PaneLayout, workspaceIds: readonly string[]): string[] {
  const primary = getPrimaryPane(layout).id
  return collectLeaves(layout)
    .filter((p) => p.id !== primary && isWorkspaceSettingsOf(p.content, workspaceIds))
    .map((p) => p.id)
}

/**
 * `layout` without its secondary panes that show the settings page of a workspace NOT in `existing`; the same object when
 * there are none (#2514: a tabs payload from a client that never saw the workspace's deletion). A primary pane is never
 * removed here (a tab whose primary pane is such a page arrives as it is, as before).
 */
export function withoutStaleSettingsPanes(layout: PaneLayout, existing: ReadonlySet<string>): PaneLayout {
  const primary = getPrimaryPane(layout).id
  let next = layout
  for (const p of collectLeaves(layout)) {
    const c = p.content
    if (p.id === primary || c.kind !== 'settings' || c.scope === 'global' || existing.has(c.scope.workspaceId)) continue
    next = removePane(next, p.id) ?? next
  }
  return next
}

/** `layout` without those panes; the same object when there are none. A primary pane is never removed here. */
export function withoutSecondarySettingsPanes(layout: PaneLayout, workspaceIds: readonly string[]): PaneLayout {
  let next = layout
  for (const id of secondarySettingsPaneIds(layout, workspaceIds)) next = removePane(next, id) ?? next
  return next
}
