// spa/src/stores/useStreamStore.ts
import { create } from 'zustand'
import { subscribeWithSelector } from 'zustand/middleware'
import { compositeKey } from '../lib/composite-key'
import type { StreamMessage, ControlRequest, StreamConnection } from '../lib/stream-ws'

export interface PerSessionState {
  messages: StreamMessage[]
  pendingControlRequests: ControlRequest[]
  isStreaming: boolean
  conn: StreamConnection | null
  sessionInfo: { ccSessionId: string; model: string }
  cost: number
}

function defaultPerSession(): PerSessionState {
  return {
    messages: [],
    pendingControlRequests: [],
    isStreaming: false,
    conn: null,
    sessionInfo: { ccSessionId: '', model: '' },
    cost: 0,
  }
}

interface StreamStore {
  // Per-session state
  sessions: Record<string, PerSessionState>

  // Global state (keyed by composite key but not part of PerSessionState)
  relayStatus: Record<string, boolean>
  handoffProgress: Record<string, string>

  // Per-session actions
  addMessage: (hostId: string, sessionCode: string, msg: StreamMessage) => void
  addControlRequest: (hostId: string, sessionCode: string, req: ControlRequest) => void
  resolveControlRequest: (hostId: string, sessionCode: string, requestId: string) => void
  setStreaming: (hostId: string, sessionCode: string, v: boolean) => void
  setSessionInfo: (hostId: string, sessionCode: string, ccSessionId: string, model: string) => void
  addCost: (hostId: string, sessionCode: string, usd: number) => void
  setConn: (hostId: string, sessionCode: string, conn: StreamConnection | null) => void
  loadHistory: (hostId: string, sessionCode: string, messages: StreamMessage[]) => void
  clearSession: (hostId: string, sessionCode: string) => void

  // Global-keyed actions
  setHandoffProgress: (hostId: string, sessionCode: string, progress: string) => void
  setRelayStatus: (hostId: string, sessionCode: string, connected: boolean) => void
}

function getOrCreate(sessions: Record<string, PerSessionState>, key: string): PerSessionState {
  return sessions[key] ?? defaultPerSession()
}

export const useStreamStore = create<StreamStore>()(subscribeWithSelector((set) => ({
  sessions: {},
  relayStatus: {},
  handoffProgress: {},

  addMessage: (hostId, sessionCode, msg) => set((s) => {
    const key = compositeKey(hostId, sessionCode)
    const cur = getOrCreate(s.sessions, key)
    return { sessions: { ...s.sessions, [key]: { ...cur, messages: [...cur.messages, msg] } } }
  }),

  addControlRequest: (hostId, sessionCode, req) => set((s) => {
    const key = compositeKey(hostId, sessionCode)
    const cur = getOrCreate(s.sessions, key)
    return { sessions: { ...s.sessions, [key]: { ...cur, pendingControlRequests: [...cur.pendingControlRequests, req] } } }
  }),

  resolveControlRequest: (hostId, sessionCode, requestId) => set((s) => {
    const key = compositeKey(hostId, sessionCode)
    const cur = getOrCreate(s.sessions, key)
    return { sessions: { ...s.sessions, [key]: { ...cur, pendingControlRequests: cur.pendingControlRequests.filter((r) => r.request_id !== requestId) } } }
  }),

  setStreaming: (hostId, sessionCode, v) => set((s) => {
    const key = compositeKey(hostId, sessionCode)
    const cur = getOrCreate(s.sessions, key)
    return { sessions: { ...s.sessions, [key]: { ...cur, isStreaming: v } } }
  }),

  setSessionInfo: (hostId, sessionCode, ccSessionId, model) => set((s) => {
    const key = compositeKey(hostId, sessionCode)
    const cur = getOrCreate(s.sessions, key)
    return { sessions: { ...s.sessions, [key]: { ...cur, sessionInfo: { ccSessionId, model } } } }
  }),

  addCost: (hostId, sessionCode, usd) => set((s) => {
    const key = compositeKey(hostId, sessionCode)
    const cur = getOrCreate(s.sessions, key)
    return { sessions: { ...s.sessions, [key]: { ...cur, cost: cur.cost + usd } } }
  }),

  setConn: (hostId, sessionCode, conn) => set((s) => {
    const key = compositeKey(hostId, sessionCode)
    const cur = getOrCreate(s.sessions, key)
    return { sessions: { ...s.sessions, [key]: { ...cur, conn } } }
  }),

  // Note: loadHistory replaces all messages. If live messages arrived via
  // addMessage before history loads, they will be lost. In practice this race
  // is narrow (CC waits for user input after --resume), but be aware.
  loadHistory: (hostId, sessionCode, messages) => set((s) => {
    const key = compositeKey(hostId, sessionCode)
    const cur = getOrCreate(s.sessions, key)
    return { sessions: { ...s.sessions, [key]: { ...cur, messages } } }
  }),

  clearSession: (hostId, sessionCode) => {
    const key = compositeKey(hostId, sessionCode)
    // Close conn outside set() to avoid re-entrant mutations
    const cur = useStreamStore.getState().sessions[key]
    cur?.conn?.close()
    set((s) => {
      const { [key]: _cleared, ...rest } = s.sessions // eslint-disable-line @typescript-eslint/no-unused-vars
      return { sessions: rest }
    })
  },

  setHandoffProgress: (hostId, sessionCode, progress) => set((s) => {
    const key = compositeKey(hostId, sessionCode)
    return { handoffProgress: { ...s.handoffProgress, [key]: progress } }
  }),

  setRelayStatus: (hostId, sessionCode, connected) => set((s) => {
    const key = compositeKey(hostId, sessionCode)
    return { relayStatus: { ...s.relayStatus, [key]: connected } }
  }),
})))
