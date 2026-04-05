// spa/src/stores/useConfigStore.ts
import { create } from 'zustand'
import { getConfig, updateConfig, type ConfigData } from '../lib/host-api'

interface ConfigState {
  config: ConfigData | null
  loading: boolean
  fetch: (hostId: string) => Promise<void>
  update: (hostId: string, updates: Partial<ConfigData>) => Promise<void>
}

export const useConfigStore = create<ConfigState>((set) => ({
  config: null,
  loading: false,
  fetch: async (hostId) => {
    set({ loading: true })
    try {
      const config = await getConfig(hostId)
      set({ config, loading: false })
    } catch {
      set({ loading: false })
    }
  },
  update: async (hostId, updates) => {
    const config = await updateConfig(hostId, updates)
    set({ config })
  },
}))
