// spa/src/stores/useAgentStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { getActiveSessionInfo } from '../lib/active-session'
import { compositeKey } from '../lib/composite-key'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

export type AgentStatus = 'running' | 'waiting' | 'idle' | 'error'
export type TabIndicatorStyle = 'overlay' | 'replace' | 'inline'

/** Normalized event from backend (replaces AgentHookEvent). */
export interface NormalizedEvent {
  agent_type: string
  status: string             // running | waiting | idle | error | clear
  model?: string
  subagents?: string[]
  raw_event_name: string
  broadcast_ts: number
  detail?: Record<string, unknown>
}

interface AgentState {
  // Backend-derived state
  statuses: Record<string, AgentStatus>
  agentTypes: Record<string, string>
  models: Record<string, string>
  subagents: Record<string, string[]>
  lastEvents: Record<string, NormalizedEvent>  // for notification dispatcher

  // UI state
  unread: Record<string, boolean>
  tabIndicatorStyle: TabIndicatorStyle

  // Actions
  handleNormalizedEvent: (hostId: string, sessionCode: string, event: NormalizedEvent) => void
  clearSession: (hostId: string, sessionCode: string) => void
  markRead: (hostId: string, sessionCode: string) => void
  removeHost: (hostId: string) => void
  setTabIndicatorStyle: (style: TabIndicatorStyle) => void
}

export const useAgentStore = create<AgentState>()(
  persist(
    (set, get) => ({
      statuses: {},
      agentTypes: {},
      models: {},
      subagents: {},
      lastEvents: {},
      unread: {},
      tabIndicatorStyle: 'replace' as TabIndicatorStyle,

      clearSession: (hostId, sessionCode) => {
        const key = compositeKey(hostId, sessionCode)
        set((s) => {
          const filterOut = <T,>(rec: Record<string, T>): Record<string, T> => {
            // eslint-disable-next-line @typescript-eslint/no-unused-vars
            const { [key]: _, ...rest } = rec
            return rest
          }
          return {
            statuses: filterOut(s.statuses),
            agentTypes: filterOut(s.agentTypes),
            models: filterOut(s.models),
            subagents: filterOut(s.subagents),
            lastEvents: filterOut(s.lastEvents),
            unread: filterOut(s.unread),
          }
        })
      },

      handleNormalizedEvent: (hostId, sessionCode, event) => {
        const key = compositeKey(hostId, sessionCode)

        if (event.status === 'clear') {
          get().clearSession(hostId, sessionCode)
          return
        }

        // Store last event (for notification dispatcher)
        set((s) => ({ lastEvents: { ...s.lastEvents, [key]: event } }))

        // Store agent type
        if (event.agent_type) {
          set((s) => ({ agentTypes: { ...s.agentTypes, [key]: event.agent_type } }))
        }

        // Store model (persist across events)
        if (event.model) {
          set((s) => ({ models: { ...s.models, [key]: event.model! } }))
        }

        // Store subagents
        if (event.subagents) {
          set((s) => ({
            subagents: event.subagents!.length > 0
              ? { ...s.subagents, [key]: event.subagents! }
              // eslint-disable-next-line @typescript-eslint/no-unused-vars
              : (() => { const { [key]: _, ...rest } = s.subagents; return rest })(),
          }))
        }

        // Store status (skip events with no status, e.g. SubagentStart/Stop)
        const status = event.status as AgentStatus | ''
        if (status) {
          set((s) => ({ statuses: { ...s.statuses, [key]: status } }))

          // Mark unread when not focused
          const isActionable = status === 'waiting' || status === 'error' ||
            (status === 'idle' && event.raw_event_name !== 'Notification')
          const activeInfo = getActiveSessionInfo()
          const activeKey = activeInfo ? compositeKey(activeInfo.hostId, activeInfo.sessionCode) : ''
          if (isActionable && activeKey !== key) {
            set((s) => ({ unread: { ...s.unread, [key]: true } }))
          }
        }
      },

      markRead: (hostId, sessionCode) => set((s) => {
        const key = compositeKey(hostId, sessionCode)
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        const { [key]: _, ...rest } = s.unread
        return { unread: rest }
      }),

      removeHost: (hostId) => set((s) => {
        const prefix = `${hostId}:`
        const filterKeys = <T,>(record: Record<string, T>): Record<string, T> => {
          const result: Record<string, T> = {}
          for (const [k, v] of Object.entries(record)) {
            if (!k.startsWith(prefix)) result[k] = v
          }
          return result
        }
        return {
          statuses: filterKeys(s.statuses),
          agentTypes: filterKeys(s.agentTypes),
          models: filterKeys(s.models),
          subagents: filterKeys(s.subagents),
          lastEvents: filterKeys(s.lastEvents),
          unread: filterKeys(s.unread),
        }
      }),

      setTabIndicatorStyle: (style) => set({ tabIndicatorStyle: style }),
    }),
    {
      name: STORAGE_KEYS.AGENT,
      storage: purdexStorage,
      version: 3,
      partialize: (state) => ({ tabIndicatorStyle: state.tabIndicatorStyle }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.AGENT, useAgentStore)
