import type { Workspace, Tab } from '../../../types/tab'

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

  // Phase 2 additions — only used by ActivityBarWide when tabPosition='left'
  tabsById?: Record<string, Tab>
  activeTabId?: string | null
  onSelectTab?: (tabId: string) => void
  onCloseTab?: (tabId: string) => void
  onMiddleClickTab?: (tabId: string) => void
  onContextMenuTab?: (e: React.MouseEvent, tabId: string) => void
  onRenameTab?: (tabId: string) => void
  onReorderWorkspaceTabs?: (wsId: string, tabIds: string[]) => void
  onReorderStandaloneTabs?: (tabIds: string[]) => void
  onAddTabToWorkspace?: (wsId: string) => void

  // Phase 3 PR D — cross-workspace DnD.
  // Default: ActivityBarWide calls the workspace store directly. Pass a
  // handler here to intercept or veto (e.g. workspace-locked mode).
  onMoveTabToWorkspace?: (tabId: string, targetWsId: string, afterTabId: string | null) => void
  onMoveTabToStandalone?: (tabId: string, sourceWsId: string) => void
}
