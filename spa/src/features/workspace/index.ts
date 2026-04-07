// Store
export { useWorkspaceStore } from './store'

// Hooks
export { useTabWorkspaceActions } from './hooks'

// Components
export { ActivityBar } from './components/ActivityBar'
export { WorkspaceDeleteDialog } from './components/WorkspaceDeleteDialog'
export { WorkspaceContextMenu } from './components/WorkspaceContextMenu'
export { WorkspaceChip } from './components/WorkspaceChip'
export { WorkspaceRenameDialog } from './components/WorkspaceRenameDialog'
export { WorkspaceColorPicker } from './components/WorkspaceColorPicker'
export { WorkspaceIconPicker } from './components/WorkspaceIconPicker'
export { MigrateTabsDialog } from './components/MigrateTabsDialog'
export { WorkspaceSettingsPage } from './components/WorkspaceSettingsPage'
export { WorkspaceIcon } from './components/WorkspaceIcon'
export { ColorGrid } from './components/WorkspaceColorPicker'

// Lib
export { getVisibleTabIds } from './lib/getVisibleTabIds'

// Re-export shared types from types/tab.ts
export type { Workspace } from '../../types/tab'
export { createWorkspace, isStandaloneTab } from '../../types/tab'
