// Store
export { useWorkspaceStore } from './store'

// Hooks
export { useTabWorkspaceActions } from './hooks'

// Components
export { ActivityBar } from './components/ActivityBar'

// Re-export shared types from types/tab.ts
export type { Workspace } from '../../types/tab'
export { createWorkspace, isStandaloneTab } from '../../types/tab'
