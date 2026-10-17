// spa/src/stores/useSessionStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { type Session, listSessions } from '../lib/host-api'
import { connectionGeneration, useTeamRosterStore } from './useTeamRosterStore'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

interface SessionState {
  sessions: Record<string, Session[]> // hostId → sessions
  activeHostId: string | null
  activeCode: string | null
  fetchHost: (hostId: string) => Promise<void>
  replaceHost: (hostId: string, sessions: Session[]) => void
  removeHost: (hostId: string) => void
  setActive: (hostId: string | null, code: string | null) => void
}

export const useSessionStore = create<SessionState>()(
  persist(
    (set) => ({
      sessions: {},
      activeHostId: null,
      activeCode: null,
      fetchHost: async (hostId: string) => {
        const gen = connectionGeneration(hostId)
        const list = await listSessions(hostId)
        // The connection it was asked on was dropped meanwhile: the reply may predate a daemon / tmux restart, so it is
        // neither written nor marked live (the new connection fetches its own list).
        if (gen !== connectionGeneration(hostId)) return
        set((state) => ({
          sessions: { ...state.sessions, [hostId]: list },
        }))
        useTeamRosterStore.getState().markSessionsLive(hostId) // after the list is in: member sets read both (R4)
      },
      replaceHost: (hostId, sessions) => {
        set((state) => ({
          sessions: { ...state.sessions, [hostId]: sessions },
        }))
        useTeamRosterStore.getState().markSessionsLive(hostId)
      },
      removeHost: (hostId) =>
        set((state) => {
           
          const { [hostId]: _, ...rest } = state.sessions
          const activeHostId = state.activeHostId === hostId ? null : state.activeHostId
          const activeCode = state.activeHostId === hostId ? null : state.activeCode
          return { sessions: rest, activeHostId, activeCode }
        }),
      setActive: (hostId, code) => set({ activeHostId: hostId, activeCode: code }),
    }),
    {
      name: STORAGE_KEYS.SESSIONS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        activeHostId: state.activeHostId,
        activeCode: state.activeCode,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.SESSIONS, useSessionStore)
