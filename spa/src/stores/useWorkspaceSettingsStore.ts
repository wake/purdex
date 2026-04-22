import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import { deepFreeze, sanitizeScopedModuleMap } from './settings-store-shape'

type ModulePayload = Record<string, unknown>
type WorkspaceSlot = Record<string, ModulePayload>

interface WorkspaceSettingsState {
  workspaces: Record<string, WorkspaceSlot>
  get: (workspaceId: string, moduleId: string) => ModulePayload | undefined
  set: (workspaceId: string, moduleId: string, patch: ModulePayload) => void
  clearWorkspace: (workspaceId: string) => void
  clearModule: (workspaceId: string, moduleId: string) => void
}

export const useWorkspaceSettingsStore = create<WorkspaceSettingsState>()(
  persist(
    (set, get) => ({
      workspaces: {},

      get: (workspaceId, moduleId) => {
        const payload = get().workspaces[workspaceId]?.[moduleId]
        return payload ? deepFreeze(payload) : undefined
      },

      set: (workspaceId, moduleId, patch) =>
        set((state) => {
          const currentWorkspace = state.workspaces[workspaceId] ?? {}
          return {
            workspaces: {
              ...state.workspaces,
              [workspaceId]: {
                ...currentWorkspace,
                [moduleId]: {
                  ...(currentWorkspace[moduleId] ?? {}),
                  ...patch,
                },
              },
            },
          }
        }),

      clearWorkspace: (workspaceId) =>
        set((state) => {
          if (!(workspaceId in state.workspaces)) return state
          const next = { ...state.workspaces }
          delete next[workspaceId]
          return { workspaces: next }
        }),

      clearModule: (workspaceId, moduleId) =>
        set((state) => {
          const currentWorkspace = state.workspaces[workspaceId]
          if (!currentWorkspace || !(moduleId in currentWorkspace)) return state
          const nextWorkspace = { ...currentWorkspace }
          delete nextWorkspace[moduleId]
          return {
            workspaces: {
              ...state.workspaces,
              [workspaceId]: nextWorkspace,
            },
          }
        }),
    }),
    {
      name: STORAGE_KEYS.WORKSPACE_SETTINGS,
      storage: purdexStorage,
      version: 1,
      onRehydrateStorage: () => (state) => {
        if (!state) return
        state.workspaces = sanitizeScopedModuleMap(state.workspaces)
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.WORKSPACE_SETTINGS, useWorkspaceSettingsStore)
