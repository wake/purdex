// spa/src/stores/useNotificationSettingsStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export interface NotificationSettings {
  enabled: boolean
  events: Record<string, boolean>
  notifyWithoutTab: boolean
  reopenTabOnClick: boolean
}

const DEFAULT_SETTINGS: NotificationSettings = {
  enabled: true, events: {}, notifyWithoutTab: false, reopenTabOnClick: false,
}

interface NotificationSettingsState {
  agents: Record<string, NotificationSettings>
  getSettingsForAgent: (agentType: string) => NotificationSettings
  setAgentEnabled: (agentType: string, enabled: boolean) => void
  setEventEnabled: (agentType: string, eventName: string, enabled: boolean) => void
  setNotifyWithoutTab: (agentType: string, value: boolean) => void
  setReopenTabOnClick: (agentType: string, value: boolean) => void
}

function getOrDefault(agents: Record<string, NotificationSettings>, agentType: string): NotificationSettings {
  return agents[agentType] ?? { ...DEFAULT_SETTINGS }
}

function updateAgent(agents: Record<string, NotificationSettings>, agentType: string, patch: Partial<NotificationSettings>): Record<string, NotificationSettings> {
  const current = getOrDefault(agents, agentType)
  return { ...agents, [agentType]: { ...current, ...patch } }
}

export const useNotificationSettingsStore = create<NotificationSettingsState>()(
  persist(
    (set, get) => ({
      agents: {},
      getSettingsForAgent: (agentType) => getOrDefault(get().agents, agentType),
      setAgentEnabled: (agentType, enabled) => set((s) => ({ agents: updateAgent(s.agents, agentType, { enabled }) })),
      setEventEnabled: (agentType, eventName, enabled) => set((s) => {
        const current = getOrDefault(s.agents, agentType)
        return { agents: updateAgent(s.agents, agentType, { events: { ...current.events, [eventName]: enabled } }) }
      }),
      setNotifyWithoutTab: (agentType, value) => set((s) => ({ agents: updateAgent(s.agents, agentType, { notifyWithoutTab: value }) })),
      setReopenTabOnClick: (agentType, value) => set((s) => ({ agents: updateAgent(s.agents, agentType, { reopenTabOnClick: value }) })),
    }),
    { name: 'tbox-notification-settings', partialize: (state) => ({ agents: state.agents }) },
  ),
)
