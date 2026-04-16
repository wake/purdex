import type { Workspace } from '../../../types/tab'

/**
 * Shared props for {@link ActivityBar} coordinator and its Narrow / Wide variants.
 *
 * Extracted to a standalone file so all three implementations agree on the shape.
 * Add a prop here once; it propagates to coordinator + both variants at build time.
 */
export interface ActivityBarProps {
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  activeStandaloneTabId: string | null
  onSelectWorkspace: (wsId: string) => void
  onSelectHome: () => void
  standaloneTabIds: string[]
  onAddWorkspace: () => void
  onReorderWorkspaces?: (orderedIds: string[]) => void
  onContextMenuWorkspace?: (e: React.MouseEvent, wsId: string) => void
  onOpenHosts: () => void
  onOpenSettings: () => void
}
