import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage } from '../lib/storage'
import { STORAGE_KEYS } from '../lib/storage/keys'

interface ModuleConfigState {
  globalConfig: Record<string, Record<string, unknown>>
  setGlobalModuleConfig: (moduleId: string, key: string, value: unknown) => void
  getGlobalModuleConfig: (moduleId: string, key: string) => unknown
}

export const useModuleConfigStore = create<ModuleConfigState>()(
  persist(
    (set, get) => ({
      globalConfig: {},

      setGlobalModuleConfig: (moduleId, key, value) =>
        set((state) => ({
          globalConfig: {
            ...state.globalConfig,
            [moduleId]: {
              ...(state.globalConfig[moduleId] ?? {}),
              [key]: value,
            },
          },
        })),

      getGlobalModuleConfig: (moduleId, key) => {
        return get().globalConfig[moduleId]?.[key]
      },
    }),
    {
      name: STORAGE_KEYS.MODULE_CONFIG,
      storage: purdexStorage,
      version: 1,
    },
  ),
)
