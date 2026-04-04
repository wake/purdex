// spa/src/stores/useAgentStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { getActiveSessionInfo } from '../lib/active-session'
import { compositeKey } from '../lib/composite-key'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

export type AgentStatus = 'running' | 'waiting' | 'idle' | 'error'
export type TabIndicatorStyle = 'overlay' | 'replace' | 'inline'

export interface AgentHookEvent {
  tmux_session: string
  event_name: string
  raw_event: Record<string, unknown>
  agent_type: string
  broadcast_ts: number
}

interface AgentState {
  events: Record<string, AgentHookEvent>       // latest event per composite key
  statuses: Record<string, AgentStatus>        // derived status per composite key
  unread: Record<string, boolean>              // unread flag per composite key
  activeSubagents: Record<string, string[]>    // active subagent IDs per composite key
  tabIndicatorStyle: TabIndicatorStyle
  hooksInstalled: boolean                      // whether CC hooks are installed

  handleHookEvent: (hostId: string, sessionCode: string, event: AgentHookEvent) => void
  markRead: (hostId: string, sessionCode: string) => void
  clearAllSubagents: () => void
  clearSubagentsForHost: (hostId: string) => void
  removeHost: (hostId: string) => void
  setTabIndicatorStyle: (style: TabIndicatorStyle) => void
  setHooksInstalled: (installed: boolean) => void
}

export function deriveStatus(eventName: string, rawEvent?: Record<string, unknown>): AgentStatus | 'clear' | null {
  switch (eventName) {
    case 'SessionStart':
      // compact is background auto-compaction, not user activity
      if (rawEvent?.source === 'compact') return null
      // startup/resume/clear = CC waiting for user input
      return 'idle'
    case 'UserPromptSubmit':
      return 'running'
    case 'Notification': {
      const nt = rawEvent?.notification_type
      if (nt === 'permission_prompt' || nt === 'elicitation_dialog') return 'waiting'
      if (nt === 'idle_prompt' || nt === 'auth_success') return 'idle'
      // unrecognized or missing notification_type → don't change status
      if (nt !== undefined) console.warn('[deriveStatus] unknown notification_type:', nt)
      return null
    }
    case 'PermissionRequest':
      return 'waiting'
    case 'Stop':
      return 'idle'
    case 'StopFailure':
      return 'error'
    case 'SessionEnd':
      return 'clear'
    default:
      return null
  }
}

/** Extract a display label from an agent hook event (centralises CC-specific field access). */
export function getAgentLabel(event: AgentHookEvent | undefined): string | null {
  if (!event) return null
  const model = event.raw_event?.modelName as string | undefined
  return model || 'Agent'
}

export const useAgentStore = create<AgentState>()(
  persist(
    (set) => ({
      events: {},
      statuses: {},
      unread: {},
      activeSubagents: {},
      tabIndicatorStyle: 'overlay' as TabIndicatorStyle,
      hooksInstalled: false,

      handleHookEvent: (hostId, sessionCode, event) => {
        const key = compositeKey(hostId, sessionCode)
        const derived = deriveStatus(event.event_name, event.raw_event)

        // Subagent tracking — does not affect main status.
        // Ignore events without agent_id (malformed or future CC changes).
        if (event.event_name === 'SubagentStart') {
          const agentId = event.raw_event?.agent_id as string | undefined
          if (!agentId) return
          set((s) => {
            const current = s.activeSubagents[key] || []
            if (current.includes(agentId)) return { events: { ...s.events, [key]: event } }
            return {
              activeSubagents: { ...s.activeSubagents, [key]: [...current, agentId] },
              events: { ...s.events, [key]: event },
            }
          })
          return
        }
        if (event.event_name === 'SubagentStop') {
          const agentId = event.raw_event?.agent_id as string | undefined
          if (!agentId) return
          set((s) => {
            const current = s.activeSubagents[key] || []
            const filtered = current.filter((id) => id !== agentId)
            if (filtered.length === 0) {
              // eslint-disable-next-line @typescript-eslint/no-unused-vars
              const { [key]: _, ...rest } = s.activeSubagents
              return { activeSubagents: rest, events: { ...s.events, [key]: event } }
            }
            return {
              activeSubagents: { ...s.activeSubagents, [key]: filtered },
              events: { ...s.events, [key]: event },
            }
          })
          return
        }

        if (derived === 'clear') {
          // SessionEnd: remove session from all maps
          set((s) => {
            // eslint-disable-next-line @typescript-eslint/no-unused-vars
            const { [key]: _e, ...restEvents } = s.events
            // eslint-disable-next-line @typescript-eslint/no-unused-vars
            const { [key]: _s, ...restStatuses } = s.statuses
            // eslint-disable-next-line @typescript-eslint/no-unused-vars
            const { [key]: _u, ...restUnread } = s.unread
            // eslint-disable-next-line @typescript-eslint/no-unused-vars
            const { [key]: _a, ...restSubagents } = s.activeSubagents
            return { events: restEvents, statuses: restStatuses, unread: restUnread, activeSubagents: restSubagents }
          })
          return
        }

        // Safety net: clear subagents on SessionStart (fresh/resumed session).
        // Skip compact — that's mid-work auto-compaction, subagents may still be running.
        if (event.event_name === 'SessionStart' && event.raw_event?.source !== 'compact') {
          set((s) => {
            if (!s.activeSubagents[key]) return s
            // eslint-disable-next-line @typescript-eslint/no-unused-vars
            const { [key]: _, ...rest } = s.activeSubagents
            return { activeSubagents: rest }
          })
        }

        // Store the latest event
        set((s) => ({ events: { ...s.events, [key]: event } }))

        if (derived !== null) {
          // Don't let informational Notification subtypes (idle_prompt, auth_success)
          // downgrade an error status — the user should see the error until a real
          // state change (UserPromptSubmit, SessionStart, Stop) clears it.
          set((s) => {
            if (s.statuses[key] === 'error' && derived === 'idle' && event.event_name === 'Notification') return s
            return { statuses: { ...s.statuses, [key]: derived } }
          })

          // Mark unread when not focused: all 'waiting' statuses are actionable;
          // 'idle' statuses are actionable only if they don't come from a Notification event
          // (idle_prompt/auth_success are informational and should not trigger the red dot).
          const isActionable = derived === 'waiting' || derived === 'error' ||
            (derived === 'idle' && event.event_name !== 'Notification')
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

      clearAllSubagents: () => set({ activeSubagents: {} }),

      clearSubagentsForHost: (hostId) => set((s) => {
        const prefix = `${hostId}:`
        const filtered: Record<string, string[]> = {}
        for (const [k, v] of Object.entries(s.activeSubagents)) {
          if (!k.startsWith(prefix)) filtered[k] = v
        }
        return { activeSubagents: filtered }
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
          events: filterKeys(s.events),
          statuses: filterKeys(s.statuses),
          unread: filterKeys(s.unread),
          activeSubagents: filterKeys(s.activeSubagents),
        }
      }),

      setTabIndicatorStyle: (style) => set({ tabIndicatorStyle: style }),
      setHooksInstalled: (installed) => set({ hooksInstalled: installed }),
    }),
    {
      name: STORAGE_KEYS.AGENT,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({ tabIndicatorStyle: state.tabIndicatorStyle }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.AGENT, useAgentStore)
