// Store
export { useWorkspaceStore } from './store'

// Hooks
export { useTabWorkspaceActions } from './hooks'

// Components
export { ActivityBar } from './components/ActivityBar'
export { WorkspaceDeleteDialog } from './components/WorkspaceDeleteDialog'
export { WorkspaceContextMenu } from './components/WorkspaceContextMenu'
export { WorkspaceIconPicker } from './components/WorkspaceIconPicker'
export { MigrateTabsDialog } from './components/MigrateTabsDialog'
export { WorkspaceSettingsPage } from './components/WorkspaceSettingsPage'
export { WorkspaceIcon } from './components/WorkspaceIcon'
export { WorkspaceEmptyState } from './components/WorkspaceEmptyState'

// Lib
export { getVisibleTabIds } from './lib/getVisibleTabIds'
export { nextWorkspaceName } from './lib/workspace-naming'

// Re-export shared types from types/tab.ts
export type { Workspace } from '../../types/tab'
export { createWorkspace, isStandaloneTab } from '../../types/tab'
