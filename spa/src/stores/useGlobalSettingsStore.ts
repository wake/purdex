import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

type ModulePayload = Record<string, unknown>

interface GlobalSettingsState {
  modules: Record<string, ModulePayload>
  get: (moduleId: string) => ModulePayload | undefined
  set: (moduleId: string, patch: ModulePayload) => void
  clear: (moduleId?: string) => void
}

export const useGlobalSettingsStore = create<GlobalSettingsState>()(
  persist(
    (set, get) => ({
      modules: {},

      get: (moduleId) => get().modules[moduleId],

      set: (moduleId, patch) =>
        set((state) => ({
          modules: {
            ...state.modules,
            [moduleId]: {
              ...(state.modules[moduleId] ?? {}),
              ...patch,
            },
          },
        })),

      clear: (moduleId) =>
        set((state) => {
          if (moduleId === undefined) {
            return { modules: {} }
          }
          if (!(moduleId in state.modules)) return state
          const next = { ...state.modules }
          delete next[moduleId]
          return { modules: next }
        }),
    }),
    {
      name: STORAGE_KEYS.GLOBAL_SETTINGS,
      storage: purdexStorage,
      version: 1,
    },
  ),
)

syncManager.register(STORAGE_KEYS.GLOBAL_SETTINGS, useGlobalSettingsStore)
