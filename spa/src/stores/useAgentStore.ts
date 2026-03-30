// spa/src/stores/useAgentStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { getActiveSessionCode } from '../lib/active-session'

export type AgentStatus = 'running' | 'waiting' | 'idle'
export type TabIndicatorStyle = 'overlay' | 'replace' | 'inline'

export interface AgentHookEvent {
  tmux_session: string
  event_name: string
  raw_event: Record<string, unknown>
  agent_type: string
  broadcast_ts: number
}

interface AgentState {
  events: Record<string, AgentHookEvent>       // latest event per session code
  statuses: Record<string, AgentStatus>        // derived status per session
  unread: Record<string, boolean>              // unread flag per session
  tabIndicatorStyle: TabIndicatorStyle
  hooksInstalled: boolean                      // whether CC hooks are installed

  handleHookEvent: (session: string, event: AgentHookEvent) => void
  markRead: (session: string) => void
  setTabIndicatorStyle: (style: TabIndicatorStyle) => void
  setHooksInstalled: (installed: boolean) => void
}

export function deriveStatus(eventName: string, rawEvent?: Record<string, unknown>): AgentStatus | 'clear' | null {
  switch (eventName) {
    case 'SessionStart':
      // compact is background auto-compaction, not user activity
      if (rawEvent?.source === 'compact') return null
      return 'running'
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
    case 'StopFailure':
      return 'idle'
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
    (set, get) => ({
      events: {},
      statuses: {},
      unread: {},
      tabIndicatorStyle: 'overlay' as TabIndicatorStyle,
      hooksInstalled: false,

      handleHookEvent: (session, event) => {
        const derived = deriveStatus(event.event_name, event.raw_event)

        if (derived === 'clear') {
          // SessionEnd: remove session from all maps
          set((s) => {
            // eslint-disable-next-line @typescript-eslint/no-unused-vars
            const { [session]: _e, ...restEvents } = s.events
            // eslint-disable-next-line @typescript-eslint/no-unused-vars
            const { [session]: _s, ...restStatuses } = s.statuses
            // eslint-disable-next-line @typescript-eslint/no-unused-vars
            const { [session]: _u, ...restUnread } = s.unread
            return { events: restEvents, statuses: restStatuses, unread: restUnread }
          })
          return
        }

        // Store the latest event
        set((s) => ({ events: { ...s.events, [session]: event } }))

        if (derived !== null) {
          // Update status
          set((s) => ({ statuses: { ...s.statuses, [session]: derived } }))

          // Mark unread when not focused: all 'waiting' statuses are actionable;
          // 'idle' statuses are actionable only if they don't come from a Notification event
          // (idle_prompt/auth_success are informational and should not trigger the red dot).
          const isActionable = derived === 'waiting' ||
            (derived === 'idle' && event.event_name !== 'Notification')
          if (isActionable && getActiveSessionCode() !== session) {
            set((s) => ({ unread: { ...s.unread, [session]: true } }))
          }
        }
      },

      markRead: (session) => set((s) => {
        // eslint-disable-next-line @typescript-eslint/no-unused-vars
        const { [session]: _, ...rest } = s.unread
        return { unread: rest }
      }),

      setTabIndicatorStyle: (style) => set({ tabIndicatorStyle: style }),
      setHooksInstalled: (installed) => set({ hooksInstalled: installed }),
    }),
    {
      name: 'tbox-agent',
      partialize: (state) => ({ tabIndicatorStyle: state.tabIndicatorStyle }),
    },
  ),
)
