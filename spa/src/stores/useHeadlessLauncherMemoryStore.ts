// spa/src/stores/useHeadlessLauncherMemoryStore.ts — the Headless launcher's
// "what did I pick last time on this host" (P-C spec §4.2): root path and
// sandbox profile, keyed by host id. Local-only convenience, never synced;
// a remembered value the host no longer offers is simply ignored by the form.
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'

export interface HeadlessLauncherMemory {
  root: string
  profile: string
}

interface HeadlessLauncherMemoryState {
  byHost: Record<string, HeadlessLauncherMemory>
  remember: (hostId: string, v: HeadlessLauncherMemory) => void
  forgetHost: (hostId: string) => void
}

export const useHeadlessLauncherMemoryStore = create<HeadlessLauncherMemoryState>()(
  persist(
    (set) => ({
      byHost: {},
      remember: (hostId, v) =>
        set((s) => ({ byHost: { ...s.byHost, [hostId]: { root: v.root, profile: v.profile } } })),
      forgetHost: (hostId) =>
        set((s) => {
          if (!(hostId in s.byHost)) return s
          const byHost = { ...s.byHost }
          delete byHost[hostId]
          return { byHost }
        }),
    }),
    {
      name: STORAGE_KEYS.HEADLESS_LAUNCHER,
      storage: purdexStorage,
      partialize: (s) => ({ byHost: s.byHost }),
    },
  ),
)
