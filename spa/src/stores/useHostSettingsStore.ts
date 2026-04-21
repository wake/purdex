import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

type ModulePayload = Record<string, unknown>
type HostSlot = Record<string, ModulePayload>

interface HostSettingsState {
  hosts: Record<string, HostSlot>
  get: (hostId: string, moduleId: string) => ModulePayload | undefined
  set: (hostId: string, moduleId: string, patch: ModulePayload) => void
  clearHost: (hostId: string) => void
  clearModule: (hostId: string, moduleId: string) => void
}

export const useHostSettingsStore = create<HostSettingsState>()(
  persist(
    (set, get) => ({
      hosts: {},

      get: (hostId, moduleId) => get().hosts[hostId]?.[moduleId],

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
    },
  ),
)

syncManager.register(STORAGE_KEYS.HOST_SETTINGS, useHostSettingsStore)
