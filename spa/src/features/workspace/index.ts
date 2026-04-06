// Store
export { useWorkspaceStore } from './store'

// Hooks
export { useTabWorkspaceActions } from './hooks'

// Components
export { ActivityBar } from './components/ActivityBar'
export { WorkspaceDeleteDialog } from './components/WorkspaceDeleteDialog'

// Lib
export { getVisibleTabIds } from './lib/getVisibleTabIds'

// Re-export shared types from types/tab.ts
export type { Workspace } from '../../types/tab'
export { createWorkspace, isStandaloneTab } from '../../types/tab'
