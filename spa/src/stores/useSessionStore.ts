// spa/src/stores/useSessionStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { type Session, listSessions } from '../lib/api'
import { useHostStore } from './useHostStore'

interface SessionState {
  sessions: Record<string, Session[]> // hostId → sessions
  activeHostId: string | null
  activeCode: string | null
  fetchHost: (hostId: string, base: string) => Promise<void>
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
      fetchHost: async (hostId: string, base: string) => {
        const list = await listSessions(base)
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
      name: 'tbox-sessions',
      version: 2,
      partialize: (state) => ({
        activeHostId: state.activeHostId,
        activeCode: state.activeCode,
      }),
      migrate: (persisted, version) => {
        if (version < 2) {
          const old = persisted as Record<string, unknown>
          const defaultHostId = useHostStore.getState().hostOrder[0] ?? 'local'
          return {
            sessions: {},
            activeHostId: defaultHostId,
            activeCode: (old.activeId as string) ?? null,
          }
        }
        return persisted
      },
    },
  ),
)
