import { createContext, useContext } from 'react'

export interface WorkspaceSettingsDraftActions {
  save: () => void
  cancel: () => void
}

export interface WorkspaceSettingsDraftContextValue {
  setDirty: (id: string, dirty: boolean) => void
  register: (id: string, actions: WorkspaceSettingsDraftActions) => () => void
}

const WorkspaceSettingsDraftContext = createContext<WorkspaceSettingsDraftContextValue | null>(null)

export const WorkspaceSettingsDraftProvider = WorkspaceSettingsDraftContext.Provider

export function useWorkspaceSettingsDraft() {
  return useContext(WorkspaceSettingsDraftContext)
}
