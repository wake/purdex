// spa/src/stores/usePausedStore.ts — per host, the sessions its daemon holds back (`GET /api/team/paused`, UP-5b). Written only
// by lib/team/paused-refresh.ts; never persisted. A host without `team.pause.v1`, or a removed one, has no entry.
import { create } from 'zustand'
import type { PausedSession } from '../lib/team/paused-api'

export interface PausedHostEntry {
  sessions: PausedSession[]
  loadedAt: number
}

interface PausedStoreState {
  byHost: Record<string, PausedHostEntry>
  set: (hostId: string, sessions: PausedSession[], loadedAt: number) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

export const usePausedStore = create<PausedStoreState>()((set) => ({
  byHost: {},
  set: (hostId, sessions, loadedAt) => set((s) => ({ byHost: { ...s.byHost, [hostId]: { sessions, loadedAt } } })),
  forgetHost: (hostId) => set((s) => {
    if (!Object.hasOwn(s.byHost, hostId)) return s
    const { [hostId]: _gone, ...rest } = s.byHost
    return { byHost: rest }
  }),
  reset: () => set({ byHost: {} }),
}))
