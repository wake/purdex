// spa/src/stores/useSessionStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { type Session, listSessions } from '../lib/host-api'
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
        const list = await listSessions(hostId)
        set((state) => ({
          sessions: { ...state.sessions, [hostId]: list },
        }))
      },
      replaceHost: (hostId, sessions) =>
        set((state) => ({
          sessions: { ...state.sessions, [hostId]: sessions },
        })),
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
