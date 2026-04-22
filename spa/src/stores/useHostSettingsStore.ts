import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import { deepFreeze, sanitizeScopedModuleMap } from './settings-store-shape'

type ModulePayload = Record<string, unknown>
type HostSlot = Record<string, ModulePayload>

interface HostSettingsState {
  hosts: Record<string, HostSlot>
  get: (hostId: string, moduleId: string) => ModulePayload | undefined
  set: (hostId: string, moduleId: string, patch: ModulePayload) => void
  removeKey: (hostId: string, moduleId: string, key: string) => void
  clearHost: (hostId: string) => void
  clearModule: (hostId: string, moduleId: string) => void
}

export const useHostSettingsStore = create<HostSettingsState>()(
  persist(
    (set, get) => ({
      hosts: {},

      get: (hostId, moduleId) => {
        const payload = get().hosts[hostId]?.[moduleId]
        return payload ? deepFreeze(payload) : undefined
      },

      set: (hostId, moduleId, patch) =>
        set((state) => {
          const currentHost = state.hosts[hostId] ?? {}
          return {
            hosts: {
              ...state.hosts,
              [hostId]: {
                ...currentHost,
                [moduleId]: {
                  ...(currentHost[moduleId] ?? {}),
                  ...patch,
                },
              },
            },
          }
        }),

      removeKey: (hostId, moduleId, key) =>
        set((state) => {
          const currentHost = state.hosts[hostId]
          const currentModule = currentHost?.[moduleId]
          if (!currentHost || !currentModule || !(key in currentModule)) return state
          const { [key]: _omit, ...rest } = currentModule
          void _omit
          const nextHost = { ...currentHost }
          if (Object.keys(rest).length === 0) {
            delete nextHost[moduleId]
          } else {
            nextHost[moduleId] = rest
          }
          return { hosts: { ...state.hosts, [hostId]: nextHost } }
        }),

      clearHost: (hostId) =>
        set((state) => {
          if (!(hostId in state.hosts)) return state
          const next = { ...state.hosts }
          delete next[hostId]
          return { hosts: next }
        }),

      clearModule: (hostId, moduleId) =>
        set((state) => {
          const currentHost = state.hosts[hostId]
          if (!currentHost || !(moduleId in currentHost)) return state
          const nextHost = { ...currentHost }
          delete nextHost[moduleId]
          return {
            hosts: {
              ...state.hosts,
              [hostId]: nextHost,
            },
          }
        }),
    }),
    {
      name: STORAGE_KEYS.HOST_SETTINGS,
      storage: purdexStorage,
      version: 1,
      onRehydrateStorage: () => (state) => {
        if (!state) return
        state.hosts = sanitizeScopedModuleMap(state.hosts)
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.HOST_SETTINGS, useHostSettingsStore)
